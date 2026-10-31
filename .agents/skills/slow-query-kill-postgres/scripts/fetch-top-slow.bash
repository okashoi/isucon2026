#!/bin/bash -eu
#
# fetch-top-slow.bash
#   リモートホストから PostgreSQL 16 のログを取得し、pgbadger で集計する。
# 使い方:
#   ./fetch-top-slow.bash [TARGET_HOST]
#   省略時 TARGET_HOST=isucon-s4
#   REMOTE_LOG=/path/to/postgresql.log ./fetch-top-slow.bash
#   LOG_LINE_PREFIX='%m [%p] %q%u@%d ' ./fetch-top-slow.bash
#
# 出力:
#   /tmp/postgresql-16.log … 取得したログ
#   /tmp/pgbadger.txt      … pgbadger の text レポート
#   /tmp/top-slow-pg.log   … 「Queries that took up the most time」の上位 10 件

TARGET_HOST="${1:-isucon-s4}"
REMOTE_LOG="${REMOTE_LOG:-/var/log/postgresql/postgresql-16-main.log}"
LOCAL_LOG="/tmp/postgresql-16.log"
REPORT="/tmp/pgbadger.txt"
TOP_LOG="/tmp/top-slow-pg.log"

if ! command -v pgbadger >/dev/null 2>&1; then
  echo "pgbadger が PATH にありません。ローカルに入れてから再実行してください。" >&2
  exit 1
fi

case "$REMOTE_LOG" in
  *\'*)
    echo "REMOTE_LOG に単一引用符は使えません: ${REMOTE_LOG}" >&2
    exit 1
    ;;
esac

ssh -C "$TARGET_HOST" "sudo cat '${REMOTE_LOG}'" > "$LOCAL_LOG"

if [ ! -s "$LOCAL_LOG" ]; then
  echo "ログが空です: ${TARGET_HOST}:${REMOTE_LOG}" >&2
  exit 1
fi

pg_args=(
  -q
  -t 10
  -x text
  -P
  -f stderr
  --sample 1
  --disable-error
  --disable-hourly
  --disable-session
  --disable-connection
  --disable-temporary
  --disable-checkpoint
  --disable-autovacuum
  --disable-lock
  -o "$REPORT"
)
if [ -n "${LOG_LINE_PREFIX:-}" ]; then
  pg_args+=(--prefix "$LOG_LINE_PREFIX")
fi

pgbadger "${pg_args[@]}" "$LOCAL_LOG"

awk '
  /^- Queries that took up the most time/ {capture=1}
  capture && /^- Most frequent queries/ {exit}
  capture {print}
' "$REPORT" > "$TOP_LOG"

if ! grep -qE '^[0-9]+\) ' "$TOP_LOG"; then
  echo "pgbadger がクエリを集計できませんでした。ログ形式か log_min_duration_statement を確認してください。" >&2
  echo "report: ${REPORT}" >&2
  exit 1
fi

echo "wrote: ${LOCAL_LOG}, ${REPORT}, ${TOP_LOG} (host=${TARGET_HOST})"
