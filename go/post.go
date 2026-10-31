package isureal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type PostLocation struct {
	Kind      string   `json:"kind" db:"location_kind"`
	Latitude  *float64 `json:"latitude,omitempty" db:"latitude"`
	Longitude *float64 `json:"longitude,omitempty" db:"longitude"`
	Locality  *string  `json:"locality,omitempty" db:"locality"`
}

func (location PostLocation) valid() bool {
	switch location.Kind {
	case "none":
		return location.Latitude == nil && location.Longitude == nil && location.Locality == nil
	case "exact":
		return location.Latitude != nil && location.Longitude != nil && location.Locality == nil &&
			*location.Latitude >= -90 && *location.Latitude <= 90 && *location.Longitude >= -180 && *location.Longitude <= 180
	case "approximate":
		return location.Latitude == nil && location.Longitude == nil && location.Locality != nil && validUserText(*location.Locality, 1, 100)
	default:
		return false
	}
}

type Post struct {
	ID              string `db:"id"`
	AuthorID        string `db:"author_id"`
	PostingWindowID string `db:"posting_window_id"`
	ImageID         string `db:"image_id"`
	Caption         string `db:"caption"`
	Visibility      string `db:"visibility"`
	PostLocation
	PostedAt       time.Time  `db:"posted_at"`
	OnTimeDeadline time.Time  `db:"on_time_deadline"`
	WindowEndedAt  *time.Time `db:"window_ended_at"`
}

const postColumns = `p.id, p.author_id, p.posting_window_id, p.image_id, p.caption, p.visibility,
  p.location_kind, p.latitude, p.longitude, p.locality, p.posted_at,
  w.on_time_deadline, w.ended_at AS window_ended_at`

func getPost(ctx context.Context, db sqlx.QueryerContext, postID string) (Post, error) {
	var post Post
	err := sqlx.GetContext(ctx, db, &post, `
  SELECT `+postColumns+`
  FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  WHERE p.id = $1 AND p.deleted_at IS NULL
 `, postID)
	return post, err
}

type PostWithAuthor struct {
	Post
	Author User `db:"author"`
}

func selectPostsWithAuthor(ctx context.Context, db sqlx.QueryerContext, conditions string, args ...any) ([]PostWithAuthor, error) {
	var posts []PostWithAuthor
	if err := sqlx.SelectContext(ctx, db, &posts, `
  SELECT `+postColumns+`, `+userColumnsAs("u", "author")+`
  FROM posts p
  JOIN posting_windows w ON w.id = p.posting_window_id
  JOIN users u ON u.id = p.author_id AND u.status = 'active'
  WHERE p.deleted_at IS NULL `+conditions, args...); err != nil {
		return nil, err
	}
	for i := range posts {
		if err := posts[i].Author.setImageKey(); err != nil {
			return nil, err
		}
	}
	return posts, nil
}

func getPostWithAuthor(ctx context.Context, db sqlx.QueryerContext, postID string) (PostWithAuthor, error) {
	posts, err := selectPostsWithAuthor(ctx, db, `AND p.id = $1`, postID)
	if err != nil {
		return PostWithAuthor{}, err
	}
	if len(posts) == 0 {
		return PostWithAuthor{}, sql.ErrNoRows
	}
	return posts[0], nil
}

type viewerContext struct {
	User
	HasPostedToday   bool
	Friends          map[string]bool
	FriendsOfFriends map[string]bool
	MutualFriends    map[string]int
	Blocked          map[string]bool
}

// loadViewerContext は europe と america からは WAN 越しになるので、1 往復で取る。
// friends_of_friends は友達の友達ごとの共通の友達の数で、友達がいれば本人も含む。
func loadViewerContext(ctx context.Context, db sqlx.QueryerContext, viewerID string) (viewerContext, error) {
	viewer := viewerContext{Friends: map[string]bool{}, FriendsOfFriends: map[string]bool{}, MutualFriends: map[string]int{}, Blocked: map[string]bool{}}
	var row struct {
		User
		HasPostedToday   bool   `db:"has_posted_today"`
		BlockedIDs       []byte `db:"blocked_ids"`
		FriendIDs        []byte `db:"friend_ids"`
		FriendsOfFriends []byte `db:"friends_of_friends"`
	}
	if err := sqlx.GetContext(ctx, db, &row, `
  WITH viewer AS (
    SELECT `+strings.Join(userColumns, ", ")+` FROM users WHERE id = $1 AND status = 'active'
  ), friends AS (
    SELECT user_high_id AS id FROM friendships WHERE user_low_id = $1
    UNION ALL
    SELECT user_low_id FROM friendships WHERE user_high_id = $1
  ), friend_edges AS (
    SELECT s.user_high_id AS id FROM friends f JOIN friendships s ON s.user_low_id = f.id
    UNION ALL
    SELECT s.user_low_id FROM friends f JOIN friendships s ON s.user_high_id = f.id
  ), friends_of_friends AS (
    SELECT id, count(*) AS mutual_friends FROM friend_edges GROUP BY id
  )
  SELECT viewer.*,
    EXISTS(
      SELECT 1 FROM posts p
      JOIN posting_windows posted ON posted.id = p.posting_window_id
      JOIN posting_windows current ON current.region = viewer.region AND current.ended_at IS NULL
                                  AND current.local_date = posted.local_date
      WHERE p.author_id = viewer.id AND p.deleted_at IS NULL
    ) AS has_posted_today,
    (SELECT coalesce(json_agg(CASE WHEN blocker_id = $1 THEN blocked_id ELSE blocker_id END), '[]')
     FROM block_relations WHERE blocker_id = $1 OR blocked_id = $1) AS blocked_ids,
    (SELECT coalesce(json_agg(id), '[]') FROM friends) AS friend_ids,
    (SELECT coalesce(json_object_agg(id, mutual_friends), '{}') FROM friends_of_friends) AS friends_of_friends
  FROM viewer
 `, viewerID); err != nil {
		return viewer, err
	}
	viewer.User = row.User
	viewer.HasPostedToday = row.HasPostedToday
	var blockedIDs, friendIDs []string
	var friendsOfFriends map[string]int
	if err := json.Unmarshal(row.BlockedIDs, &blockedIDs); err != nil {
		return viewer, err
	}
	if err := json.Unmarshal(row.FriendIDs, &friendIDs); err != nil {
		return viewer, err
	}
	if err := json.Unmarshal(row.FriendsOfFriends, &friendsOfFriends); err != nil {
		return viewer, err
	}
	for _, id := range blockedIDs {
		viewer.Blocked[id] = true
	}
	for _, id := range friendIDs {
		viewer.Friends[id] = true
	}
	for id, mutualFriends := range friendsOfFriends {
		viewer.FriendsOfFriends[id] = true
		if id != viewer.ID {
			viewer.MutualFriends[id] = mutualFriends
		}
	}
	if err := viewer.setImageKey(); err != nil {
		return viewer, err
	}
	return viewer, nil
}

func (viewer viewerContext) canView(post Post) bool {
	if post.AuthorID == viewer.ID {
		return true
	}
	if post.WindowEndedAt != nil || !viewer.HasPostedToday || viewer.Blocked[post.AuthorID] {
		return false
	}
	switch post.Visibility {
	case "public":
		return true
	case "friends":
		return viewer.Friends[post.AuthorID]
	case "friends_of_friends":
		return viewer.Friends[post.AuthorID] || viewer.FriendsOfFriends[post.AuthorID]
	default:
		return false
	}
}

func canViewPost(ctx context.Context, db sqlx.QueryerContext, viewerID, postID string) (bool, error) {
	post, err := getPostWithAuthor(ctx, db, postID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	viewer, err := loadViewerContext(ctx, db, viewerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return viewer.canView(post.Post), nil
}

func getFriendIDs(ctx context.Context, db sqlx.QueryerContext, userID string) ([]string, error) {
	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, `SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id = $1 OR user_high_id = $1`, userID); err != nil {
		return nil, err
	}
	var ids []string
	for _, friendship := range friendships {
		if friendship.UserLowID == userID {
			ids = append(ids, friendship.UserHighID)
		} else {
			ids = append(ids, friendship.UserLowID)
		}
	}
	return ids, nil
}

func getBlockedUserIDs(ctx context.Context, db sqlx.QueryerContext, userID string) (map[string]bool, error) {
	var ids []string
	if err := sqlx.SelectContext(ctx, db, &ids, `
  SELECT CASE WHEN blocker_id = $1 THEN blocked_id ELSE blocker_id END
  FROM block_relations WHERE blocker_id = $1 OR blocked_id = $1
 `, userID); err != nil {
		return nil, err
	}
	blocked := make(map[string]bool, len(ids))
	for _, id := range ids {
		blocked[id] = true
	}
	return blocked, nil
}
