package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIDMapRoundTrip(t *testing.T) {
	m := newIDMap(regexp.MustCompile(defaultIDRegex))
	oldTok, newTok := "01M5Q3GC9K16XVGKYPTD7ETK5D", "01M5Q9ZZZZZZZZZZZZZZZZZZZZ"
	m.learn(`{"id":"`+oldTok+`","at":1761300000000}`, `{"id":"`+newTok+`","at":1761300999999}`)

	if got := m.rewrite("/api/rides/" + oldTok + "/status"); got != "/api/rides/"+newTok+"/status" {
		t.Fatalf("rewrite: %s", got)
	}
	want := m.canon(`{"id":"`+oldTok+`","at":1761300000000}`, false, &numSet{m: map[string]bool{}})
	got := m.canon(`{"id":"`+newTok+`","at":1761300999999}`, true, &numSet{m: map[string]bool{}})
	if want != got {
		t.Fatalf("canon で同じにならない: %s / %s", want, got)
	}
	if m.canon(`{"id":"01UNKNOWNUNKNOWNUNKNOWN000"}`, true, &numSet{m: map[string]bool{}}) != `{"id":"<ID>"}` {
		t.Fatal("対応のない ID は <ID> に潰す")
	}
}

func TestNumericIDs(t *testing.T) {
	table := map[string]string{"12": "1012", "3": "1003"}
	if got := rewriteNums("/api/items/12/comments?user_id=3&limit=3", table, true); got != "/api/items/1012/comments?user_id=1003&limit=3" {
		t.Fatalf("URI の数値 ID: %s", got)
	}
	if got := rewriteNums(`{"item_id":12,"count":12}`, table, false); got != `{"item_id":1012,"count":12}` {
		t.Fatalf("本文の数値 ID は id 系のキーだけ: %s", got)
	}
	paths := idPaths(`{"items":[{"id":5,"owner_id":3},{"id":7}],"total":7}`)
	if v, ok := valueAt(map[string]any{"items": []any{map[string]any{"id": json.Number("1005")}}}, paths["5"][0]); !ok || v != "1005" {
		t.Fatalf("同じ位置の値を引けない: %v %v", paths, v)
	}
	if _, ok := paths["7"]; !ok {
		t.Fatal("一覧の2件目の id も拾う")
	}
}

func TestDependencies(t *testing.T) {
	tok := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	recs := []Record{
		{TMs: 0, DurMs: 1000, URI: "/api/initialize"},
		{TMs: 1100, DurMs: 10, URI: "/api/register", RespHeader: map[string]string{"Set-Cookie": "s=" + tok}},
		{TMs: 1200, DurMs: 10, URI: "/api/me", ReqHeader: map[string]string{"Cookie": "s=" + tok}},
		{TMs: 1205, DurMs: 10, URI: "/api/poll", ReqHeader: map[string]string{"Cookie": "s=" + tok}},
	}
	deps := makePlan(recs, regexp.MustCompile(`^/api/initialize`), regexp.MustCompile(defaultIDRegex)).deps
	if len(deps[1]) != 1 || deps[1][0] != 0 {
		t.Fatalf("initialize の後に送る: %v", deps[1])
	}
	if !slices.Contains(deps[2], 1) {
		t.Fatalf("Cookie を発行したリクエストを待つ: %v", deps[2])
	}
	if slices.Contains(deps[3], 2) {
		t.Fatalf("録画で並行していた同じ Cookie のリクエストは待たない: %v", deps[3])
	}
}

// fakeApp は ISUCON14 と違う性質を持つ問題の代わり。連番の数値 ID、UUID、Authorization ヘッダ、
// バイナリの画像、RFC3339 の時刻を使う。idBase で連番の始まりを変え、再生時に ID がずれる状況を作る。
type fakeApp struct {
	mu       sync.Mutex
	idBase   int
	nextUser int
	nextItem int
	sessions map[string]int
	items    map[int]map[string]any
	icons    map[int][]byte
	bug      bool
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *fakeApp) user(r *http.Request) (int, bool) {
	if c, err := r.Cookie("session"); err == nil {
		if u, ok := a.sessions[c.Value]; ok {
			return u, true
		}
	}
	u, ok := a.sessions[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	return u, ok
}

func (a *fakeApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch r.URL.Path {
	case "/initialize":
		a.nextUser, a.nextItem = a.idBase+1, a.idBase+1
		a.sessions, a.items, a.icons = map[string]int{}, map[int]map[string]any{}, map[int][]byte{}
		reply(200, map[string]string{"language": "go"})
		return
	case "/api/register":
		var in struct{ Name string }
		json.NewDecoder(r.Body).Decode(&in)
		id := a.nextUser
		a.nextUser++
		cookie, token := randHex(16), randHex(24)
		a.sessions[cookie], a.sessions[token] = id, id
		http.SetCookie(w, &http.Cookie{Name: "session", Value: cookie})
		reply(201, map[string]any{"id": id, "name": in.Name, "token": token})
		return
	}
	uid, ok := a.user(r)
	if !ok {
		reply(401, map[string]string{"message": "unauthorized"})
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/items":
		var in struct{ Title string }
		json.NewDecoder(r.Body).Decode(&in)
		id := a.nextItem
		a.nextItem++
		u := fmt.Sprintf("%s-%s-%s-%s-%s", randHex(4), randHex(2), randHex(2), randHex(2), randHex(6))
		a.items[id] = map[string]any{"id": id, "owner_id": uid, "title": in.Title, "uuid": u, "created_at": time.Now().Format(time.RFC3339Nano)}
		reply(201, a.items[id])
	case r.Method == http.MethodGet && r.URL.Path == "/api/items":
		owner, _ := strconv.Atoi(r.URL.Query().Get("owner_id"))
		var list []map[string]any
		for id := a.idBase + 1; id < a.nextItem; id++ {
			if it := a.items[id]; it["owner_id"] == owner {
				list = append(list, it)
			}
		}
		reply(200, map[string]any{"items": list})
	case len(parts) >= 3 && parts[1] == "items":
		id, _ := strconv.Atoi(parts[2])
		it, found := a.items[id]
		if !found || it["owner_id"] != uid {
			reply(404, map[string]string{"message": "not found"})
			return
		}
		switch {
		case len(parts) == 4 && r.Method == http.MethodPut:
			a.icons[id], _ = io.ReadAll(r.Body)
			w.WriteHeader(204)
		case len(parts) == 4:
			w.Header().Set("Content-Type", "image/png")
			w.Write(a.icons[id])
		default:
			if a.bug {
				it = map[string]any{"id": it["id"], "owner_id": it["owner_id"], "title": strings.ToUpper(it["title"].(string)), "uuid": it["uuid"], "created_at": it["created_at"]}
			}
			reply(200, it)
		}
	case len(parts) == 3 && parts[1] == "by-uuid":
		for _, it := range a.items {
			if it["uuid"] == parts[2] {
				reply(200, it)
				return
			}
		}
		reply(404, map[string]string{"message": "not found"})
	default:
		reply(404, map[string]string{"message": "no route"})
	}
}

type cookieJar struct {
	mu sync.Mutex
	c  []*http.Cookie
}

func (j *cookieJar) SetCookies(_ *url.URL, cs []*http.Cookie) { j.mu.Lock(); j.c = cs; j.mu.Unlock() }
func (j *cookieJar) Cookies(*url.URL) []*http.Cookie          { j.mu.Lock(); defer j.mu.Unlock(); return j.c }

// benchClient は公式ベンチの代わりに、利用者ごとの流れを並行に流す。
func benchClient(t *testing.T, base string, users int) {
	do := func(c *http.Client, method, path, bearer string, body []byte) map[string]any {
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var v map[string]any
		json.NewDecoder(res.Body).Decode(&v)
		return v
	}
	do(http.DefaultClient, "POST", "/initialize", "", nil)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe, 0x00, 0x80}
	var wg sync.WaitGroup
	for i := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i*7%5) * time.Millisecond)
			c := &http.Client{Jar: &cookieJar{}}
			u := do(c, "POST", "/api/register", "", []byte(fmt.Sprintf(`{"name":"user%d"}`, i)))
			token, uid := u["token"].(string), int(u["id"].(float64))
			for k := range 2 {
				it := do(c, "POST", "/api/items", "", []byte(fmt.Sprintf(`{"title":"item-%d-%d"}`, i, k)))
				id := int(it["id"].(float64))
				do(c, "GET", fmt.Sprintf("/api/items/%d", id), "", nil)
				do(c, "PUT", fmt.Sprintf("/api/items/%d/icon", id), token, png)
				req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/items/%d/icon", base, id), nil)
				req.Header.Set("Authorization", "Bearer "+token)
				if res, err := c.Do(req); err == nil {
					res.Body.Close()
				}
				do(c, "GET", "/api/by-uuid/"+it["uuid"].(string), "", nil)
			}
			do(c, "GET", fmt.Sprintf("/api/items?owner_id=%d", uid), "", nil)
		}()
	}
	wg.Wait()
}

// recordFake は fakeApp の前に録画用の中継を挟んでベンチを流し、JSONL を経由した録画を返す。
func recordFake(t *testing.T, users int) []Record {
	app := httptest.NewServer(&fakeApp{})
	defer app.Close()
	u, _ := url.Parse(app.URL)
	ch := make(chan Record, 10000)
	rec := httptest.NewServer(newRecorder(u, regexp.MustCompile(`^/api/internal/`), ch))
	benchClient(t, rec.URL, users)
	rec.Close()
	close(ch)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for r := range ch {
		enc.Encode(r)
	}
	recs, err := decodeRecords(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func replayFake(t *testing.T, recs []Record, app *fakeApp) Result {
	srv := httptest.NewServer(app)
	defer srv.Close()
	res, samples := runReplay(replayOpts{
		target: srv.URL, speed: 4,
		barrier: regexp.MustCompile(`^(/api)?/initialize`), id: regexp.MustCompile(defaultIDRegex),
	}, recs)
	for k, s := range samples {
		t.Logf("%s:\n%s", k, strings.Join(s, "\n"))
	}
	return res
}

func TestReplayOtherProblemShape(t *testing.T) {
	recs := recordFake(t, 8)
	if len(recs) < 50 {
		t.Fatalf("録画が少ない: %d", len(recs))
	}

	res := replayFake(t, recs, &fakeApp{idBase: 1000})
	for k, s := range res.Endpoints {
		if s.StatusDiff+s.BodyDiff+s.Errs > 0 {
			t.Errorf("ID がずれても正常版なら食い違わないはず %s: %+v", k, *s)
		}
	}
	if res.IDsMapped == 0 {
		t.Error("ID の対応を1つも覚えていない")
	}

	bad := replayFake(t, recs, &fakeApp{idBase: 1000, bug: true})
	if compare([]Result{res}, bad) {
		t.Error("バグ入りの版を compare が通した")
	}
	if s := bad.Endpoints["GET /api/items/:num"]; s == nil || s.BodyDiff == 0 {
		t.Errorf("バグを入れた GET /api/items/:num の食い違いを数えていない: %+v", s)
	}
}
