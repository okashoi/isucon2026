#!/bin/sh
# slp-pg-wrap.sh
#   pprotein は常に `slp my ...` を呼ぶ。Postgres の duration ログを
#   UI で集計するため、my を pg に差し替え、log_line_prefix を渡す。
# 入れ方 (pprotein ホスト):
#   sudo mv /usr/local/bin/slp /usr/local/bin/slp.real
#   sudo install -m 0755 mybin/pprotein/slp-pg-wrap.sh /usr/local/bin/slp
# 戻す:
#   sudo mv /usr/local/bin/slp.real /usr/local/bin/slp
#
# 環境変数:
#   SLP_REAL              (既定: /usr/local/bin/slp.real)
#   SLP_LOG_LINE_PREFIX   (既定: '%m [%p] %q%u@%d ' = 本問 postgresql.conf)

set -eu

SLP_REAL=${SLP_REAL:-/usr/local/bin/slp.real}
# Match infra/*/etc/postgresql/16/main/postgresql.conf log_line_prefix.
SLP_LOG_LINE_PREFIX=${SLP_LOG_LINE_PREFIX:-%m [%p] %q%u@%d }

if [ ! -x "$SLP_REAL" ]; then
  echo "slp-pg-wrap: missing executable $SLP_REAL" >&2
  exit 127
fi

if [ "${1:-}" = "my" ]; then
  shift
  exec "$SLP_REAL" pg --log-line-prefix "$SLP_LOG_LINE_PREFIX" "$@"
fi

exec "$SLP_REAL" "$@"
