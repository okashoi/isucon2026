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

func getNotificationsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		session := GetSession(c)
		userID := session.UserID
		var connectedAt time.Time
		if err := h.ReadDB.GetContext(ctx, &connectedAt, `SELECT clock_timestamp()`); err != nil {
			return err
		}
		response := c.Response()
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(response)
		if err := controller.Flush(); err != nil {
			return nil
		}

		wait := h.Windows.Subscribe(session.Region)
		defer h.Windows.Unsubscribe(session.Region, wait)

		// 購読開始前に始まった窓を取りこぼさない。
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
		if err == nil {
			h.Windows.SetCurrent(existing)
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
				okActive, err := notificationUserActive(ctx, h, userID, session.Region)
				if err != nil {
					if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
						c.Logger().Error("failed to get notification user", "error", err)
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
				okActive, err := notificationUserActive(ctx, h, userID, session.Region)
				if err != nil {
					if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
						c.Logger().Error("failed to get notification user", "error", err)
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
