package isureal

import (
	"context"
	"net/http"
	"net/url"
	"sync"

	"github.com/labstack/echo/v5"
)

// graphEntry はフィードの友達判定に使う集合。フォローは持たない。
type graphEntry struct {
	Friends map[string]bool
	Blocked map[string]bool
}

// GraphCache は利用者ごとの友達とブロックをプロセス内に持つ。
// 書き込みのあと Put が古い読み取りを戻さないよう、SessionCache と同じ世代で破棄を覚える。
type GraphCache struct {
	mu               sync.RWMutex
	generation       uint64
	entries          map[string]graphEntry
	purgedAtByUserID map[string]uint64
	purgedAllAt      uint64
}

func NewGraphCache() *GraphCache {
	return &GraphCache{entries: map[string]graphEntry{}, purgedAtByUserID: map[string]uint64{}}
}

func (c *GraphCache) Get(userID string) (friends, blocked map[string]bool, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[userID]
	if !ok {
		return nil, nil, false
	}
	return copyIDSet(entry.Friends), copyIDSet(entry.Blocked), true
}

// Generation は DB を読む前に取得して Put に渡す。
func (c *GraphCache) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// Put は generation の取得後にその利用者の破棄か全破棄があったら保存しない。
func (c *GraphCache) Put(generation uint64, userID string, friends, blocked map[string]bool) {
	friends = copyIDSet(friends)
	blocked = copyIDSet(blocked)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.purgedAllAt > generation || c.purgedAtByUserID[userID] > generation {
		return
	}
	c.entries[userID] = graphEntry{Friends: friends, Blocked: blocked}
}

func (c *GraphCache) PurgeUser(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeUserLocked(userID)
}

func (c *GraphCache) purgeUserLocked(userID string) {
	c.generation++
	delete(c.entries, userID)
	c.purgedAtByUserID[userID] = c.generation
}

func (c *GraphCache) PurgeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeAllLocked()
}

func (c *GraphCache) Clear() {
	c.PurgeAll()
}

func (c *GraphCache) purgeAllLocked() {
	c.generation++
	clear(c.entries)
	clear(c.purgedAtByUserID)
	c.purgedAllAt = c.generation
}

func copyIDSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for id, ok := range in {
		if ok {
			out[id] = true
		}
	}
	return out
}

// purgeGraphUsers は友達かブロックが変わった両利用者を、応答の前に全アプリサーバから捨てる。
func purgeGraphUsers(ctx context.Context, h *HandlerContext, userIDs ...string) error {
	seen := make(map[string]bool, len(userIDs))
	ids := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		h.Graph.PurgeUser(id)
	}
	for region, peerURL := range h.Config.PeerURLs {
		if region == h.Config.Region {
			continue
		}
		for _, id := range ids {
			if err := requestPeerPurge(ctx, peerURL+"/internal/graph-cache/"+url.PathEscape(id)); err != nil {
				return err
			}
		}
	}
	return nil
}

func purgeAllGraphs(ctx context.Context, h *HandlerContext) error {
	h.Graph.Clear()
	for region, peerURL := range h.Config.PeerURLs {
		if region == h.Config.Region {
			continue
		}
		if err := requestPeerPurge(ctx, peerURL+"/internal/graph-cache"); err != nil {
			return err
		}
	}
	return nil
}

func purgeAllGraphHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h.Graph.Clear()
		return c.NoContent(http.StatusNoContent)
	}
}

func purgeUserGraphHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h.Graph.PurgeUser(c.Param("userId"))
		return c.NoContent(http.StatusNoContent)
	}
}
