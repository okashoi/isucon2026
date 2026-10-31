---
name: slow-query-kill-postgres
description: >-
  リモートの PostgreSQL 16 ログを取得して pgbadger で集計し、
  合計時間の上位 10 クエリへの index チューニング案をチャットに出力する。
  Use when analyzing PostgreSQL slow queries, pgbadger, postgresql-16-main.log,
  isucon-s4, or when the user mentions postgres のスロークエリ / pgbadger /
  PostgreSQL index 提案. MySQL の pt-query-digest は slow-query-kill を使う。
---

# Slow Query Kill (PostgreSQL 16)

対象は PostgreSQL 16。既定ホストは `isucon-s4`。集計は pgbadger。
MySQL の slow log は `slow-query-kill` に任せる。

## 手順

1. ログ取得と pgbadger 実行（TARGET_HOST は省略可能。省略時 `isucon-s4`）

```bash
bash .agents/skills/slow-query-kill-postgres/scripts/fetch-top-slow.bash [TARGET_HOST]
```

ログの場所が違うときだけ `REMOTE_LOG` を付ける。`log_line_prefix` が stderr の自動判定に合わないときだけ `LOG_LINE_PREFIX` を付ける。

```bash
REMOTE_LOG=/var/lib/postgresql/16/main/log/postgresql.log \
  LOG_LINE_PREFIX='%m [%p] %q%u@%d ' \
  bash .agents/skills/slow-query-kill-postgres/scripts/fetch-top-slow.bash isucon-s4
```

ローカルに `pgbadger` が無いときはインストールを案内して止まり、提案は出さない。

2. `/tmp/top-slow-pg.log` を読む。中身は pgbadger の「Queries that took up the most time」上位 10 件だけ。既存 index は `sql/*.sql` の `CREATE INDEX` / `PRIMARY KEY` / `UNIQUE` で確認する。
3. 下記フォーマットだけをチャットに返す。ファイルには書き出さない。サーバの設定も変えない。
4. スクリプト失敗時、または集計が 0 件のときはエラー内容だけ報告する。`log_min_duration_statement` の変更、postgresql の再起動、ログの削除はしない。提案を捏造しない。

## 提案ルール

- 根拠は `/tmp/top-slow-pg.log` の行だけ（推測禁止）。単発の「Slowest queries」は対象にしない。合計時間の節だけを見る。
- `EXPLAIN` には Example の実クエリを使う。正規化クエリ（`?` と小文字）は条件の形を読むためだけに使う。
- 複合 index は等価条件 → 範囲 / `ORDER BY` の順。`ORDER BY col DESC` は `(col DESC)`。
- 既存 index と重複するもの、後方スキャンで足りるものは提案しない。
- index で効く形がないクエリは `CREATE INDEX` を書かない。
- 提案は `CREATE INDEX`。`CONCURRENTLY` は書かない（初期化 SQL のトランザクションに入れられる形）。
- `fsync` や `synchronous_commit` など耐久性を下げる設定は提案しない。

## 出力フォーマット

前置き・後書きなしで、この構造だけを書く。

# クエリチューニング
## 対象: `SELECT * FROM rides WHERE chair_id = ? ORDER BY updated_at DESC LIMIT ?`

```
1) 200ms - 2 - 80ms/120ms/100ms - select * from rides where chair_id = ? order by updated_at desc limit ?;
--
	- Example 1: 120ms - SELECT * FROM rides WHERE chair_id = 'abc' ORDER BY updated_at DESC LIMIT 1;
```

```sql
EXPLAIN SELECT * FROM rides WHERE chair_id = 'abc' ORDER BY updated_at DESC LIMIT 1;

CREATE INDEX ON rides (chair_id, updated_at DESC);
```

（統計と Example は `/tmp/top-slow-pg.log` の該当行をそのまま貼る。index を付けないときは `CREATE INDEX` 行を書かず、1 行で理由だけ書く。以降 `## 対象:` を 10 件分繰り返す）
