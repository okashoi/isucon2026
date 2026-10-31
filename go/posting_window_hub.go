package isureal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

type postingWindowEvent struct {
	ID       string    `json:"id" db:"id"`
	Region   string    `json:"region" db:"region"`
	StartsAt time.Time `json:"startsAt" db:"starts_at"`
}

// PostingWindowHub は投稿期間開始を同リージョンの SSE 待ちへ届ける。
type PostingWindowHub struct {
	mu      sync.Mutex
	waiters map[string]map[chan postingWindowEvent]struct{}
}

func NewPostingWindowHub() *PostingWindowHub {
	return &PostingWindowHub{waiters: map[string]map[chan postingWindowEvent]struct{}{}}
}

func (h *PostingWindowHub) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for region, waiters := range h.waiters {
		for ch := range waiters {
			close(ch)
		}
		delete(h.waiters, region)
	}
}

func (h *PostingWindowHub) Subscribe(region string) chan postingWindowEvent {
	ch := make(chan postingWindowEvent, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.waiters[region] == nil {
		h.waiters[region] = map[chan postingWindowEvent]struct{}{}
	}
	h.waiters[region][ch] = struct{}{}
	return ch
}

func (h *PostingWindowHub) Unsubscribe(region string, ch chan postingWindowEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	waiters := h.waiters[region]
	if waiters == nil {
		return
	}
	if _, ok := waiters[ch]; ok {
		delete(waiters, ch)
		close(ch)
	}
	if len(waiters) == 0 {
		delete(h.waiters, region)
	}
}

func (h *PostingWindowHub) Publish(event postingWindowEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.waiters[event.Region] {
		select {
		case ch <- event:
		default:
		}
	}
}

func deliverPostingWindow(h *HandlerContext, event postingWindowEvent) {
	h.Windows.Publish(event)
	h.Active.DropOtherWindows(event.Region, event.ID)
}

func notifyPostingWindowPeers(ctx context.Context, h *HandlerContext, event postingWindowEvent) {
	deliverPostingWindow(h, event)
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	for region, peerURL := range h.Config.PeerURLs {
		if region == h.Config.Region {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL+"/internal/posting-window-notify", bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
}

func notifyPostingWindowHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var event postingWindowEvent
		if err := c.Bind(&event); err != nil {
			return err
		}
		if !validRegion(event.Region) || event.ID == "" {
			return NewAPIError(http.StatusBadRequest, "invalid_posting_window")
		}
		deliverPostingWindow(h, event)
		return c.NoContent(http.StatusNoContent)
	}
}
