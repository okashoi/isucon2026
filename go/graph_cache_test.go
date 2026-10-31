package isureal

import "testing"

func TestGraphCacheHit(t *testing.T) {
	c := NewGraphCache()
	c.Put(c.Generation(), "u1", map[string]bool{"friend": true}, map[string]bool{"blocked": true})
	friends, blocked, ok := c.Get("u1")
	if !ok {
		t.Fatal("cached entry is missing")
	}
	if !friends["friend"] || !blocked["blocked"] {
		t.Fatal("cached friends or blocks do not match")
	}
	friends["extra"] = true
	blocked["extra"] = true
	friends, blocked, ok = c.Get("u1")
	if !ok || friends["extra"] || blocked["extra"] {
		t.Fatal("caller mutation changed the cache")
	}
	if _, _, ok := c.Get("missing"); ok {
		t.Fatal("missing user is cached")
	}
}

func TestGraphCachePurgeUser(t *testing.T) {
	c := NewGraphCache()
	generation := c.Generation()
	c.Put(generation, "u1", map[string]bool{"a": true}, nil)
	c.Put(generation, "u2", map[string]bool{"b": true}, nil)
	c.PurgeUser("u1")
	if _, _, ok := c.Get("u1"); ok {
		t.Fatal("purged user is still cached")
	}
	if _, _, ok := c.Get("u2"); !ok {
		t.Fatal("other user was purged")
	}
}

func TestGraphCachePutSkipsAfterPurge(t *testing.T) {
	c := NewGraphCache()
	generation := c.Generation()
	c.PurgeUser("u1")
	c.Put(generation, "u1", map[string]bool{"a": true}, map[string]bool{"b": true})
	if _, _, ok := c.Get("u1"); ok {
		t.Fatal("entry put after a later purge is cached")
	}
}

func TestGraphCachePutKeepsOtherUserAfterPurge(t *testing.T) {
	c := NewGraphCache()
	generation := c.Generation()
	c.PurgeUser("u2")
	c.Put(generation, "u1", map[string]bool{"a": true}, nil)
	if _, _, ok := c.Get("u1"); !ok {
		t.Fatal("other user's purge dropped this entry")
	}
}

func TestGraphCachePurgeAll(t *testing.T) {
	c := NewGraphCache()
	generation := c.Generation()
	c.Put(generation, "u1", map[string]bool{"a": true}, nil)
	c.PurgeAll()
	if _, _, ok := c.Get("u1"); ok {
		t.Fatal("entry remains after purge all")
	}
	c.Put(generation, "u1", map[string]bool{"a": true}, nil)
	if _, _, ok := c.Get("u1"); ok {
		t.Fatal("entry put after a later purge all is cached")
	}
}

func TestGraphCacheInitializeClear(t *testing.T) {
	c := NewGraphCache()
	c.Put(c.Generation(), "u1", map[string]bool{"a": true}, map[string]bool{"b": true})
	c.Clear()
	if _, _, ok := c.Get("u1"); ok {
		t.Fatal("entry remains after initialize clear")
	}
}
