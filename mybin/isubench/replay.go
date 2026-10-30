package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// サーバーが発行する文字列の ID・トークン (ULID, UUID, hex, base64url) とみなす既定の形。
const defaultIDRegex = `[0-9A-Za-z_-]{20,}`

// 比較の前に潰す時刻 (ミリ秒・秒の UNIX 時刻と RFC3339)。
var tsRe = regexp.MustCompile(`\b1[0-9]{12}\b|\b1[0-9]{9}\b|\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)

// 数値 ID とみなす JSON・クエリのキー (id, user_id, userId)。
var idKeyRe = regexp.MustCompile(`(?i)^id$|_id$|[a-z]Id$`)

// JSON 本文の "xxx_id": 123。
var jsonNumIDRe = regexp.MustCompile(`"([A-Za-z0-9_]*)"\s*:\s*(\d+)`)

// idMap は文字列の ID・トークンについて、録画時の値と再生時に発行された値の対応を持つ。
// 文字列 ID は問題全体でほぼ一意なので、1つの表で足りる。数値 ID は表ごとに重なるので numBind で扱う。
type idMap struct {
	id       *regexp.Regexp
	mu       sync.RWMutex
	fwd, rev map[string]string
}

func newIDMap(id *regexp.Regexp) *idMap {
	return &idMap{id: id, fwd: map[string]string{}, rev: map[string]string{}}
}

// learn は録画と再生のレスポンスに出てくる ID を出現順に突き合わせる。
// 個数が違う (一覧の件数が変わった) ときは対応が取れないので覚えない。
func (m *idMap) learn(oldS, newS string) {
	o, n := m.id.FindAllString(oldS, -1), m.id.FindAllString(newS, -1)
	if len(o) != len(n) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range o {
		if _, ok := m.fwd[o[i]]; !ok && o[i] != n[i] {
			m.fwd[o[i]] = n[i]
			m.rev[n[i]] = o[i]
		}
	}
}

func (m *idMap) rewrite(s string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.id.ReplaceAllStringFunc(s, func(t string) string {
		if n, ok := m.fwd[t]; ok {
			return n
		}
		return t
	})
}

// canon は比較用に正規化する。再生側の文字列 ID は録画時の値に戻し、対応の取れない文字列 ID は <ID> に潰す。
// 数値 ID は、その回の再生 (または録画) で作られたものなら <NUM> に潰し、初期データの ID はそのまま比べる。
func (m *idMap) canon(s string, replayed bool, created *numSet) string {
	m.mu.RLock()
	s = m.id.ReplaceAllStringFunc(s, func(t string) string {
		if replayed {
			if o, ok := m.rev[t]; ok {
				return o
			}
		} else if _, ok := m.fwd[t]; ok {
			return t
		}
		return "<ID>"
	})
	m.mu.RUnlock()
	s = jsonNumIDRe.ReplaceAllStringFunc(s, func(kv string) string {
		sub := jsonNumIDRe.FindStringSubmatch(kv)
		if idKeyRe.MatchString(sub[1]) && created.has(sub[2]) {
			return strings.TrimSuffix(kv, sub[2]) + "<NUM>"
		}
		return kv
	})
	return tsRe.ReplaceAllString(s, "<TS>")
}

type numSet struct {
	mu sync.RWMutex
	m  map[string]bool
}

func (s *numSet) addAll(paths map[string][][]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for v := range paths {
		s.m[v] = true
	}
}

func (s *numSet) has(v string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[v]
}

// idPaths は JSON 本文の id 系キーにある数値と、その位置 (キーと添字の列) を返す。
func idPaths(body string) map[string][][]any {
	var v any
	d := json.NewDecoder(strings.NewReader(body))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil
	}
	out := map[string][][]any{}
	var walk func(v any, key string, path []any)
	walk = func(v any, key string, path []any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				walk(c, k, append(path[:len(path):len(path)], k))
			}
		case []any:
			for i, c := range x {
				walk(c, key, append(path[:len(path):len(path)], i))
			}
		case json.Number:
			if idKeyRe.MatchString(key) && isDigits(x.String()) {
				out[x.String()] = append(out[x.String()], path)
			}
		}
	}
	walk(v, "", nil)
	return out
}

func valueAt(v any, path []any) (string, bool) {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return "", false
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				return "", false
			}
			v = a[k]
		}
	}
	n, ok := v.(json.Number)
	return n.String(), ok
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// rewriteNums は、パスの数値だけの区切り、id 系のクエリ・フォームの値、JSON の id 系キーの数値を table で置き換える。
func rewriteNums(s string, table map[string]string, isURI bool) string {
	if len(table) == 0 {
		return s
	}
	mapOne := func(v string) string {
		if n, ok := table[v]; ok {
			return n
		}
		return v
	}
	form := func(q string) string {
		pairs := strings.Split(q, "&")
		for i, p := range pairs {
			if k, v, ok := strings.Cut(p, "="); ok && idKeyRe.MatchString(k) && isDigits(v) {
				pairs[i] = k + "=" + mapOne(v)
			}
		}
		return strings.Join(pairs, "&")
	}
	if isURI {
		path, query, hasQ := strings.Cut(s, "?")
		segs := strings.Split(path, "/")
		for i, seg := range segs {
			if isDigits(seg) {
				segs[i] = mapOne(seg)
			}
		}
		s = strings.Join(segs, "/")
		if hasQ {
			s += "?" + form(query)
		}
		return s
	}
	s = jsonNumIDRe.ReplaceAllStringFunc(s, func(kv string) string {
		sub := jsonNumIDRe.FindStringSubmatch(kv)
		if !idKeyRe.MatchString(sub[1]) {
			return kv
		}
		return strings.TrimSuffix(kv, sub[2]) + mapOne(sub[2])
	})
	if strings.Contains(s, "=") && !strings.ContainsAny(s, "{[") {
		s = form(s)
	}
	return s
}

type numUse struct{ v, key string }

// reqNumIDs はリクエストの中で数値 ID として使われている値と、そのキー (パスの区切りなら空) を返す。
func reqNumIDs(r Record) []numUse {
	var vals []numUse
	path, query, _ := strings.Cut(r.URI, "?")
	for _, seg := range strings.Split(path, "/") {
		if isDigits(seg) {
			vals = append(vals, numUse{v: seg})
		}
	}
	form := query
	if !r.ReqB64 {
		for _, sub := range jsonNumIDRe.FindAllStringSubmatch(r.ReqBody, -1) {
			if idKeyRe.MatchString(sub[1]) {
				vals = append(vals, numUse{sub[2], sub[1]})
			}
		}
		if !strings.ContainsAny(r.ReqBody, "{[") {
			form += "&" + r.ReqBody
		}
	}
	for _, p := range strings.Split(form, "&") {
		if k, v, ok := strings.Cut(p, "="); ok && idKeyRe.MatchString(k) && isDigits(v) {
			vals = append(vals, numUse{v, k})
		}
	}
	return vals
}

// numBind は「リクエストの数値 old は、リクエスト p のレスポンスの path の位置から得た」という対応。
type numBind struct {
	old  string
	p    int
	path []any
}

var digitSegRe = regexp.MustCompile(`/\d+(/|$)`)

func endpointKey(r Record, id *regexp.Regexp) string {
	p, _, _ := strings.Cut(r.URI, "?")
	p = id.ReplaceAllString(p, ":id")
	for digitSegRe.MatchString(p) {
		p = digitSegRe.ReplaceAllString(p, "/:num$1")
	}
	return r.Method + " " + p
}

type rewriteFlag [][2]string

func (f *rewriteFlag) String() string { return fmt.Sprint(*f) }
func (f *rewriteFlag) Set(v string) error {
	old, new, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("-rewrite は old=new の形で指定する: %q", v)
	}
	*f = append(*f, [2]string{old, new})
	return nil
}

type replayOpts struct {
	target, host string
	speed        float64
	barrier, id  *regexp.Regexp
	rewrites     [][2]string
}

func replay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	in := fs.String("in", "record.jsonl", "録画ファイル")
	target := fs.String("target", "https://127.0.0.1", "送り先 (nginx)")
	host := fs.String("host", "", "Host ヘッダと TLS の SNI。空なら録画時のまま")
	speed := fs.Float64("speed", 4, "録画時の間隔を何倍速で再生するか")
	until := fs.Duration("until", 0, "録画の先頭からこの時間分だけ再生する (0 なら全部)。重い録画を飽和させずに短く流すのに使う")
	stub := fs.String("stub", "", "外部サービス (決済など) の代わりに、POST は 204・GET は [] を返すスタブを立てるアドレス")
	barrier := fs.String("barrier", `^(/api)?/initialize`, "これに一致するリクエストが終わるまで、後続を送らない")
	idExpr := fs.String("id-regex", defaultIDRegex, "サーバーが発行する文字列の ID・トークンとみなす形")
	services := fs.String("services", "", "CPU 秒を測る systemd サービス (カンマ区切り)")
	hosts := fs.String("hosts", "local", "-services を測るホスト (カンマ区切り。local 以外は ssh する)")
	diffOut := fs.String("diff", "diff.txt", "食い違いの例の出力先")
	out := fs.String("out", "", "結果 JSON の出力先")
	baseline := fs.String("baseline", "", "結果を正常版の基準としてこのファイルに追記する")
	against := fs.String("against", "", "このファイルの基準と比べ、食い違いが増えていたら exit 1")
	var rewrites rewriteFlag
	fs.Var(&rewrites, "rewrite", "リクエストの URI・本文・ヘッダの文字列を置き換える old=new (繰り返し可)。公式ベンチ側の URL をスタブに向けるのに使う")
	fs.Parse(args)

	recs := load(*in)
	if *until > 0 {
		cut := float64(until.Milliseconds())
		n := sort.Search(len(recs), func(i int) bool { return recs[i].TMs >= cut })
		recs = recs[:n]
	}
	if len(recs) == 0 {
		log.Fatalf("%s にリクエストがない", *in)
	}
	if *stub != "" {
		go func() {
			log.Fatal(http.ListenAndServe(*stub, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Write([]byte("[]"))
			})))
		}()
	}

	cpu0, cpuErr := cpuSnapshot(*hosts, *services)
	res, samples := runReplay(replayOpts{
		target: *target, host: *host, speed: *speed,
		barrier: regexp.MustCompile(*barrier), id: regexp.MustCompile(*idExpr), rewrites: rewrites,
	}, recs)
	if cpuErr != nil {
		log.Printf("CPU 秒を測れない: %v", cpuErr)
	} else if *services != "" {
		cpu1, err := cpuSnapshot(*hosts, *services)
		// サービスが再起動するとカウンタが 0 に戻るので、起動時刻が変わったら値を捨てる。
		if err == nil && cpu1.starts == cpu0.starts {
			res.CPUSec = float64(cpu1.ns-cpu0.ns) / 1e9
			res.CPUValid = true
		}
	}

	writeSamples(*diffOut, samples)
	res.print()
	if *out != "" {
		writeJSON(*out, res)
	}
	if *baseline != "" {
		res.warnIfBroken()
		runs, _ := readRuns(*baseline)
		writeJSON(*baseline, append(runs, res))
		fmt.Printf("基準に追加しました (%s, %d 回分)\n", *baseline, len(runs)+1)
	}
	if *against != "" {
		runs, err := readRuns(*against)
		if err != nil || len(runs) == 0 {
			log.Fatalf("基準 %s を読めない。先に -baseline で正常版を再生しておく: %v", *against, err)
		}
		if !compare(runs, res) {
			os.Exit(1)
		}
	}
}

// runReplay は録画を録画時の間隔の 1/speed で送り直し、録画時のレスポンスとの食い違いを数える。
func runReplay(o replayOpts, recs []Record) (Result, map[string][]string) {
	applyRewrites := func(s string) string {
		for _, rw := range o.rewrites {
			s = strings.ReplaceAll(s, rw[0], rw[1])
		}
		return s
	}
	pl := makePlan(recs, o.barrier, o.id)

	tlsConf := &tls.Config{InsecureSkipVerify: true}
	if o.host != "" {
		tlsConf.ServerName = o.host
	}
	client := &http.Client{
		Timeout:       20 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: tlsConf, MaxIdleConnsPerHost: 4096},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	m := newIDMap(o.id)
	oldCreated, newCreated := &numSet{m: map[string]bool{}}, &numSet{m: map[string]bool{}}
	for i, r := range recs {
		if r.Method != http.MethodGet {
			oldCreated.addAll(pl.respIDs[i])
		}
	}
	// 数値 ID の付け替え元になるレスポンス。書き込みは done[p] を閉じる前、読み出しは done[p] を待った後。
	replayedJSON := make([]any, len(recs))
	done := make([]chan struct{}, len(recs))
	for i := range done {
		done[i] = make(chan struct{})
	}
	var mu sync.Mutex
	res := Result{Speed: o.speed, Records: len(recs), Endpoints: map[string]*EndpointStat{}}
	samples := map[string][]string{}

	start := time.Now()
	var wg sync.WaitGroup
	for i, r := range recs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done[i])
			time.Sleep(time.Until(start.Add(time.Duration(r.TMs / o.speed * float64(time.Millisecond)))))
			for _, d := range pl.deps[i] {
				<-done[d]
			}

			nums := map[string]string{}
			for _, b := range pl.binds[i] {
				if v, ok := valueAt(replayedJSON[b.p], b.path); ok {
					nums[b.old] = v
				}
			}
			uri := rewriteNums(m.rewrite(applyRewrites(r.URI)), nums, true)
			body := applyRewrites(r.ReqBody)
			if !r.ReqB64 {
				body = rewriteNums(m.rewrite(body), nums, false)
			}
			req, err := http.NewRequest(r.Method, o.target+uri, strings.NewReader(body))
			if err != nil {
				log.Printf("リクエストを作れない %s %s: %v", r.Method, r.URI, err)
				return
			}
			req.Host = r.Host
			if o.host != "" {
				req.Host = o.host
			}
			for k, v := range r.ReqHeader {
				req.Header.Set(k, m.rewrite(applyRewrites(v)))
			}
			t0 := time.Now()
			resp, err := client.Do(req)
			var got []byte
			if err == nil {
				got, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			lat := float64(time.Since(t0).Microseconds()) / 1000

			key := endpointKey(r, o.id)
			mu.Lock()
			s := res.Endpoints[key]
			if s == nil {
				s = &EndpointStat{}
				res.Endpoints[key] = s
			}
			s.N++
			s.LatMs += lat
			if err != nil {
				s.Errs++
				mu.Unlock()
				return
			}
			mu.Unlock()

			gotS := string(got)
			want := r.RespBody
			if !r.RespB64 {
				if pl.producer[i] {
					var v any
					d := json.NewDecoder(strings.NewReader(gotS))
					d.UseNumber()
					if d.Decode(&v) == nil {
						replayedJSON[i] = v
					}
				}
				if r.Method != http.MethodGet {
					newCreated.addAll(idPaths(gotS))
				}
				m.learn(r.RespBody+" "+r.RespHeader["Set-Cookie"], gotS+" "+strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
				want, gotS = m.canon(want, false, oldCreated), m.canon(gotS, true, newCreated)
			}
			mu.Lock()
			defer mu.Unlock()
			if resp.StatusCode >= 500 {
				s.Status5xx++
			}
			switch {
			case r.Status != resp.StatusCode:
				s.StatusDiff++
			case want != gotS:
				s.BodyDiff++
			default:
				return
			}
			if len(samples[key]) < 3 {
				samples[key] = append(samples[key], fmt.Sprintf("%s %s\n  want %d %s\n  got  %d %s",
					r.Method, r.URI, r.Status, trunc(want), resp.StatusCode, trunc(gotS)))
			}
		}()
	}
	wg.Wait()
	res.WallSec = time.Since(start).Seconds()
	res.PlannedSec = recs[len(recs)-1].TMs / 1000 / o.speed
	res.IDsMapped = len(m.fwd)
	return res, samples
}

type plan struct {
	deps     [][]int
	binds    [][]numBind
	respIDs  []map[string][][]any // 録画のレスポンスにある数値 ID とその位置
	producer []bool               // 数値 ID の付け替え元として再生時のレスポンスを取っておくか
}

// makePlan は各リクエストが送る前に待つべきリクエストと、数値 ID の出どころを決める。
// 待つのは、使う ID を返し終えたリクエスト、同じ認証情報で直前に終わっていたリクエスト、直前の barrier (initialize)。
func makePlan(recs []Record, barrier, id *regexp.Regexp) plan {
	end := func(r Record) float64 { return r.TMs + r.DurMs }
	pl := plan{
		deps: make([][]int, len(recs)), binds: make([][]numBind, len(recs)),
		respIDs: make([]map[string][][]any, len(recs)), producer: make([]bool, len(recs)),
	}
	producer := map[string]int{}
	for i, r := range recs {
		if r.RespB64 {
			continue
		}
		pl.respIDs[i] = idPaths(r.RespBody)
		for _, t := range id.FindAllString(r.RespBody+" "+r.RespHeader["Set-Cookie"], -1) {
			if p, ok := producer[t]; !ok || end(r) < end(recs[p]) {
				producer[t] = i
			}
		}
	}

	lastBySession := map[string]int{}
	history := map[string][]int{}
	lastBarrier := -1
	for i, r := range recs {
		seen := map[int]bool{}
		add := func(p int) {
			if !seen[p] {
				seen[p] = true
				pl.deps[i] = append(pl.deps[i], p)
			}
		}
		if lastBarrier >= 0 {
			add(lastBarrier)
		}
		if barrier.MatchString(r.URI) {
			lastBarrier = i
		}
		uses := r.URI + " " + r.ReqBody
		for _, v := range r.ReqHeader {
			uses += " " + v
		}
		for _, t := range id.FindAllString(uses, -1) {
			if p, ok := producer[t]; ok && p < i {
				add(p)
			}
		}

		sess := r.ReqHeader["Cookie"]
		if sess == "" {
			sess = r.ReqHeader["Authorization"]
		}
		// 数値 ID は、同じ利用者が直前までに受け取ったレスポンスから探す。見つからなければ全体の直近から探す。
		// ponytail: 全体は直近 2000 件だけ見る。他人が作った古い ID を使う問題では取りこぼす
		for _, u := range reqNumIDs(r) {
			if b, ok := findNumSource(recs, pl.respIDs, history[sess], u, r.TMs, end); ok {
				pl.binds[i] = append(pl.binds[i], b)
			} else if b, ok := findNumSource(recs, pl.respIDs, recentIdx(i, 2000), u, r.TMs, end); ok {
				pl.binds[i] = append(pl.binds[i], b)
			} else {
				continue
			}
			p := pl.binds[i][len(pl.binds[i])-1].p
			pl.producer[p] = true
			add(p)
		}

		if sess != "" {
			if p, ok := lastBySession[sess]; ok && end(recs[p]) <= r.TMs {
				add(p)
			}
			lastBySession[sess] = i
			history[sess] = append(history[sess], i)
		}
	}
	return pl
}

func recentIdx(i, n int) []int {
	idx := make([]int, 0, n)
	for p := max(0, i-n); p < i; p++ {
		idx = append(idx, p)
	}
	return idx
}

// findNumSource は candidates を新しい順に見て、数値 u.v を id 系キーに持ち、
// リクエストを送る時刻 (startMs) までに返し終えていたレスポンスを探す。
// 同じ数値が別の表の ID として何度も出るので、キー名が同じ位置 (owner_id=3 なら "owner_id": 3)、
// 次に "id" の位置 (パスの /items/3 はそのものの ID のことが多い)、の順に選ぶ。同じ順位なら新しいものを使う。
func findNumSource(recs []Record, respIDs []map[string][][]any, candidates []int, u numUse, startMs float64, end func(Record) float64) (numBind, bool) {
	rank := func(path []any) int {
		last, _ := path[len(path)-1].(string)
		switch {
		case u.key != "" && last == u.key:
			return 0
		case last == "id":
			return 1
		}
		return 2
	}
	var best numBind
	bestRank := 3
	for k := len(candidates) - 1; k >= 0 && bestRank > 0; k-- {
		p := candidates[k]
		if end(recs[p]) > startMs {
			continue
		}
		for _, path := range respIDs[p][u.v] {
			if r := rank(path); r < bestRank {
				best, bestRank = numBind{old: u.v, p: p, path: path}, r
			}
		}
	}
	return best, bestRank < 3
}

func load(path string) []Record {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	recs, err := decodeRecords(f)
	if err != nil {
		log.Fatal(err)
	}
	return recs
}

func decodeRecords(rd io.Reader) ([]Record, error) {
	var recs []Record
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}
		for _, b := range []struct {
			s   *string
			b64 bool
		}{{&r.ReqBody, r.ReqB64}, {&r.RespBody, r.RespB64}} {
			if b.b64 {
				raw, err := base64.StdEncoding.DecodeString(*b.s)
				if err != nil {
					return nil, err
				}
				*b.s = string(raw)
			}
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// 録画はレスポンス完了順に書かれるので、送信時刻順に並べ直す。
	sort.SliceStable(recs, func(a, b int) bool { return recs[a].TMs < recs[b].TMs })
	return recs, nil
}

func writeSamples(path string, samples map[string][]string) {
	var buf bytes.Buffer
	keys := make([]string, 0, len(samples))
	for k := range samples {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, s := range samples[k] {
			fmt.Fprintln(&buf, s)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		log.Printf("食い違いの例を書けない: %v", err)
	}
}

func trunc(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
