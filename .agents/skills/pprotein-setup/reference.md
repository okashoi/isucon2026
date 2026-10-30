# pprotein Setup Reference (generic)

## Go: app pprof (:19001)

ユーザーが Go 書き換えを指示したあと、エージェントが PR を開くときの雛形。チャットに貼って終わりにしない。ホストの nginx/mysql 設定はここではなく `pprotein-setup.sh`。

```go
import "github.com/kaz/pprotein/integration/standalone"

func main() {
	go standalone.Integrate(":19001")
	// existing listen, e.g. :8080
}
```

依存: `go get github.com/kaz/pprotein@v1.2.4`（または現行リリース）。  
VCS stamp エラー時: `go build -buildvcs=false`。

listen アドレスやエントリポイント名は問題ごとに異なる。既存 `main` に最小追加する。

## Go: initialize webhook

ファイル名は任意（例 `pprotein_notify.go`）。

```go
func notifyPproteinCollect() {
	url := os.Getenv("PPROTEIN_WEBHOOK_URL")
	if url == "" {
		return
	}
	go func(url string) {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("pprotein webhook failed: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("pprotein webhook returned non-2xx: %d", resp.StatusCode)
		}
	}(url)
}
```

Initialize（またはベンチ開始前に必ず叩かれる初期化 API）の**先頭**で呼ぶ:

```go
func <initializeHandler>(...) {
	notifyPproteinCollect()
	// existing logic...
}
```

環境変数（systemd `EnvironmentFile` や `env.sh` など、その問題の流儀に合わせる）:

```sh
PPROTEIN_WEBHOOK_URL="http://<pprotein-host>:9000/api/group/collect"
```

## Non-Go apps

- **pprof**: 言語標準の profiler を `:19001` で出せれば targets の `pprof` に使う。出せなければ agent `:19000` の pprof を使い、アプリプロファイルではないと注記する。
- **webhook**: Initialize 相当から `GET $PPROTEIN_WEBHOOK_URL` を非同期で。失敗しても初期化は成功させる。

## nginx LTSV

`http` コンテキスト（または実際に access_log を書いている server）:

```nginx
log_format ltsv "time:$time_local"
                "\thost:$remote_addr"
                "\tforwardedfor:$http_x_forwarded_for"
                "\treq:$request"
                "\tstatus:$status"
                "\tmethod:$request_method"
                "\turi:$request_uri"
                "\tsize:$body_bytes_sent"
                "\treferer:$http_referer"
                "\tua:$http_user_agent"
                "\treqtime:$request_time"
                "\tcache:$upstream_http_x_cache"
                "\truntime:$upstream_http_x_runtime"
                "\tapptime:$upstream_response_time"
                "\tvhost:$host";

access_log  /var/log/nginx/access.log ltsv;
```

パスが違う場合は `PPROTEIN_HTTPLOG` と揃える。

## group/targets template

ホスト数は可変。1 ホストあたり:

```json
[
  {"Type":"pprof","Label":"<label>","URL":"http://<ip>:19001/debug/pprof/profile","Duration":10},
  {"Type":"httplog","Label":"<label>","URL":"http://<ip>:19000/debug/log/httplog","Duration":10},
  {"Type":"slowlog","Label":"<label>","URL":"http://<ip>:19000/debug/log/slowlog","Duration":10}
]
```

API（pprotein 上）:

- GET/POST `/api/group/targets`
- GET `/api/group/collect`

## Generating alp matching_groups

Do **not** reuse another contest's groups.

1. Collect routes from code or OpenAPI.
2. Keep only paths with parameters.
3. Replace each parameter segment with `[^/]+` (or a tighter class if IDs are numeric-only: `[0-9]+`).
4. Anchor with `^` and `$`.

Examples of **conversion** (illustrative, not contest-specific):

| Route definition | matching_groups entry |
|------------------|------------------------|
| `/api/items/{id}` | `^/api/items/[^/]+$` |
| `/api/items/:id/comments` | `^/api/items/[^/]+/comments$` |
| `/user/<user_id>/icon` | `^/user/[^/]+/icon$` |

```yaml
matching_groups:
  - ^/api/items/[^/]+$
  - ^/api/items/[^/]+/comments$
```

固定パスだけのルートは通常不要。動的セグメントが無いなら空に近いリストでもよい。

API: GET/POST `/api/httplog/config`

## Agent install without helper scripts

1. Download `pprotein_*_linux_amd64.tar.gz` from kaz/pprotein releases.
2. Install `pprotein-agent` to a fixed path (e.g. `/home/isucon/pprotein-agent`).
3. systemd unit: `Environment=PORT=19000`, `PPROTEIN_HTTPLOG=...`, `PPROTEIN_SLOWLOG=...`, `PPROTEIN_GIT_REPOSITORY=...`.
4. Ensure the service user can read both log files (`setfacl` or group perms).

## Connectivity pitfalls

- `/debug/log/httplog` and `/debug/log/slowlog` may hang under curl → probe `/debug/pprof/` with `--max-time 3`
- Cross-VPC: use Public IPs in targets; TCP 19000/19001 must already reach from pprotein (agent does **not** open SG)
- UFW inactive ≠ cloud SG open — if probes fail, tell the user; do not add SG rules
- On competition hosts: stay on the current git branch; never `checkout` / `switch` unless the user asks
