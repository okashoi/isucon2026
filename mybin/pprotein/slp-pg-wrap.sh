#!/bin/sh -eu
#
# pprotein v1.2.4 は次を呼ぶ。
#   slp my --config ... --output standard --format tsv --file BODY
#   (internal/extproc/slp/processor.go)
# このスクリプトを PATH 上の slp として置く。本体は同じディレクトリの slp.real。
#
# 環境変数:
#   SLP_REAL             既定は PATH 上の slp.real
#   SLP_LOG_LINE_PREFIX  既定は '%m [%p] %q%u@%d '（末尾スペースあり。postgresql.conf と同じ）
#
# 導入（pprotein ホスト）:
#   bin=$(command -v slp)
#   sudo mv "$bin" "$(dirname "$bin")/slp.real"
#   sudo install -m 755 mybin/pprotein/slp-pg-wrap.sh "$bin"
#
# 切り戻し:
#   bin=$(command -v slp)
#   sudo mv "$(dirname "$bin")/slp.real" "$bin"
#
# --file の前処理: go-pg-slowlog は同一行の `statement:\s+(.+)` だけを取り、
# 取れたあとだけ継続行を足す。`statement:` の行に SQL が無いとクエリごと落ちる。
# その場合だけ、次の `YYYY-... LOG:` までを 1 行に畳む。

prefix=${SLP_LOG_LINE_PREFIX:-'%m [%p] %q%u@%d '}
real=${SLP_REAL:-slp.real}
case "$real" in
  */*) ;;
  *)
    if command -v "$real" >/dev/null 2>&1; then
      real=$(command -v "$real")
    fi
    ;;
esac
if [ ! -x "$real" ]; then
  echo "slp-pg-wrap: missing executable $real" >&2
  exit 127
fi

tmp=
cleanup() {
  if [ -n "$tmp" ]; then
    rm -f "$tmp"
  fi
}
trap cleanup EXIT

fold_bare_statement_records() {
  src=$1
  if [ ! -f "$src" ]; then
    echo "slp-pg-wrap: --file not found: $src" >&2
    exit 1
  fi
  if [ -z "$tmp" ]; then
    tmp=$(mktemp "${TMPDIR:-/tmp}/slp-pg-wrap.XXXXXX")
  fi
  awk '
    /^[0-9]{4}-[0-9]{2}-[0-9]{2} .* LOG:/ {
      if (folding) print buf
      if (index($0, "statement:") > 0) {
        rest = $0
        sub(/.*statement:/, "", rest)
        gsub(/^[ \t]+|[ \t]+$/, "", rest)
        if (rest != "") {
          folding = 0
          print
        } else {
          buf = $0
          folding = 1
        }
      } else {
        folding = 0
        print
      }
      next
    }
    {
      if (folding) {
        line = $0
        gsub(/^[ \t]+|[ \t]+$/, "", line)
        if (line != "") buf = buf " " line
        next
      }
      print
    }
    END { if (folding) print buf }
  ' "$src" > "$tmp"
}

if [ "${1:-}" = "my" ]; then
  shift
  set -- pg --log-line-prefix "$prefix" "$@"
fi

n=$#
i=0
while [ "$i" -lt "$n" ]; do
  arg=$1
  shift
  i=$((i + 1))
  if [ "$arg" = "--file" ]; then
    if [ "$i" -ge "$n" ]; then
      echo "slp-pg-wrap: --file needs a path" >&2
      exit 2
    fi
    path=$1
    shift
    i=$((i + 1))
    fold_bare_statement_records "$path"
    set -- "$@" --file "$tmp"
    continue
  fi
  set -- "$@" "$arg"
done

"$real" "$@"
