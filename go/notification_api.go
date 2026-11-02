package isureal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getNotificationsResponse struct {
	ID              string    `json:"id"`
	PostingWindowID string    `json:"postingWindowId"`
	Region          string    `json:"region"`
	CreatedAt       time.Time `json:"createdAt"`
}

type notificationWindow struct {
	ID       string    `db:"id"`
	Region   string    `db:"region"`
	StartsAt time.Time `db:"starts_at"`
}

type notificationSnapshot struct {
	loadedAt time.Time
	windows  []notificationWindow
}

const notificationReloadInterval = 100 * time.Millisecond

var (
	notificationSnap      atomic.Pointer[notificationSnapshot]
	notificationReloading sync.Mutex
)

// clearNotificationSnapshot は /initialize で進行中の投稿期間のスナップショットを捨てる。
func clearNotificationSnapshot() { notificationSnap.Store(nil) }

// openWindows は進行中の投稿期間を返す。古ければ 1 人だけが DB から読み直す。
func openWindows(ctx context.Context, db *sqlx.DB) ([]notificationWindow, error) {
	snap := notificationSnap.Load()
	if (snap == nil || time.Since(snap.loadedAt) >= notificationReloadInterval) && notificationReloading.TryLock() {
		defer notificationReloading.Unlock()
		var rows []notificationWindow
		if err := db.SelectContext(ctx, &rows, `SELECT id, region, starts_at FROM posting_windows WHERE ended_at IS NULL`); err != nil {
			return nil, err
		}
		snap = &notificationSnapshot{loadedAt: time.Now(), windows: rows}
		notificationSnap.Store(snap)
	}
	if snap == nil {
		return nil, nil
	}
	return snap.windows, nil
}

// notificationID は通知を行として持たないので、投稿期間と利用者の組から決定的に作る。
func notificationID(postingWindowID, userID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(postingWindowID+"/"+userID)).String()
}

func getNotificationsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		session := GetSession(c)
		userID := session.UserID
		var conn struct {
			Now    time.Time `db:"now"`
			Region *string   `db:"region"`
		}
		if err := h.ReadDB.GetContext(ctx, &conn, `SELECT clock_timestamp() AS now, (SELECT region FROM users WHERE id = $1 AND status = 'active') AS region`, userID); err != nil {
			return err
		}
		connectedAt := conn.Now
		response := c.Response()
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(response)
		if err := controller.Flush(); err != nil {
			return nil
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		if conn.Region == nil || *conn.Region != session.Region {
			return nil
		}
		for {
			windows, err := openWindows(ctx, h.ReadDB)
			if err != nil {
				if ctx.Err() == nil {
					c.Logger().Error("failed to get notification", "error", err)
				}
				return nil
			}
			for _, w := range windows {
				if w.Region != session.Region || !w.StartsAt.After(connectedAt) {
					continue
				}
				// 送信直前に利用者の状態を確認する。
				var region string
				if err := h.ReadDB.GetContext(ctx, &region, `SELECT region FROM users WHERE id = $1 AND status = 'active'`, userID); err != nil {
					if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
						c.Logger().Error("failed to get notification", "error", err)
					}
					return nil
				}
				if region != session.Region {
					return nil
				}
				data, err := json.Marshal(getNotificationsResponse{
					ID:              notificationID(w.ID, userID),
					PostingWindowID: w.ID,
					Region:          region,
					CreatedAt:       w.StartsAt,
				})
				if err != nil {
					c.Logger().Error("failed to encode notification", "error", err)
					return nil
				}
				if _, err := fmt.Fprintf(response, "data: %s\n\n", data); err != nil {
					return nil
				}
				_ = controller.Flush()
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
}
