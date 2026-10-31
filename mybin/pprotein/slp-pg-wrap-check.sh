#!/bin/sh -eu

here=$(CDPATH= cd "$(dirname "$0")" && pwd)
wrap=$here/slp-pg-wrap.sh
fixture=$here/testdata/slow-multiline.log

if [ -n "${SLP_REAL:-}" ]; then
  real=$SLP_REAL
elif [ -x /tmp/slpbin/slp ]; then
  real=/tmp/slpbin/slp
elif command -v slp >/dev/null 2>&1; then
  real=$(command -v slp)
else
  echo "slp-pg-wrap-check: slp が無い。SLP_REAL か /tmp/slpbin/slp を用意する" >&2
  exit 1
fi

out=$(SLP_REAL="$real" "$wrap" my --output standard --format tsv --file "$fixture") || {
  echo "slp-pg-wrap-check: wrap が失敗した" >&2
  exit 1
}

printf '%s\n' "$out"

fail=0
if ! printf '%s\n' "$out" | grep -F -q 'login_sessions s JOIN'; then
  echo "slp-pg-wrap-check: TSV に login_sessions s JOIN が無い" >&2
  fail=1
fi
if ! printf '%s\n' "$out" | grep -F -q 'posted_at DESC'; then
  echo "slp-pg-wrap-check: TSV に posted_at DESC が無い" >&2
  fail=1
fi
if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "slp-pg-wrap-check: ok"
