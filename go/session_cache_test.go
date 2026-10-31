package isureal

import "testing"

func TestSessionCacheReplaceDropsOldToken(t *testing.T) {
	c := NewSessionCache()
	c.Add(c.Generation(), Session{UserID: "u1", TokenHash: "old"})
	c.Replace(c.Generation(), Session{UserID: "u1", TokenHash: "new"})
	if _, ok := c.Get("old"); ok {
		t.Fatal("old token is still cached")
	}
	if _, ok := c.Get("new"); !ok {
		t.Fatal("new token is not cached")
	}
}

func TestSessionCacheReplaceIgnoresOtherUsersPurge(t *testing.T) {
	c := NewSessionCache()
	generation := c.Generation()
	c.PurgeUser("u2")
	c.Replace(generation, Session{UserID: "u1", TokenHash: "new"})
	if _, ok := c.Get("new"); !ok {
		t.Fatal("new token is not cached")
	}
}

func TestSessionCacheReplaceSkipsAfterSameUserPurge(t *testing.T) {
	c := NewSessionCache()
	generation := c.Generation()
	c.PurgeUser("u1")
	c.Replace(generation, Session{UserID: "u1", TokenHash: "new"})
	if _, ok := c.Get("new"); ok {
		t.Fatal("token replaced after a later purge is cached")
	}
}

func TestSessionCacheReplaceSkipsAfterPurgeAll(t *testing.T) {
	c := NewSessionCache()
	generation := c.Generation()
	c.PurgeAll()
	c.Replace(generation, Session{UserID: "u1", TokenHash: "new"})
	if _, ok := c.Get("new"); ok {
		t.Fatal("token replaced after a later purge is cached")
	}
}

// DB から古いセッションを読んでいる途中で置き換わったら、その読み取り結果は保存しない。
func TestSessionCacheAddSkipsAfterReplace(t *testing.T) {
	c := NewSessionCache()
	generation := c.Generation()
	c.Replace(c.Generation(), Session{UserID: "u1", TokenHash: "new"})
	c.Add(generation, Session{UserID: "u1", TokenHash: "old"})
	if _, ok := c.Get("old"); ok {
		t.Fatal("old token read before the replace is cached")
	}
	if _, ok := c.Get("new"); !ok {
		t.Fatal("new token is not cached")
	}
}
