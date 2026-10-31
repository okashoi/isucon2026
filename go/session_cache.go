package isureal

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

const peerRequestTimeout = 5 * time.Second

var peerClient = &http.Client{Timeout: peerRequestTimeout}

// SessionCache は token_hash からログインセッションを引く。
// 持つのはサーバのリージョンに所属する利用者のセッションだけ。
// asia のように同一リージョンに複数台あるときは、PeerURLs の全台へ破棄を送る。
type SessionCache struct {
	mu                sync.RWMutex
	generation        uint64
	sessions          map[string]Session
	tokenHashByUserID map[string]string
}

func NewSessionCache() *SessionCache {
	return &SessionCache{sessions: map[string]Session{}, tokenHashByUserID: map[string]string{}}
}

func (c *SessionCache) Get(tokenHash string) (*Session, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	session, ok := c.sessions[tokenHash]
	return &session, ok
}

// Generation は DB からセッションを読む前に取得して Add に渡す。
func (c *SessionCache) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// Add は generation の取得後に破棄があったら保存しない。DB から読んだ値が破棄より前のものかもしれないため。
func (c *SessionCache) Add(generation uint64, session Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	delete(c.sessions, c.tokenHashByUserID[session.UserID])
	c.sessions[session.TokenHash] = session
	c.tokenHashByUserID[session.UserID] = session.TokenHash
}

func (c *SessionCache) PurgeUser(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	delete(c.sessions, c.tokenHashByUserID[userID])
	delete(c.tokenHashByUserID, userID)
}

func (c *SessionCache) PurgeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	clear(c.sessions)
	clear(c.tokenHashByUserID)
}

// purgeUserSession はセッションや所属リージョンの変更を commit したあと、応答を返す前に呼ぶ。
// region は変更前の所属リージョン。
func purgeUserSession(ctx context.Context, h *HandlerContext, region, userID string) error {
	if region == h.Config.Region {
		h.Sessions.PurgeUser(userID)
	}
	path := "/internal/session-cache/" + url.PathEscape(userID)
	for _, peerURL := range h.Config.PeerURLs[region] {
		if err := requestPeerPurge(ctx, peerURL+path); err != nil {
			return err
		}
	}
	return nil
}

func purgeAllSessions(ctx context.Context, h *HandlerContext) error {
	h.Sessions.PurgeAll()
	for region, peerURLs := range h.Config.PeerURLs {
		if region == h.Config.Region {
			continue
		}
		for _, peerURL := range peerURLs {
			if err := requestPeerPurge(ctx, peerURL+"/internal/session-cache"); err != nil {
				return err
			}
		}
	}
	// 同一リージョンの他台（asia の s4 など）も消す。
	for _, peerURL := range h.Config.PeerURLs[h.Config.Region] {
		if err := requestPeerPurge(ctx, peerURL+"/internal/session-cache"); err != nil {
			return err
		}
	}
	return nil
}

// requestPeerPurge は commit 済みの変更を反映するので、クライアントが切断しても中断しない。
func requestPeerPurge(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodDelete, target, nil)
	if err != nil {
		return fmt.Errorf("failed to create peer cache purge request: %w", err)
	}
	res, err := peerClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to purge peer cache: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to purge peer cache: %s: %s", target, res.Status)
	}
	return nil
}

func purgeAllSessionsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h.Sessions.PurgeAll()
		return c.NoContent(http.StatusNoContent)
	}
}

func purgeUserSessionHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h.Sessions.PurgeUser(c.Param("userId"))
		return c.NoContent(http.StatusNoContent)
	}
}
