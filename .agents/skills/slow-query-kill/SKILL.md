---
name: slow-query-kill
description: >-
  リモートの MySQL slow query log を取得して pt-query-digest を実行し、
  上位 10 クエリへの index チューニング案をチャットに出力する。
  Use when analyzing slow queries, pt-query-digest, mysql-slow.log, index tuning,
  or when the user mentions fetch-top-slow / top-slow / スロークエリ index 提案.
---

# Slow Query Kill

## 手順

1. log 取得と pt-query-digest実行 (TARGET_HOSTは省略可能)

```bash
bash .agents/skills/slow-query-kill/scripts/fetch-top-slow.bash [TARGET_HOST]
```

2. `/tmp/top-slow.log`（上位 10 件のみ出力済み）を読む。既存 index は `sql/*.sql` で確認する。
3. 下記フォーマットだけをチャットに返す。ファイルには書き出さない。
4. スクリプト失敗時はエラー内容を報告し、提案を捏造しない。

## 提案ルール

- 根拠は digest の Query / Attribute のみ（推測禁止）。
- 複合 index は 等価条件 → 範囲 / `ORDER BY` の順。
- 既存 index と重複するものは提案しない。

## 出力フォーマット

前置き・後書きなしで、この構造だけを書く。

# クエリチューニング
## 対象: `SELECT * FROM rides WHERE chair_id = '01K9K74T2H7WBH7HHM1N950CCW' ORDER BY updated_at DESC LIMIT 1`

```
# Attribute    pct   total     min     max     avg     95%  stddev  median
# ============ === ======= ======= ======= ======= ======= ======= =======
# Count          4   45700
# Exec time     52    147s    15us    91ms     3ms    12ms     5ms    38us
```

```sql
EXPLAIN SELECT * FROM rides WHERE chair_id = '01K9K74T2H7WBH7HHM1N950CCW' ORDER BY updated_at DESC LIMIT 1;

ALTER TABLE rides ADD INDEX (chair_id, updated_at);
```

（Attribute 表は digest の該当行をそのまま貼る。以降 `## 対象:` を 10 件分繰り返す）
