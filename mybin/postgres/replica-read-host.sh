#!/bin/sh -eu
#
# replica-read-host.sh
#   s2/s3 の ISUREAL_DB_READ_HOST を冪等に ON/OFF する。
#   env を直したうえで、動いているアプリの environ と違うときだけ restart する。
#
# 使い方:
#   ./mybin/postgres/replica-read-host.sh on
#   ./mybin/postgres/replica-read-host.sh off
#   ./mybin/postgres/replica-read-host.sh status
#
# 環境変数:
#   STANDBY_APP_HOSTS (既定: isucon-s2 isucon-s3)
#   ASIA_APP_HOST     (既定: isucon-s1)  … READ_HOST を置かない
#   ENV_FILE          (既定: /home/isucon/env.sh)
#   APPNAME           (既定: isureal-go.service)
#   READ_HOST         (既定: 127.0.0.1)

STANDBY_APP_HOSTS=${STANDBY_APP_HOSTS:-isucon-s2 isucon-s3}
ASIA_APP_HOST=${ASIA_APP_HOST:-isucon-s1}
ENV_FILE=${ENV_FILE:-/home/isucon/env.sh}
APPNAME=${APPNAME:-isureal-go.service}
READ_HOST=${READ_HOST:-127.0.0.1}
ACTION=${1:-}

ensure_line() {
  host=$1
  want=$2 # empty = ensure absent
  ssh "$host" "bash -s" <<EOF
set -eu
envf='$ENV_FILE'
tmp=\$(mktemp)
# drop any READ_HOST lines (commented or not), keep the rest
grep -v -E '^[[:space:]]*#?[[:space:]]*ISUREAL_DB_READ_HOST=' "\$envf" > "\$tmp" || true
if [ -n '$want' ]; then
  printf '%s\n' 'ISUREAL_DB_READ_HOST=$want' >> "\$tmp"
fi
if cmp -s "\$tmp" "\$envf"; then
  rm -f "\$tmp"
  echo "[INFO] $host env unchanged"
else
  cat "\$tmp" > "\$envf"
  rm -f "\$tmp"
  echo "[INFO] $host env updated"
fi

want_env='$want'
pid=\$(systemctl show -p MainPID --value $APPNAME 2>/dev/null || echo 0)
if [ "\$pid" = "0" ] || [ -z "\$pid" ] || ! test -r /proc/\$pid/environ; then
  echo "[INFO] $host app not running; skip restart check"
  exit 0
fi
have=\$(tr '\\0' '\\n' < /proc/\$pid/environ | sed -n 's/^ISUREAL_DB_READ_HOST=//p' | head -n1 || true)
if [ "\$have" = "\$want_env" ]; then
  echo "[INFO] $host process already has READ_HOST=[\$have]"
  exit 0
fi
echo "[INFO] $host restart $APPNAME (process READ_HOST=[\$have] want=[\$want_env])"
sudo systemctl restart $APPNAME
EOF
}

show_one() {
  host=$1
  ssh "$host" "bash -s" <<EOF
set -eu
echo "--- $host ---"
grep -E '^ISUREAL_DB_(HOST|READ_HOST)=' '$ENV_FILE' || true
pid=\$(systemctl show -p MainPID --value $APPNAME 2>/dev/null || echo 0)
echo -n "service=\$(systemctl is-active $APPNAME 2>/dev/null || echo unknown) "
if [ "\$pid" != "0" ] && [ -n "\$pid" ] && test -r /proc/\$pid/environ; then
  tr '\\0' '\\n' < /proc/\$pid/environ | grep -E '^ISUREAL_DB_(HOST|READ_HOST)=' || true
else
  echo "(no process env)"
fi
EOF
}

case "$ACTION" in
  status)
    show_one "$ASIA_APP_HOST"
    for h in $STANDBY_APP_HOSTS; do
      show_one "$h"
    done
    ;;
  on)
    ensure_line "$ASIA_APP_HOST" ""
    for h in $STANDBY_APP_HOSTS; do
      ensure_line "$h" "$READ_HOST"
    done
    ;;
  off)
    ensure_line "$ASIA_APP_HOST" ""
    for h in $STANDBY_APP_HOSTS; do
      ensure_line "$h" ""
    done
    ;;
  *)
    echo "usage: $0 on|off|status" >&2
    exit 2
    ;;
esac
