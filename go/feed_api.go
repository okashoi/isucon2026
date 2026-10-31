package isureal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// feedPageLimit は 1 ページ 50 件の次があるかを見るための件数。
const feedPageLimit = 51

type getFeedRequest struct {
	Feed           string     `param:"feed"`
	BeforeScore    *int64     `query:"beforeScore"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type feedItemResponse struct {
	createPostResponse
	Score *int64 `json:"score,omitempty"`
}

type getFeedResponse struct {
	Items   []feedItemResponse `json:"items"`
	HasMore bool               `json:"hasMore"`
}

func getFeedHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getFeedRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		switch input.Feed {
		case "friends", "friends-of-friends", "public", "following":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if (input.BeforePostedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ranked := input.Feed == "friends-of-friends"
		if ranked && (input.BeforeScore == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		viewer, err := loadViewerContext(ctx, h.ReadDB, GetSession(c).UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []feedItemResponse{}})
		}
		if err != nil {
			return err
		}
		if !viewer.HasPostedToday {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []feedItemResponse{}})
		}
		following := map[string]bool{}
		if input.Feed == "following" {
			var followeeIDs []string
			if err := h.ReadDB.SelectContext(ctx, &followeeIDs, `SELECT followee_id FROM follows WHERE follower_id = $1`, viewer.ID); err != nil {
				return err
			}
			for _, id := range followeeIDs {
				following[id] = true
			}
		}
		beforePostedAt, beforeID := input.BeforePostedAt, input.BeforeID
		if ranked {
			beforePostedAt, beforeID = nil, ""
		}
		posts, err := selectFeedPosts(ctx, h.ReadDB, viewer, following, input.Feed, beforePostedAt, beforeID)
		if err != nil {
			return err
		}
		candidates := make([]PostWithAuthor, 0)
		for _, post := range posts {
			if viewer.canView(post.Post) && matchesFeed(viewer, following, post.Post, input.Feed) {
				candidates = append(candidates, post)
			}
		}
		var scores map[string]int64
		if ranked {
			if scores, err = recommendationScores(ctx, h.ReadDB, viewer, candidates); err != nil {
				return err
			}
			sort.SliceStable(candidates, func(i, j int) bool {
				return scores[candidates[i].ID] > scores[candidates[j].ID]
			})
		}
		items := make([]feedItemResponse, 0)
		for _, post := range candidates {
			score := scores[post.ID]
			if ranked && input.BeforeScore != nil && !rankedBefore(score, post.Post, *input.BeforeScore, *input.BeforePostedAt, input.BeforeID) {
				continue
			}
			item := feedItemResponse{createPostResponse: newPostResponse(post.Post, post.Author)}
			if ranked {
				item.Score = &score
			}
			items = append(items, item)
			if len(items) == feedPageLimit {
				break
			}
		}
		hasMore := len(items) == feedPageLimit
		if hasMore {
			items = items[:feedPageLimit-1]
		}
		return c.JSON(http.StatusOK, getFeedResponse{Items: items, HasMore: hasMore})
	}
}

// selectFeedPosts は canView と matchesFeed と同じ投稿だけを読む。
// 友達・フォロー・公開は posted_at 順の 51 件で止める。ここで余分な行を返すと、後段が落とした分だけページが欠ける。
// 友達の友達はスコア順なので、時刻では切らず作者で絞る。
func selectFeedPosts(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, following map[string]bool, feed string, beforePostedAt *time.Time, beforeID string) ([]PostWithAuthor, error) {
	switch feed {
	case "friends":
		authors := idList(viewer.Friends, viewer.ID, viewer.Blocked)
		return selectAuthorFeed(ctx, db, viewer.ID, authors, []string{"public", "friends", "friends_of_friends"}, beforePostedAt, beforeID, true)
	case "public":
		return selectPublicFeed(ctx, db, viewer, beforePostedAt, beforeID)
	case "following":
		return selectFollowingFeed(ctx, db, viewer, following, beforePostedAt, beforeID)
	case "friends-of-friends":
		authors := idList(viewer.FriendsOfFriends, viewer.ID, viewer.Friends, viewer.Blocked)
		return selectAuthorFeed(ctx, db, viewer.ID, authors, []string{"public", "friends_of_friends"}, nil, "", false)
	default:
		return []PostWithAuthor{}, nil
	}
}

func selectAuthorFeed(ctx context.Context, db sqlx.QueryerContext, viewerID string, authors, visibilities []string, before *time.Time, beforeID string, limit bool) ([]PostWithAuthor, error) {
	if len(authors) == 0 {
		return []PostWithAuthor{}, nil
	}
	condition := `
  AND w.ended_at IS NULL
  AND p.author_id <> ?
  AND p.author_id IN (?)
  AND p.visibility IN (?)`
	args := []any{viewerID, authors, visibilities}
	condition, args = appendFeedSeek(condition, args, before, beforeID)
	condition += ` ORDER BY p.posted_at DESC, p.id DESC`
	if limit {
		condition += fmt.Sprintf(` LIMIT %d`, feedPageLimit)
	}
	return queryFeedPosts(ctx, db, condition, args)
}

func selectPublicFeed(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, before *time.Time, beforeID string) ([]PostWithAuthor, error) {
	condition := `
  AND w.ended_at IS NULL
  AND p.author_id <> ?
  AND p.visibility = 'public'`
	args := []any{viewer.ID}
	if blocked := idList(viewer.Blocked, ""); len(blocked) > 0 {
		condition += ` AND p.author_id NOT IN (?)`
		args = append(args, blocked)
	}
	condition, args = appendFeedSeek(condition, args, before, beforeID)
	condition += fmt.Sprintf(` ORDER BY p.posted_at DESC, p.id DESC LIMIT %d`, feedPageLimit)
	return queryFeedPosts(ctx, db, condition, args)
}

func selectFollowingFeed(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, following map[string]bool, before *time.Time, beforeID string) ([]PostWithAuthor, error) {
	followees := make([]string, 0, len(following))
	for id := range following {
		if id == viewer.ID || viewer.Blocked[id] {
			continue
		}
		followees = append(followees, id)
	}
	slices.Sort(followees)
	if len(followees) == 0 {
		return []PostWithAuthor{}, nil
	}
	visibilitySQL, visibilityArgs := followingVisibilitySQL(viewer)
	condition := `
  AND w.ended_at IS NULL
  AND p.author_id <> ?
  AND p.author_id IN (?)
  AND ` + visibilitySQL
	args := append([]any{viewer.ID, followees}, visibilityArgs...)
	condition, args = appendFeedSeek(condition, args, before, beforeID)
	condition += fmt.Sprintf(` ORDER BY p.posted_at DESC, p.id DESC LIMIT %d`, feedPageLimit)
	return queryFeedPosts(ctx, db, condition, args)
}

// followingVisibilitySQL はフォロー中の作者に対する canView の公開範囲。
// 友達なら 3 種、友達の友達なら public と friends_of_friends、それ以外は public だけ。
func followingVisibilitySQL(viewer viewerContext) (string, []any) {
	parts := []string{"p.visibility = 'public'"}
	var args []any
	if friends := idList(viewer.Friends, viewer.ID); len(friends) > 0 {
		parts = append(parts, "(p.visibility = 'friends' AND p.author_id IN (?))")
		args = append(args, friends)
	}
	friendOrFoF := make(map[string]bool, len(viewer.Friends)+len(viewer.FriendsOfFriends))
	for id := range viewer.Friends {
		friendOrFoF[id] = true
	}
	for id := range viewer.FriendsOfFriends {
		friendOrFoF[id] = true
	}
	if visible := idList(friendOrFoF, viewer.ID); len(visible) > 0 {
		parts = append(parts, "(p.visibility = 'friends_of_friends' AND p.author_id IN (?))")
		args = append(args, visible)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func appendFeedSeek(condition string, args []any, before *time.Time, beforeID string) (string, []any) {
	if before == nil {
		return condition, args
	}
	return condition + ` AND (p.posted_at, p.id) < (?::timestamptz, ?)`, append(args, *before, beforeID)
}

func queryFeedPosts(ctx context.Context, db sqlx.QueryerContext, condition string, args []any) ([]PostWithAuthor, error) {
	condition, args, err := sqlx.In(condition, args...)
	if err != nil {
		return nil, err
	}
	return selectPostsWithAuthor(ctx, db, sqlx.Rebind(sqlx.DOLLAR, condition), args...)
}

func idList(include map[string]bool, self string, skip ...map[string]bool) []string {
	if len(include) == 0 {
		return nil
	}
	ids := make([]string, 0, len(include))
next:
	for id := range include {
		if self != "" && id == self {
			continue
		}
		for _, skipped := range skip {
			if skipped[id] {
				continue next
			}
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func rankedBefore(score int64, post Post, beforeScore int64, beforePostedAt time.Time, beforeID string) bool {
	if score != beforeScore {
		return score < beforeScore
	}
	if !post.PostedAt.Equal(beforePostedAt) {
		return post.PostedAt.Before(beforePostedAt)
	}
	return post.ID < beforeID
}

func recommendationScores(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, posts []PostWithAuthor) (map[string]int64, error) {
	scores := make(map[string]int64, len(posts))
	if len(posts) == 0 {
		return scores, nil
	}
	authorIDs := make([]string, 0, len(posts))
	postIDs := make([]string, 0, len(posts))
	for _, post := range posts {
		authorIDs = append(authorIDs, post.AuthorID)
		postIDs = append(postIDs, post.ID)
	}

	query, args, err := sqlx.In(`SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (?) OR user_high_id IN (?)`, authorIDs, authorIDs)
	if err != nil {
		return nil, err
	}
	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	connections := map[string]int64{}
	for _, friendship := range friendships {
		connections[friendship.UserLowID] += int64(viewer.MutualFriends[friendship.UserHighID])
		connections[friendship.UserHighID] += int64(viewer.MutualFriends[friendship.UserLowID])
	}

	query, args, err = sqlx.In(`
  SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
  JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.post_id IN (?) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
  UNION
  SELECT c.post_id, c.author_id AS user_id FROM comments c
  JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.post_id IN (?) AND c.deleted_at IS NULL
 `, postIDs, postIDs)
	if err != nil {
		return nil, err
	}
	var engagements []struct {
		PostID string `db:"post_id"`
		UserID string `db:"user_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &engagements, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	friendEngagements := map[string]int64{}
	for _, engagement := range engagements {
		if viewer.Friends[engagement.UserID] {
			friendEngagements[engagement.PostID]++
		}
	}

	for _, post := range posts {
		scores[post.ID] = 10*friendEngagements[post.ID] + 3*int64(viewer.MutualFriends[post.AuthorID]) + min(connections[post.AuthorID], 30)
	}
	return scores, nil
}

func matchesFeed(viewer viewerContext, following map[string]bool, post Post, feed string) bool {
	switch feed {
	case "public":
		return post.Visibility == "public"
	case "following":
		return following[post.AuthorID]
	case "friends":
		return viewer.Friends[post.AuthorID]
	case "friends-of-friends":
		return !viewer.Friends[post.AuthorID] && viewer.FriendsOfFriends[post.AuthorID]
	default:
		return false
	}
}
