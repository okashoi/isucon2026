package isureal

import (
	"testing"
	"time"
)

func TestImageVisiblePin(t *testing.T) {
	clearImageMetaCache()
	old := time.Now().Add(-time.Hour)
	load := func(id string) bool {
		v, ok := imageVisibleCache.Load(id)
		return ok && imageVisibleValid(v.(imageVisible))
	}

	storeImageVisible("a", imageVisible{fetchedAt: old, owner: "u1"}, true, imageVisibleEpoch())
	storeImageVisible("b", imageVisible{fetchedAt: old, owner: "u2"}, false, imageVisibleEpoch())
	if !load("a") || load("b") {
		t.Fatal("pinned entry must outlive TTL, unpinned must not")
	}
	purgeImageVisibleOwner("u2")
	if !load("a") {
		t.Fatal("other owner's purge must not drop the entry")
	}
	purgeImageVisibleOwner("u1")
	if load("a") {
		t.Fatal("owner purge must drop the entry")
	}
	e := imageVisibleEpoch()
	purgeImageVisibleOwner("u1") // purge between the read and the store
	storeImageVisible("a", imageVisible{fetchedAt: old, owner: "u1"}, true, e)
	if load("a") {
		t.Fatal("a read from before the purge must not be pinned")
	}
	storeImageVisible("a", imageVisible{fetchedAt: old, owner: "u1"}, true, imageVisibleEpoch())
	purgeImageVisibleAll()
	if load("a") {
		t.Fatal("purge all must drop the entry")
	}
}
