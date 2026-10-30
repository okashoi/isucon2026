---
name: bench-slack-notify-init
description: Add a one-line Slack end-hint notification in Go Initialize handlers via Incoming Webhook. After /initialize access, sends <!here> message N seconds later (default 60) indicating benchmark likely finished. No start notification. Use for bench end alerts or delayed Slack notifications on initialize.
---

# Bench Slack Notify Init

## Purpose

`Initialize`（または同等の初期化ハンドラ）から、**ベンチ終了目安の Slack 通知を 1 行だけ**送る。

- `/initialize` アクセスから N 秒後に 1 回通知（既定 60 秒）
- **開始通知は送らない**
- 文面は 1 行、`<!here>` でチャンネル通知
- 時刻・ホスト名は文面に含めない

遅延秒数と通知 ON/OFF は環境変数で制御する。

## Quick Start

1. 初期化ハンドラの先頭で `notifyBenchEndHint()` を呼ぶ。
2. `bench_notify.go` に `notifySlack()` / `notifyBenchEndHint()` を実装する。
3. `BENCH_NOTIFY_ENABLED` で通知 ON/OFF（未設定時 ON）。
4. `BENCH_END_NOTIFY_AFTER_SEC` で遅延秒数 N（未設定時 60）。
5. `SLACK_WEBHOOK_URL` に Slack Incoming Webhook URL を設定する。

## Implementation Steps (Go)

### 1) 必要な環境変数

- `SLACK_WEBHOOK_URL` (通知する場合は必須)
- `BENCH_NOTIFY_ENABLED` (任意, 既定: `true`)
- `BENCH_END_NOTIFY_AFTER_SEC` (任意, 既定: `60`)

### 2) Initialize で通知を呼ぶ

初期化処理の**先頭**で `notifyBenchEndHint()` を呼ぶだけにする。

```go
func postInitialize(w http.ResponseWriter, r *http.Request) {
	notifyBenchEndHint()
	// existing logic...
}
```

通知の失敗で初期化処理を落とさない。送信失敗時のみログに出す（スキップ時はログを出さない）。

`BENCH_NOTIFY_ENABLED=false` のときは通知処理を行わない（ログも出さない）。

### 3) 通知 ON/OFF の解決

`BENCH_NOTIFY_ENABLED` を読み取り:

- 未設定なら `true`
- `false`, `0`, `off`, `no`（大文字小文字は無視）なら `false`
- それ以外は `true`

### 4) 遅延秒数 N の解決

`BENCH_END_NOTIFY_AFTER_SEC` を `strconv.Atoi` で読み取り:

- 未設定/不正値/0以下なら `60`
- それ以外は指定値を使用

### 5) 非同期送信

`Initialize` のレスポンス遅延を避けるため、`time.Sleep` と `notifySlack` は goroutine 内で行う。ハンドラ本体では待たない。

### 6) メッセージ構成

**1 行のみ**。改行・時刻・ホストは含めない。

```
<!here> ✅ ベンチマーク終了（目安） 開始から{N}秒経過・計測終了想定
```

- `<!here>` は Slack Incoming Webhook で `@here` 相当
- `{N}` は `BENCH_END_NOTIFY_AFTER_SEC` の解決値

## References

- 関数リファレンス: [reference.md](reference.md)
- アプリ側に `bench_notify.go` を追加（例: `go/bench_notify.go`）

## Validation Checklist

- [ ] `/initialize` 直後には通知が来ない
- [ ] N 秒後に 1 行の終了目安通知が来る（`<!here>` 含む）
- [ ] `BENCH_END_NOTIFY_AFTER_SEC=10` で 10 秒後に来る
- [ ] `BENCH_NOTIFY_ENABLED=false` で通知が送られない
- [ ] `SLACK_WEBHOOK_URL` 未設定でも initialize が正常完了する
- [ ] Webhook 送信失敗時にログへ理由が出る

## Agent Behavior

このスキルを使うとき、エージェントは次を守る:

1. 既存の `Initialize` 実装を壊さず最小差分で追加する。
2. 開始通知・多行文面・時刻/ホスト表示は追加しない。
3. 同期的な待機 (`time.Sleep`) をリクエスト本体で行わない。
4. デフォルト遅延 60 秒を保持し、N 秒に一般化する。
5. 環境変数で通知を ON/OFF できるようにする（既定 ON）。
6. 通知送信失敗を致命エラーにしない。スキップ時は静かに return する。
