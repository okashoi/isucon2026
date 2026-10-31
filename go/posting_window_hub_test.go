package isureal

import (
	"testing"
	"time"
)

func TestPostingWindowHubPublishDelivers(t *testing.T) {
	hub := NewPostingWindowHub()
	ch := hub.Subscribe("europe")
	defer hub.Unsubscribe("europe", ch)
	event := postingWindowEvent{ID: "w1", Region: "europe", StartsAt: time.Unix(1, 0).UTC()}
	hub.Publish(event)
	select {
	case got := <-ch:
		if got.ID != event.ID || got.Region != event.Region {
			t.Fatalf("got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}
