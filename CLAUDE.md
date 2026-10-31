# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

ISUCON2026 の競技用リポジトリ。題材は写真共有 SNS「ISUREAL」で、Go 実装（`go/`）をチューニングする。公開 HTTP API の互換性を保つかぎり、実装は大きく変えてよい。

README と `AGENTS.md` の一部は別の問題を想定したテンプレートのままで、MySQL 前提の記述や `mybin/mysql/` は使わない。この問題の DB は PostgreSQL 16。

## 守ること

- 作業前に [go/AGENTS.md](go/AGENTS.md) を読む。競技サーバーは本番として扱い、サーバー上では読み取りだけを行う。変更はブランチと PR を通し、デプロイは人間が行う。
- データ耐久性や可用性を下げる変更は提案もしない。[docs/doc-idea.md](docs/doc-idea.md) の `synchronous_commit = off` の案も対象で、go/AGENTS.md を優先する。
- サーバーのリージョン、ルーター経由の通信、`isucon-register.service` は維持する。競技の仕様は [docs/contest/manual.md](docs/contest/manual.md) と [docs/contest/regulation.md](docs/contest/regulation.md) を正とする。特に「データの反映」節の整合性要件は、リージョンをまたぐ読み取りにも適用される。
- 応答は日本語で書く。

## コマンド

```sh
make -C go                                  # go/isureal をビルド
cd go && go vet ./... && gofmt -l .         # 静的解析と整形の確認
cd mybin/isubench && go test ./...          # isubench のテスト
cd mybin/isubench && go test -run TestNumericIDs ./...   # 単体で実行
```

`go/` に既存のテストはない。追加するときは公開 HTTP API を通す特性テストにする（方針は `AGENTS.md` の Agent Instructions）。

次は競技サーバー上で人間が実行する。

```sh
make gogo                     # 全サービス停止、ビルド、ログ消去、起動
make logs/error               # アプリのエラーログ
make etc-sync/pull            # /etc を infra/<host>/etc へ収集
make etc-sync/deploy          # /etc への反映差分を確認（dry-run）
make etc-sync/deploy/apply    # /etc へ反映
make isubench/check           # 録画した通信を再生して正常版と比較
```

アプリをローカルで起動するには PostgreSQL が必要で、接続先は環境変数 `ISUREAL_DB_HOST` / `PORT` / `USER` / `PASSWORD` / `NAME` で渡す。待ち受けは API が `:8080`、pprof が `:19001`。`POST /api/initialize` は実行時のカレントディレクトリからの相対パス `../sql/init.sh` を呼ぶので、`go/` で起動し、psql も必要になる。

## アーキテクチャ

### サーバー構成

| サーバ | リージョン | 役割 |
|---|---|---|
| asia1 / europe1 / america1 | 各リージョン固定 | nginx と Go アプリ |
| asia2（`isucon-s4`） | asia | 共有 PostgreSQL 16 |

利用者の通信はルーターが `X-Country-Code` で各リージョンのサーバへ振り分ける。リージョン間の通信には遅延と帯域制限があり、europe1 と america1 のアプリは 1 クエリごとに DB まで WAN を往復する。スコアの倍率は europe と america が asia より高いので、DB への往復回数と転送量を減らす変更を優先して検討する。

### アプリ（`go/`、パッケージ `isureal`）

- `go/server.go` の `NewServer` が DB 接続とルーティングのすべてを持つ。ハンドラは `xxxHandler(h *HandlerContext) echo.HandlerFunc` の形で、API ごとの `*_api.go` にある。ドメインごとの DB アクセスは `post.go`、`user.go`、`comment.go` などに置き、`sqlx.QueryerContext` を受けて DB でもトランザクションでも呼べるようにしている。
- 大半のルートに `SessionMiddleware` と `RegionMiddleware` が付く。ログイン中の利用者のリージョンがサーバの `ISUREAL_REGION` と違うと `403 region_mismatch` になる。全リクエストに `IPBanMiddleware` が付き、`ip_bans` を 200ms ごとに読み直す。
- エラーは `NewAPIError` で返す。それ以外のエラーは `APIErrorHandler` が処理する。
- `GET /api/notifications` は SSE で、接続を保持せず 500ms ごとに DB をポーリングする。
- 画像は `image_objects`（`created_at` によるパーティションが 36 個）に `bytea` で保存し、`GET /api/images/:imageId` が署名を検証して返す。

### DB アクセスの注意

ドライバは pgx の stdlib を sqlx から使う。

- 既定の実行モードは `go/config.go` の DSN で指定した simple protocol（動的プレースホルダ）。第1引数に `preparedQuery` を渡したクエリだけ prepared statement で実行する。`bytea` を運ぶクエリと `image_objects` を引くクエリに使う。
- 2 つのモードでは `time.Time` のエンコードが違う。binary はナノ秒を切り捨て、text は PostgreSQL 側で丸める。Go で作った時刻を一方で書いて、もう一方で完全一致の検索をしない。
- simple protocol では値が SQL に文字列リテラルとして埋め込まれ、型は PostgreSQL が文脈から決める。文脈で決まらない箇所は `$1::timestamptz` のようにキャストを書く。
- `maxDBConns` はアプリ 3 台の合計が `max_connections = 100` に収まる値にする。上げるときは s4 の PostgreSQL の設定も合わせて変える。
- スキーマは `sql/001-schema.sql`。初期化は `sql/reset.sql` が、`initial_data_boundary` より新しい行の削除と `initial_*` テーブルからの復元で行う。テーブルやキャッシュを追加したら `reset.sql` か初期化処理にも反映する。

### 計測

- ベンチの結果は pprotein の HTTP API から読む。手順は [docs/pprotein-api.md](docs/pprotein-api.md)。
- `POST /api/initialize` は環境変数 `PPROTEIN_WEBHOOK_URL` があれば計測を開始する。
- 施策の候補は [docs/doc-idea.md](docs/doc-idea.md) にまとまっている。
- `.claude/skills` は `.agents/skills` へのシンボリックリンクで、isubench、pprotein のセットアップ、スロークエリ分析などのスキルがある。

## コミットと PR

- コミットの題は `type: 要約` の形にする（例: `perf: 投稿の画像書き込みをウィンドウロックの外に出す`）。
- PR には影響、切り戻し手順、設定の変更点を書く。リクエスト処理や DB アクセスを変えたときは計測結果も載せる。
