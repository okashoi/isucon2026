#!/bin/bash -eu
#
# fetch-top-slow.bash
#   リモートホストから MySQL slow query log を取得し、pt-query-digest を実行する。
# 使い方:
#   ./fetch-top-slow.bash [TARGET_HOST]
#   省略時 TARGET_HOST=isucon-s1
#
# 出力:
#   /tmp/mysql-slow.log  … 取得した slow query log
#   /tmp/top-slow.log    … pt-query-digest の結果（上位 10 クエリのみ）

TARGET_HOST="${1:-isucon-s1}"
SLOW_LOG="/tmp/mysql-slow.log"
TOP_LOG="/tmp/top-slow.log"

ssh "$TARGET_HOST" "sudo cp /var/log/mysql/mysql-slow.log mysql-slow.log && sudo chmod 777 mysql-slow.log"
scp -C "$TARGET_HOST:mysql-slow.log" "$SLOW_LOG"

pt-query-digest --limit 10 "$SLOW_LOG" > "$TOP_LOG"

echo "wrote: ${SLOW_LOG}, ${TOP_LOG} (host=${TARGET_HOST})"
