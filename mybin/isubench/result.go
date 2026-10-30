package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// EndpointStat は1回の再生でのエンドポイント別の集計。Diff は録画時のレスポンスとの食い違い。
type EndpointStat struct {
	N          int     `json:"n"`
	StatusDiff int     `json:"status_diff"`
	BodyDiff   int     `json:"body_diff"`
	Status5xx  int     `json:"status_5xx"`
	Errs       int     `json:"errs"`
	LatMs      float64 `json:"lat_ms_sum"`
}

type Result struct {
	Speed      float64                  `json:"speed"`
	Records    int                      `json:"records"`
	WallSec    float64                  `json:"wall_sec"`
	PlannedSec float64                  `json:"planned_sec"` // 予定どおり送れたら終わる時刻
	CPUSec     float64                  `json:"cpu_sec"`
	CPUValid   bool                     `json:"cpu_valid"`
	IDsMapped  int                      `json:"ids_mapped"`
	Endpoints  map[string]*EndpointStat `json:"endpoints"`
}

func (r Result) sortedKeys() []string {
	keys := make([]string, 0, len(r.Endpoints))
	for k := range r.Endpoints {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return r.Endpoints[keys[a]].N > r.Endpoints[keys[b]].N })
	return keys
}

func (r Result) print() {
	var tot EndpointStat
	fmt.Printf("%-52s %6s %6s %6s %5s %5s %8s\n", "endpoint", "n", "status", "body", "5xx", "err", "avg_ms")
	for _, k := range r.sortedKeys() {
		s := r.Endpoints[k]
		fmt.Printf("%-52s %6d %6d %6d %5d %5d %8.1f\n", k, s.N, s.StatusDiff, s.BodyDiff, s.Status5xx, s.Errs, s.LatMs/float64(s.N))
		tot.N += s.N
		tot.StatusDiff += s.StatusDiff
		tot.BodyDiff += s.BodyDiff
		tot.Status5xx += s.Status5xx
		tot.Errs += s.Errs
		tot.LatMs += s.LatMs
	}
	fmt.Printf("%-52s %6d %6d %6d %5d %5d %8.1f\n", "TOTAL", tot.N, tot.StatusDiff, tot.BodyDiff, tot.Status5xx, tot.Errs, tot.LatMs/float64(tot.N))
	cpu := "n/a"
	if r.CPUValid {
		cpu = fmt.Sprintf("%.2f", r.CPUSec)
	}
	fmt.Printf("wall_sec=%.2f cpu_sec=%s avg_ms=%.1f ids_mapped=%d\n", r.WallSec, cpu, tot.LatMs/float64(tot.N), r.IDsMapped)
	// 予定より大きく延びたのは、アプリ (か同居する再生側) が負荷に追いついていないから。
	// 飽和すると CPU 秒もレスポンス時間も揺れて、比べられなくなる (ISUCON14 で 9〜44% 揺れた)。
	if r.PlannedSec > 0 && r.WallSec > r.PlannedSec*1.3+2 {
		fmt.Printf("警告: 予定 %.0f 秒の再生に %.0f 秒かかった。負荷が飽和しているので、-speed か -until を下げる\n", r.PlannedSec, r.WallSec)
	}
}

// allowed は、正常版の再生で出た食い違いの最大値に対して、候補で許す件数。
// 同じコードでも乱数やタイミングで食い違いは揺れるので、幅を持たせる。
func allowed(baseMax int) int { return baseMax + max(3, baseMax/2) }

// warnIfBroken は、正常版なのにステータスの食い違いが多い (再生がそもそも成立していない) ときに知らせる。
// ID の対応が取れずに 401/404 が続く、初期化が効いていない、スタブが要る、などが原因になる。
func (r Result) warnIfBroken() {
	var n, diff int
	for _, s := range r.Endpoints {
		n += s.N
		diff += s.StatusDiff + s.Errs
	}
	if n == 0 || float64(diff)/float64(n) < 0.1 {
		return
	}
	fmt.Printf("\n警告: 正常版なのに %d / %d 件のステータスが録画と違う。再生が成立していない可能性が高い。多い順:\n", diff, n)
	keys := r.sortedKeys()
	sort.SliceStable(keys, func(a, b int) bool {
		x, y := r.Endpoints[keys[a]], r.Endpoints[keys[b]]
		return x.StatusDiff+x.Errs > y.StatusDiff+y.Errs
	})
	for _, k := range keys[:min(5, len(keys))] {
		s := r.Endpoints[k]
		fmt.Printf("  %-48s %d / %d\n", k, s.StatusDiff+s.Errs, s.N)
	}
}

// compare は候補の再生結果を正常版の基準と比べて表示し、正しさが保たれていれば true を返す。
func compare(base []Result, cand Result) bool {
	ok := true
	fmt.Printf("\n== 正しさ (正常版 %d 回分の最大値と比べる)\n", len(base))
	type metric struct {
		name string
		get  func(*EndpointStat) int
	}
	metrics := []metric{
		{"status", func(s *EndpointStat) int { return s.StatusDiff }},
		{"body", func(s *EndpointStat) int { return s.BodyDiff }},
		{"5xx", func(s *EndpointStat) int { return s.Status5xx }},
		{"err", func(s *EndpointStat) int { return s.Errs }},
	}
	for _, k := range cand.sortedKeys() {
		c := cand.Endpoints[k]
		for _, mt := range metrics {
			bmax := 0
			for _, b := range base {
				if s, found := b.Endpoints[k]; found {
					bmax = max(bmax, mt.get(s))
				}
			}
			if v := mt.get(c); v > allowed(bmax) {
				ok = false
				fmt.Printf("NG %-48s %-6s %d / %d 件 (正常版は最大 %d)\n", k, mt.name, v, c.N, bmax)
			}
		}
	}
	if ok {
		fmt.Println("OK 食い違いは正常版のばらつきの範囲")
	}

	fmt.Println("\n== 負荷 (同じリクエストの束を処理したときの CPU 秒と平均レスポンス時間)")
	var cpus, lats []float64
	for _, b := range base {
		if b.CPUValid {
			cpus = append(cpus, b.CPUSec)
		}
		lats = append(lats, b.avgMs())
	}
	if cand.CPUValid && len(cpus) > 0 {
		band("CPU 秒", cpus, cand.CPUSec, 0.05, "軽くなった", "重くなった")
	} else {
		fmt.Println("CPU 秒: 比べられない (-services 未指定か、再生中にサービスが再起動した)")
	}
	band("平均レスポンス ms", lats, cand.avgMs(), 0.25, "速くなった", "遅くなった")
	return ok
}

func (r Result) avgMs() float64 {
	var n int
	var lat float64
	for _, s := range r.Endpoints {
		n += s.N
		lat += s.LatMs
	}
	return lat / float64(max(n, 1))
}

// band は候補の値が正常版の最小〜最大から tol 以上外れたかで判定して表示する。
// ISUCON14 で同じ版を繰り返し測ると、等速の CPU 秒は 1〜2%、平均レスポンス時間は最大 40% 揺れた。
func band(name string, base []float64, v, tol float64, better, worse string) {
	lo, hi, sum := base[0], base[0], 0.0
	for _, b := range base {
		lo, hi, sum = min(lo, b), max(hi, b), sum+b
	}
	mean := sum / float64(len(base))
	verdict := "差なし (正常版のばらつきの範囲)"
	if v < lo*(1-tol) {
		verdict = better
	} else if v > hi*(1+tol) {
		verdict = worse
	}
	fmt.Printf("%s %s: 正常版 %.2f〜%.2f → 候補 %.2f (%+.0f%%)\n", name, verdict, lo, hi, v, (v/mean-1)*100)
}

type cpuSnap struct {
	ns     int64
	starts string
}

// cpuSnapshot は systemd が数えているサービスの累計 CPU 時間を、ホストをまたいで合計する。
func cpuSnapshot(hosts, services string) (cpuSnap, error) {
	var snap cpuSnap
	if services == "" {
		return snap, nil
	}
	svcs := strings.ReplaceAll(services, ",", " ")
	cmd := "systemctl show -p CPUUsageNSec --value " + svcs + "; echo --; systemctl show -p ActiveEnterTimestampMonotonic --value " + svcs
	for _, h := range strings.Split(hosts, ",") {
		var c *exec.Cmd
		if h == "local" {
			c = exec.Command("sh", "-c", cmd)
		} else {
			c = exec.Command("ssh", "-o", "BatchMode=yes", h, cmd)
		}
		out, err := c.Output()
		if err != nil {
			return snap, fmt.Errorf("%s: %w", h, err)
		}
		cpuPart, startPart, _ := strings.Cut(string(out), "--")
		for _, f := range strings.Fields(cpuPart) {
			v, err := strconv.ParseInt(f, 10, 64)
			if err != nil {
				return snap, fmt.Errorf("%s: CPUUsageNSec が数値でない (%q)。CPUAccounting が無効かもしれない", h, f)
			}
			snap.ns += v
		}
		snap.starts += h + ":" + strings.Join(strings.Fields(startPart), ",") + ";"
	}
	return snap, nil
}

func writeJSON(path string, v any) {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Fatal(err)
	}
}

func readRuns(path string) ([]Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var runs []Result
	return runs, json.Unmarshal(b, &runs)
}
