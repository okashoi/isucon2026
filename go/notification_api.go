package isureal

import (
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

type notificationPoll struct {
	Region          string     `db:"region"`
	PostingWindowID *string    `db:"posting_window_id"`
	StartsAt        *time.Time `db:"starts_at"`
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
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			var poll notificationPoll
			err := h.ReadDB.GetContext(ctx, &poll, `
    SELECT u.region, w.id AS posting_window_id, w.starts_at
    FROM users u
    LEFT JOIN posting_windows w ON w.region = u.region AND w.ended_at IS NULL AND w.starts_at > $2
    WHERE u.id = $1 AND u.status = 'active'
   `, userID, connectedAt)
			if err != nil {
				if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
					c.Logger().Error("failed to get notification", "error", err)
				}
				return nil
			}
			if poll.Region != session.Region {
				return nil
			}
			if poll.PostingWindowID != nil {
				data, err := json.Marshal(getNotificationsResponse{
					ID:              notificationID(*poll.PostingWindowID, userID),
					PostingWindowID: *poll.PostingWindowID,
					Region:          poll.Region,
					CreatedAt:       *poll.StartsAt,
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
