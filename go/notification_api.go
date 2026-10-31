package isureal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

type getNotificationsResponse struct {
	ID              string    `json:"id"`
	PostingWindowID string    `json:"postingWindowId"`
	Region          string    `json:"region"`
	CreatedAt       time.Time `json:"createdAt"`
}

// notificationID は通知を行として持たないので、投稿期間と利用者の組から決定的に作る。
func notificationID(postingWindowID, userID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(postingWindowID+"/"+userID)).String()
}

func writeNotificationEvent(c *echo.Context, userID string, event postingWindowEvent) error {
	data, err := json.Marshal(getNotificationsResponse{
		ID:              notificationID(event.ID, userID),
		PostingWindowID: event.ID,
		Region:          event.Region,
		CreatedAt:       event.StartsAt,
	})
	if err != nil {
		return err
	}
	response := c.Response()
	if _, err := fmt.Fprintf(response, "data: %s\n\n", data); err != nil {
		return err
	}
	return http.NewResponseController(response).Flush()
}

func notificationUserActive(ctx context.Context, h *HandlerContext, userID, region string) (bool, error) {
	row := struct {
		Region string `db:"region"`
		Status string `db:"status"`
	}{}
	if err := h.ReadDB.GetContext(ctx, &row, `SELECT region, status FROM users WHERE id = $1`, userID); err != nil {
		return false, err
	}
	return row.Status == "active" && row.Region == region, nil
}

func hubWindowAfterConnect(hub *PostingWindowHub, region string, connectedAt time.Time) (postingWindowEvent, bool) {
	cur, ok := hub.Current(region)
	if !ok || !cur.StartsAt.After(connectedAt) {
		return postingWindowEvent{}, false
	}
	return cur, true
}

func loadOpenWindowAfter(ctx context.Context, h *HandlerContext, userID, region string, connectedAt time.Time) (postingWindowEvent, error) {
	if cur, ok := hubWindowAfterConnect(h.Windows, region, connectedAt); ok {
		return cur, nil
	}
	var existing postingWindowEvent
	err := h.ReadDB.GetContext(ctx, &existing, `
  SELECT w.id, w.region, w.starts_at,
         to_char(w.local_date, 'YYYY-MM-DD') AS local_date,
         w.on_time_deadline
  FROM users u
  JOIN posting_windows w ON w.region = u.region AND w.ended_at IS NULL AND w.starts_at > $2
  WHERE u.id = $1 AND u.status = 'active'
  LIMIT 1
 `, userID, connectedAt)
	if err != nil {
		return postingWindowEvent{}, err
	}
	h.Windows.SetCurrent(existing)
	return existing, nil
}

func getNotificationsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		session := GetSession(c)
		userID := session.UserID
		// starts_at と比較するので、接続時刻は DB 時計に揃える。
		var connectedAt time.Time
		if err := h.ReadDB.GetContext(ctx, &connectedAt, `SELECT clock_timestamp()`); err != nil {
			return err
		}

		response := c.Response()
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Cache-Control", "no-cache")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(response)
		if err := controller.Flush(); err != nil {
			return nil
		}

		wait := h.Windows.Subscribe(session.Region)
		defer h.Windows.Unsubscribe(session.Region, wait)

		existing, err := loadOpenWindowAfter(ctx, h, userID, session.Region, connectedAt)
		if err == nil {
			okActive, activeErr := notificationUserActive(ctx, h, userID, session.Region)
			if activeErr != nil {
				if !errors.Is(activeErr, sql.ErrNoRows) && ctx.Err() == nil {
					c.Logger().Error("failed to get notification user", "error", activeErr)
				}
				return nil
			}
			if !okActive {
				return nil
			}
			if writeErr := writeNotificationEvent(c, userID, existing); writeErr != nil {
				return nil
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			c.Logger().Error("failed to get notification", "error", err)
			return nil
		}

		statusTick := time.NewTicker(5 * time.Second)
		defer statusTick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case event, ok := <-wait:
				if !ok {
					return nil
				}
				if !event.StartsAt.After(connectedAt) || event.Region != session.Region {
					continue
				}
				okActive, activeErr := notificationUserActive(ctx, h, userID, session.Region)
				if activeErr != nil {
					if !errors.Is(activeErr, sql.ErrNoRows) && ctx.Err() == nil {
						c.Logger().Error("failed to get notification user", "error", activeErr)
					}
					return nil
				}
				if !okActive {
					return nil
				}
				if writeErr := writeNotificationEvent(c, userID, event); writeErr != nil {
					return nil
				}
				return nil
			case <-statusTick.C:
				okActive, activeErr := notificationUserActive(ctx, h, userID, session.Region)
				if activeErr != nil {
					if !errors.Is(activeErr, sql.ErrNoRows) && ctx.Err() == nil {
						c.Logger().Error("failed to get notification user", "error", activeErr)
					}
					return nil
				}
				if !okActive {
					return nil
				}
			}
		}
	}
}
