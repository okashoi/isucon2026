package isureal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

// ActivePostStore は ended_at が NULL の投稿だけを持つ。
// 著者で友達フィードと今日の投稿を引き、リージョンで期間が閉じた投稿を落とす。
// live は、このプロセスが計測中の投稿を正本として扱えることを示す。
// initialize の Clear と再起動の直後は false で、その間の友達フィードは DB を読む。
type ActivePostStore struct {
	mu       sync.RWMutex
	live     bool
	byID     map[string]PostWithAuthor
	byAuthor map[string]map[string]struct{}
	byRegion map[string]map[string]struct{}
}

func NewActivePostStore() *ActivePostStore {
	return &ActivePostStore{
		byID:     map[string]PostWithAuthor{},
		byAuthor: map[string]map[string]struct{}{},
		byRegion: map[string]map[string]struct{}{},
	}
}

func (s *ActivePostStore) Live() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.live
}

func (s *ActivePostStore) MarkLive() {
	s.mu.Lock()
	s.live = true
	s.mu.Unlock()
}

func (s *ActivePostStore) Add(post PostWithAuthor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.byID[post.ID]; ok {
		s.unlinkLocked(old)
	}
	s.linkLocked(post)
	s.live = true
}

func (s *ActivePostStore) Remove(postID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	post, ok := s.byID[postID]
	if !ok {
		return
	}
	s.unlinkLocked(post)
}

func (s *ActivePostStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID = map[string]PostWithAuthor{}
	s.byAuthor = map[string]map[string]struct{}{}
	s.byRegion = map[string]map[string]struct{}{}
	s.live = false
}

// DropOtherWindows は region の投稿のうち、開いている windowID 以外を消す。
// 次の投稿期間が始まると、前の期間の投稿は友達フィードに残らない。
func (s *ActivePostStore) DropOtherWindows(region, openWindowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.byRegion[region] {
		post := s.byID[id]
		if post.PostingWindowID == openWindowID {
			continue
		}
		s.unlinkLocked(post)
	}
}

func (s *ActivePostStore) HasPostedToday(authorID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byAuthor[authorID]) > 0
}

// FriendsFeed は hit が false のとき正本ではない。呼び出し側は DB を読む。
func (s *ActivePostStore) FriendsFeed(viewer viewerContext, beforePostedAt *time.Time, beforeID string, limit int) (items []PostWithAuthor, hit bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.live {
		return nil, false
	}
	items = make([]PostWithAuthor, 0)
	for authorID := range viewer.Friends {
		if authorID == viewer.ID || viewer.Blocked[authorID] {
			continue
		}
		for postID := range s.byAuthor[authorID] {
			post := s.byID[postID]
			if !friendsFeedVisibility(post.Visibility) || !feedSeekBefore(post.Post, beforePostedAt, beforeID) {
				continue
			}
			items = append(items, post)
		}
	}
	slices.SortFunc(items, func(a, b PostWithAuthor) int {
		if a.PostedAt.After(b.PostedAt) {
			return -1
		}
		if a.PostedAt.Before(b.PostedAt) {
			return 1
		}
		if a.ID == b.ID {
			return 0
		}
		if a.ID > b.ID {
			return -1
		}
		return 1
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, true
}

func (s *ActivePostStore) linkLocked(post PostWithAuthor) {
	s.byID[post.ID] = post
	if s.byAuthor[post.AuthorID] == nil {
		s.byAuthor[post.AuthorID] = map[string]struct{}{}
	}
	s.byAuthor[post.AuthorID][post.ID] = struct{}{}
	region := post.Author.Region
	if s.byRegion[region] == nil {
		s.byRegion[region] = map[string]struct{}{}
	}
	s.byRegion[region][post.ID] = struct{}{}
}

func (s *ActivePostStore) unlinkLocked(post PostWithAuthor) {
	delete(s.byID, post.ID)
	if posts := s.byAuthor[post.AuthorID]; posts != nil {
		delete(posts, post.ID)
		if len(posts) == 0 {
			delete(s.byAuthor, post.AuthorID)
		}
	}
	if posts := s.byRegion[post.Author.Region]; posts != nil {
		delete(posts, post.ID)
		if len(posts) == 0 {
			delete(s.byRegion, post.Author.Region)
		}
	}
}

func friendsFeedVisibility(visibility string) bool {
	switch visibility {
	case "public", "friends", "friends_of_friends":
		return true
	default:
		return false
	}
}

func feedSeekBefore(post Post, before *time.Time, beforeID string) bool {
	if before == nil {
		return true
	}
	if post.PostedAt.Equal(*before) {
		return post.ID < beforeID
	}
	return post.PostedAt.Before(*before)
}

// activePostWire はピア間の PostWithAuthor。
// 公開の User JSON は署名鍵とプロフィール画像 ID を落とす。友達フィードの画像 URL を揃えるため、内部の転送だけ足す。
type activePostWire struct {
	PostWithAuthor
	SecretKeyHash  string  `json:"secretKeyHash"`
	ProfileImageID *string `json:"profileImageId"`
}

func newActivePostWire(post PostWithAuthor) activePostWire {
	return activePostWire{
		PostWithAuthor: post,
		SecretKeyHash:  post.Author.SecretKeyHash,
		ProfileImageID: post.Author.ProfileImageID,
	}
}

func (w activePostWire) decode() (PostWithAuthor, error) {
	post := w.PostWithAuthor
	post.Author.SecretKeyHash = w.SecretKeyHash
	post.Author.ProfileImageID = w.ProfileImageID
	if err := post.Author.setImageKey(); err != nil {
		return PostWithAuthor{}, err
	}
	return post, nil
}

func publishActivePost(ctx context.Context, h *HandlerContext, post PostWithAuthor) error {
	h.Active.Add(post)
	body, err := json.Marshal(newActivePostWire(post))
	if err != nil {
		return err
	}
	return forEachPeer(h, func(peerURL string) error {
		return postPeer(ctx, peerURL+"/internal/active-posts", body)
	})
}

func withdrawActivePost(ctx context.Context, h *HandlerContext, postID string) error {
	h.Active.Remove(postID)
	return forEachPeer(h, func(peerURL string) error {
		return requestPeerPurge(ctx, peerURL+"/internal/active-posts/"+url.PathEscape(postID))
	})
}

func purgeAllActivePosts(ctx context.Context, h *HandlerContext) error {
	h.Active.Clear()
	return forEachPeer(h, func(peerURL string) error {
		return requestPeerPurge(ctx, peerURL+"/internal/active-posts")
	})
}

func forEachPeer(h *HandlerContext, fn func(peerURL string) error) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(h.Config.PeerURLs))
	for region, peerURL := range h.Config.PeerURLs {
		if region == h.Config.Region || peerURL == "" {
			continue
		}
		wg.Add(1)
		go func(peerURL string) {
			defer wg.Done()
			if err := fn(peerURL); err != nil {
				errCh <- err
			}
		}(peerURL)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return err
	}
	return nil
}

func postPeer(ctx context.Context, target string, body []byte) error {
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create peer active post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := peerClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to publish peer active post: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to publish peer active post: %s: %s", target, res.Status)
	}
	return nil
}

func postActivePostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var wire activePostWire
		if err := c.Bind(&wire); err != nil {
			return err
		}
		post, err := wire.decode()
		if err != nil {
			return err
		}
		if post.ID == "" || post.AuthorID == "" {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		h.Active.Add(post)
		return c.NoContent(http.StatusNoContent)
	}
}

func deleteActivePostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h.Active.Remove(c.Param("postId"))
		return c.NoContent(http.StatusNoContent)
	}
}

func purgeActivePostsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h.Active.Clear()
		return c.NoContent(http.StatusNoContent)
	}
}
