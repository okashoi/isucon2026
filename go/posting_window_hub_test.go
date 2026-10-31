package isureal

import (
	"testing"
	"time"
)

func TestPostingWindowHubPublishDelivers(t *testing.T) {
	hub := NewPostingWindowHub()
	ch := hub.Subscribe("europe")
	defer hub.Unsubscribe("europe", ch)
	event := postingWindowEvent{
		ID: "w1", Region: "europe", StartsAt: time.Unix(1, 0).UTC(),
		LocalDate: "2026-10-31", OnTimeDeadline: time.Unix(120, 0).UTC(),
	}
	hub.Publish(event)
	select {
	case got := <-ch:
		if got.ID != event.ID || got.Region != event.Region {
			t.Fatalf("got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	cur, ok := hub.Current("europe")
	if !ok || cur.LocalDate != "2026-10-31" || cur.OnTimeDeadline != event.OnTimeDeadline {
		t.Fatalf("current %#v ok=%v", cur, ok)
	}
}

func TestHubWindowAfterConnectIgnoresStale(t *testing.T) {
	hub := NewPostingWindowHub()
	connected := time.Unix(10, 0).UTC()
	hub.SetCurrent(postingWindowEvent{ID: "old", Region: "asia", StartsAt: connected.Add(-time.Second)})
	if _, ok := hubWindowAfterConnect(hub, "asia", connected); ok {
		t.Fatal("window started before connect must not notify")
	}
	hub.SetCurrent(postingWindowEvent{ID: "new", Region: "asia", StartsAt: connected.Add(time.Second)})
	got, ok := hubWindowAfterConnect(hub, "asia", connected)
	if !ok || got.ID != "new" {
		t.Fatalf("got %#v ok=%v", got, ok)
	}
}
