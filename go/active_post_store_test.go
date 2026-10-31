package isureal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestActivePostStoreFriendsFeedHiddenAndOrdered(t *testing.T) {
	store := NewActivePostStore()
	viewer := friendViewer("self", []string{"self", "ann", "bob", "cam", "dee", "eve", "kay"}, []string{"kay"})
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	posts := []PostWithAuthor{
		samplePost("a1", "ann", "asia", "w-asia", "friends", base.Add(10*time.Second)),
		samplePost("b1", "bob", "europe", "w-europe", "public", base.Add(30*time.Second)),
		samplePost("c1", "cam", "america", "w-america", "friends_of_friends", base.Add(20*time.Second)),
		samplePost("d1", "dee", "asia", "w-asia", "friends", base.Add(20*time.Second)),
		samplePost("e1", "eve", "europe", "w-europe", "secret", base.Add(40*time.Second)),
		samplePost("s1", "self", "asia", "w-asia", "public", base.Add(50*time.Second)),
		samplePost("k1", "kay", "america", "w-america", "public", base.Add(45*time.Second)),
		samplePost("x1", "stranger", "asia", "w-asia", "public", base.Add(35*time.Second)),
	}
	if items, hit := store.FriendsFeed(viewer, nil, "", 10); hit || items != nil {
		t.Fatal("empty store before the first post is not a friends-feed authority")
	}
	for _, post := range posts {
		store.Add(post)
	}
	items, hit := store.FriendsFeed(viewer, nil, "", 10)
	if !hit {
		t.Fatal("store is not live after Add")
	}
	got := postIDs(items)
	want := []string{"b1", "d1", "c1", "a1"}
	if !sameIDs(got, want) {
		t.Fatalf("feed ids %v, want %v", got, want)
	}

	cursor := base.Add(20 * time.Second)
	items, hit = store.FriendsFeed(viewer, &cursor, "d1", 10)
	if !hit || !sameIDs(postIDs(items), []string{"c1", "a1"}) {
		t.Fatalf("seek ids %v, hit %v", postIDs(items), hit)
	}
	items, hit = store.FriendsFeed(viewer, nil, "", 2)
	if !hit || !sameIDs(postIDs(items), []string{"b1", "d1"}) {
		t.Fatalf("limit ids %v", postIDs(items))
	}
}

func TestActivePostStorePostedTodayRemoveAndClear(t *testing.T) {
	store := NewActivePostStore()
	post := samplePost("a1", "ann", "asia", "w-asia", "friends", time.Unix(10, 0).UTC())
	if store.HasPostedToday("ann") || store.Live() {
		t.Fatal("empty store reports a post or live")
	}
	store.Add(post)
	if !store.HasPostedToday("ann") || store.HasPostedToday("bob") || !store.Live() {
		t.Fatal("posted-today does not follow the author's active post")
	}
	store.Remove("missing")
	store.Remove("a1")
	if store.HasPostedToday("ann") {
		t.Fatal("removed post still counts as posted today")
	}
	store.Add(post)
	store.Clear()
	if store.Live() || store.HasPostedToday("ann") {
		t.Fatal("clear left the store live or still holding the post")
	}
	items, hit := store.FriendsFeed(friendViewer("self", []string{"ann"}, nil), nil, "", 10)
	if hit || items != nil {
		t.Fatal("cleared store is still a friends-feed authority")
	}
}

func TestActivePostStoreMarkLiveWithoutPosts(t *testing.T) {
	store := NewActivePostStore()
	store.MarkLive()
	items, hit := store.FriendsFeed(friendViewer("self", nil, nil), nil, "", 10)
	if !hit || len(items) != 0 {
		t.Fatalf("live empty store hit %v items %d", hit, len(items))
	}
}

func TestActivePostStoreDropClosedRegionWindow(t *testing.T) {
	store := NewActivePostStore()
	oldAsia := samplePost("old", "ann", "asia", "w-old", "public", time.Unix(10, 0).UTC())
	newAsia := samplePost("new", "bob", "asia", "w-new", "public", time.Unix(20, 0).UTC())
	europe := samplePost("eu", "cam", "europe", "w-europe", "public", time.Unix(30, 0).UTC())
	store.Add(oldAsia)
	store.Add(newAsia)
	store.Add(europe)
	store.DropOtherWindows("asia", "w-new")
	viewer := friendViewer("self", []string{"ann", "bob", "cam"}, nil)
	items, hit := store.FriendsFeed(viewer, nil, "", 10)
	if !hit || !sameIDs(postIDs(items), []string{"eu", "new"}) {
		t.Fatalf("after window close ids %v hit %v", postIDs(items), hit)
	}
	if store.HasPostedToday("ann") || !store.HasPostedToday("bob") {
		t.Fatal("closed-window author is still posted today, or the open window author is not")
	}
}

func TestActivePostStoreReplaceKeepsOnePost(t *testing.T) {
	store := NewActivePostStore()
	post := samplePost("a1", "ann", "asia", "w-asia", "friends", time.Unix(10, 0).UTC())
	store.Add(post)
	post.Caption = "edited"
	post.Visibility = "public"
	store.Add(post)
	items, hit := store.FriendsFeed(friendViewer("self", []string{"ann"}, nil), nil, "", 10)
	if !hit || len(items) != 1 || items[0].Caption != "edited" || items[0].Visibility != "public" {
		t.Fatalf("replaced post %+v hit %v", items, hit)
	}
}

func TestActivePostStoreTryCreateSlotAndGet(t *testing.T) {
	store := NewActivePostStore()
	starts := time.Unix(10, 0).UTC()
	post := samplePost("a1", "ann", "asia", "w-asia", "friends", starts.Add(time.Second))
	if !store.TryCreate(post, "2026-10-31", starts.Add(time.Second), starts) {
		t.Fatal("first create should succeed")
	}
	if _, ok := store.Get("a1"); !ok {
		t.Fatal("created post missing")
	}
	dup := samplePost("a2", "ann", "asia", "w-asia", "friends", starts.Add(2*time.Second))
	if store.TryCreate(dup, "2026-10-31", starts.Add(2*time.Second), starts) {
		t.Fatal("second active post must be rejected")
	}
	store.Remove("a1")
	if !store.TryRecordDelete("ann", "2026-10-31") || !store.TryRecordDelete("ann", "2026-10-31") {
		t.Fatal("two deletes should be allowed")
	}
	if store.TryRecordDelete("ann", "2026-10-31") {
		t.Fatal("third delete must be rejected")
	}
	if store.TryCreate(dup, "2026-10-31", starts.Add(3*time.Second), starts) {
		t.Fatal("create after two deletes must be rejected")
	}
}

func TestActivePostWireKeepsSigningMaterial(t *testing.T) {
	imageID := "profile-1"
	post := samplePost("a1", "ann", "asia", "w-asia", "public", time.Unix(10, 0).UTC())
	post.Author.ProfileImageID = &imageID
	if err := post.Author.setImageKey(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(newActivePostWire(post))
	if err != nil {
		t.Fatal(err)
	}
	var wire activePostWire
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	decoded, err := wire.decode()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Author.SecretKeyHash != post.Author.SecretKeyHash {
		t.Fatal("secret key hash dropped on the peer payload")
	}
	if decoded.Author.ProfileImageID == nil || *decoded.Author.ProfileImageID != imageID {
		t.Fatal("profile image id dropped on the peer payload")
	}
	if !bytes.Equal(decoded.Author.ImageKey, post.Author.ImageKey) {
		t.Fatal("image signing key does not match after decode")
	}
	if !decoded.PostedAt.Equal(post.PostedAt) || decoded.Caption != post.Caption {
		t.Fatal("post fields changed on the peer payload")
	}
}

func TestActivePostStoreConcurrentAddRemoveFeed(t *testing.T) {
	store := NewActivePostStore()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "p" + time.Unix(int64(i), 0).UTC().Format("150405") + string(rune('a'+i))
			author := "a" + string(rune('a'+i))
			post := samplePost(id, author, "asia", "w-asia", "public", time.Unix(int64(i+1), 0).UTC())
			store.Add(post)
			store.HasPostedToday(author)
			store.FriendsFeed(friendViewer("self", []string{author}, nil), nil, "", 5)
			if i%2 == 0 {
				store.Remove(id)
			}
		}(i)
	}
	wg.Wait()
	if !store.Live() {
		t.Fatal("concurrent adds left the store offline")
	}
}

func friendViewer(id string, friends, blocked []string) viewerContext {
	viewer := viewerContext{
		User:    User{ID: id},
		Friends: map[string]bool{},
		Blocked: map[string]bool{},
	}
	for _, friend := range friends {
		viewer.Friends[friend] = true
	}
	for _, block := range blocked {
		viewer.Blocked[block] = true
	}
	return viewer
}

func samplePost(id, authorID, region, windowID, visibility string, postedAt time.Time) PostWithAuthor {
	sum := sha256.Sum256([]byte(authorID))
	return PostWithAuthor{
		Post: Post{
			ID:              id,
			AuthorID:        authorID,
			PostingWindowID: windowID,
			ImageID:         "img-" + id,
			Caption:         "caption-" + id,
			Visibility:      visibility,
			PostedAt:        postedAt,
		},
		Author: User{
			ID:            authorID,
			Username:      authorID,
			SecretKeyHash: hex.EncodeToString(sum[:]),
			DisplayName:   authorID,
			Region:        region,
			AccountType:   "general",
			Status:        "active",
		},
	}
}

func postIDs(posts []PostWithAuthor) []string {
	ids := make([]string, len(posts))
	for i, post := range posts {
		ids[i] = post.ID
	}
	return ids
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
