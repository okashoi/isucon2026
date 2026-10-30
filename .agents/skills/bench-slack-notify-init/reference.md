# Reference (Go Functions)

`Initialize` から呼ぶ最小構成。アプリ側の `bench_notify.go`（例: `go/bench_notify.go`）に配置する。

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultBenchEndDelaySec = 60

func benchNotifyEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("BENCH_NOTIFY_ENABLED")))
	switch v {
	case "":
		return true
	case "false", "0", "off", "no":
		return false
	default:
		return true
	}
}

func benchEndDelaySec() int {
	v := os.Getenv("BENCH_END_NOTIFY_AFTER_SEC")
	if v == "" {
		return defaultBenchEndDelaySec
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultBenchEndDelaySec
	}
	return n
}

func notifySlack(text string) {
	webhook := os.Getenv("SLACK_WEBHOOK_URL")
	if webhook == "" {
		return
	}

	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		log.Printf("slack payload marshal failed: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, webhook, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("slack request build failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("slack request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("slack request returned non-2xx: %d", resp.StatusCode)
	}
}

func benchEndHintSlackText(delaySec int) string {
	return fmt.Sprintf("<!here> ✅ ベンチマーク終了（目安） 開始から%d秒経過・計測終了想定", delaySec)
}

func notifyBenchEndHint() {
	if !benchNotifyEnabled() {
		return
	}

	delaySec := benchEndDelaySec()

	go func(delaySec int) {
		time.Sleep(time.Duration(delaySec) * time.Second)
		notifySlack(benchEndHintSlackText(delaySec))
	}(delaySec)
}
```

初期化ハンドラ側:

```go
func postInitialize(w http.ResponseWriter, r *http.Request) {
	notifyBenchEndHint()
	// existing logic...
}
```
