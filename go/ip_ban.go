package isureal

import (
	"context"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

const (
	ipBanReloadInterval = 200 * time.Millisecond
	ipBanDuration       = 2 * time.Minute
	ipBanWriteTimeout   = 5 * time.Second
)

// IPBanList は ip_bans のスナップショットと、このサーバが検知した BAN を持つ。
// 後者は ip_bans の書き込みとレプリカの遅れを待たずに効かせるためのもの。
type IPBanList struct {
	reloading sync.Mutex
	snapshot  atomic.Pointer[ipBanSnapshot]

	mu    sync.RWMutex
	local map[string]time.Time
}

type ipBanSnapshot struct {
	loadedAt time.Time
	bans     map[string]time.Time
}

type ipBan struct {
	IP          string    `db:"ip"`
	BannedUntil time.Time `db:"banned_until"`
}

// Ban はこのサーバで直ちに BAN し、他のサーバへは ip_bans を通して伝える。
// 同期レプリケーションの COMMIT を待つとリクエストが遅れるので、書き込みは応答と切り離す。
func (l *IPBanList) Ban(db *sqlx.DB, ip string) {
	l.banLocally(ip, time.Now().Add(ipBanDuration))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ipBanWriteTimeout)
		defer cancel()
		if err := writeIPBan(ctx, db, ip); err != nil {
			log.Printf("ip ban write failed: ip=%s: %v", ip, err)
		}
	}()
}

func (l *IPBanList) banLocally(ip string, until time.Time) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.local == nil {
		l.local = map[string]time.Time{}
	}
	for bannedIP, bannedUntil := range l.local {
		if !now.Before(bannedUntil) {
			delete(l.local, bannedIP)
		}
	}
	l.local[ip] = until
}

func (l *IPBanList) bannedLocally(ip string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	until, ok := l.local[ip]
	return ok && time.Now().Before(until)
}

// ClearLocal はこのサーバが検知した BAN を捨てる。ip_bans の分は初期化で消える。
func (l *IPBanList) ClearLocal() {
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.local)
}

func (l *IPBanList) Banned(ctx context.Context, db *sqlx.DB, ip string) (bool, error) {
	if l.bannedLocally(ip) {
		return true, nil
	}
	snapshot := l.snapshot.Load()
	if (snapshot == nil || time.Since(snapshot.loadedAt) >= ipBanReloadInterval) && l.reloading.TryLock() {
		defer l.reloading.Unlock()
		loaded, err := loadIPBans(ctx, db)
		if err != nil {
			return false, err
		}
		l.snapshot.Store(loaded)
		snapshot = loaded
	}
	if snapshot == nil {
		return false, nil
	}
	until, ok := snapshot.bans[ip]
	return ok && time.Now().Before(until), nil
}

func loadIPBans(ctx context.Context, db *sqlx.DB) (*ipBanSnapshot, error) {
	var rows []ipBan
	if err := db.SelectContext(ctx, &rows, `SELECT ip, banned_until FROM ip_bans WHERE banned_until > clock_timestamp()`); err != nil {
		return nil, err
	}
	bans := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		bans[row.IP] = row.BannedUntil
	}
	return &ipBanSnapshot{loadedAt: time.Now(), bans: bans}, nil
}

// writeIPBan は BAN の期限を DB の時刻で決める。すでに BAN されていれば遅い方の期限を残す。
func writeIPBan(ctx context.Context, db *sqlx.DB, ip string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO ip_bans (ip, banned_until)
		VALUES ($1, clock_timestamp() + make_interval(secs => $2::double precision))
		ON CONFLICT (ip) DO UPDATE SET banned_until = GREATEST(ip_bans.banned_until, EXCLUDED.banned_until)
	`, ip, ipBanDuration.Seconds())
	return err
}

func extractClientIP(req *http.Request) string {
	if ip := req.Header.Get("X-Client-IP"); ip != "" {
		return ip
	}
	return echo.ExtractIPDirect()(req)
}

func IPBanMiddleware(h *HandlerContext) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			banned, err := h.IPBans.Banned(c.Request().Context(), h.ReadDB, c.RealIP())
			if err != nil {
				return err
			}
			if banned {
				return NewAPIError(http.StatusForbidden, "ip_banned")
			}
			return next(c)
		}
	}
}
