package isureal

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

const (
	abuseLogin            = "login"
	abuseInvalidImage     = "invalid_image"
	abuseInvalidSignature = "invalid_signature"
)

// AbuseDetector は IP ごと、種類ごとに、集計期間内の失敗の時刻を持つ。
// 回数はサーバごとに数える。
type AbuseDetector struct {
	mu       sync.Mutex
	counters map[string]*abuseCounter
}

func NewAbuseDetector() *AbuseDetector {
	return &AbuseDetector{counters: map[string]*abuseCounter{
		abuseLogin:            newAbuseCounter(10, 10*time.Second),
		abuseInvalidImage:     newAbuseCounter(2, 5*time.Second),
		abuseInvalidSignature: newAbuseCounter(3, 5*time.Second),
	}}
}

// Observe は失敗を記録し、BAN すべきなら true を返す。
func (d *AbuseDetector) Observe(kind, ip string, at time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counters[kind].observe(ip, at)
}

func (d *AbuseDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, counter := range d.counters {
		clear(counter.failures)
	}
}

type abuseCounter struct {
	threshold int
	window    time.Duration
	failures  map[string][]time.Time
	lastSweep time.Time
}

func newAbuseCounter(threshold int, window time.Duration) *abuseCounter {
	return &abuseCounter{threshold: threshold, window: window, failures: map[string][]time.Time{}}
}

// observe は集計期間内の失敗が threshold に達したら true を返し、その IP を数え直す。
func (c *abuseCounter) observe(ip string, at time.Time) bool {
	c.sweep(at)
	kept := c.failures[ip][:0]
	for _, failedAt := range c.failures[ip] {
		if at.Sub(failedAt) < c.window {
			kept = append(kept, failedAt)
		}
	}
	kept = append(kept, at)
	if len(kept) >= c.threshold {
		delete(c.failures, ip)
		return true
	}
	c.failures[ip] = kept
	return false
}

// sweep は集計期間ごとに、期間内の失敗がなくなった IP を捨てる。
func (c *abuseCounter) sweep(now time.Time) {
	if now.Sub(c.lastSweep) < c.window {
		return
	}
	c.lastSweep = now
	for ip, times := range c.failures {
		if now.Sub(times[len(times)-1]) >= c.window {
			delete(c.failures, ip)
		}
	}
}

// purgeAllAbuseState は全リージョンのサーバで、検知の回数とサーバごとの BAN を消す。
func purgeAllAbuseState(ctx context.Context, h *HandlerContext) error {
	h.clearAbuseState()
	for region, peerURL := range h.Config.PeerURLs {
		if region == h.Config.Region {
			continue
		}
		if err := requestPeerPurge(ctx, peerURL+"/internal/abuse-state"); err != nil {
			return err
		}
	}
	return nil
}

func purgeAbuseStateHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h.clearAbuseState()
		return c.NoContent(http.StatusNoContent)
	}
}

func (h *HandlerContext) clearAbuseState() {
	h.Abuse.Reset()
	h.IPBans.ClearLocal()
}

// abuseKind はハンドラが返した APIError から失敗の種類を判定する。対象外なら空文字列を返す。
func abuseKind(c *echo.Context, status int) string {
	if abuse, _ := c.Get("abuse").(string); abuse != "" {
		return abuse
	}
	route := c.RouteInfo()
	if status == http.StatusUnauthorized && actionNames[route.Method+" "+route.Path] == "login" {
		return abuseLogin
	}
	return ""
}

// AbuseBanMiddleware はハンドラの結果から攻撃を検知して BAN する。
// エラーの応答は外側の ActionLogMiddleware が書くので、ステータスは APIError から読む。
func AbuseBanMiddleware(h *HandlerContext) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			err := next(c)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				return err
			}
			kind := abuseKind(c, apiErr.Status)
			ip := c.RealIP()
			if kind != "" && ip != "" && h.Abuse.Observe(kind, ip, time.Now()) {
				h.IPBans.Ban(h.DB, ip)
			}
			return err
		}
	}
}
