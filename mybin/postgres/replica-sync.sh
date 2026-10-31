#!/bin/sh -eu
#
# replica-sync.sh
#   s4 の同期レプリカ設定を冪等に ON/OFF する。
#   ON は両スタンバイが streaming のときだけ。既に同じ設定なら reload のみ省略可。
#
# 使い方:
#   ./mybin/postgres/replica-sync.sh on
#   ./mybin/postgres/replica-sync.sh off
#   ./mybin/postgres/replica-sync.sh status
#
# 環境変数:
#   PRIMARY_SSH (既定: isucon-s4)
#   REPO_SYNC_EXAMPLE (既定: このリポジトリの isucon-sync.conf.example)

PRIMARY_SSH=${PRIMARY_SSH:-isucon-s4}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
REPO_SYNC_EXAMPLE=${REPO_SYNC_EXAMPLE:-$ROOT/infra/s4/etc/postgresql/16/main/conf.d/isucon-sync.conf.example}
REMOTE_SYNC=/etc/postgresql/16/main/conf.d/isucon-sync.conf
ACTION=${1:-}

streaming_count() {
  ssh "$PRIMARY_SSH" "sudo -u postgres psql -tAc \"SELECT count(*) FROM pg_stat_replication WHERE application_name IN ('europe1','america1') AND state='streaming'\"" | tr -d '[:space:]'
}

show_status() {
  ssh "$PRIMARY_SSH" "sudo -u postgres psql -c \"SHOW synchronous_standby_names;\" -c \"SHOW synchronous_commit;\" -c \"SELECT application_name, state, sync_state FROM pg_stat_replication ORDER BY 1;\""
  ssh "$PRIMARY_SSH" "if test -f $REMOTE_SYNC; then echo '[INFO] $REMOTE_SYNC present'; else echo '[INFO] $REMOTE_SYNC absent'; fi"
}

case "$ACTION" in
  status)
    show_status
    ;;
  off)
    ssh "$PRIMARY_SSH" "bash -s" <<EOF
set -eu
if test -f $REMOTE_SYNC; then
  sudo rm -f $REMOTE_SYNC
  sudo -u postgres psql -c "SELECT pg_reload_conf();" >/dev/null
  echo "[INFO] removed $REMOTE_SYNC and reloaded"
else
  echo "[INFO] already off ($REMOTE_SYNC absent)"
fi
EOF
    show_status
    ;;
  on)
    count=$(streaming_count)
    if [ "$count" != "2" ]; then
      echo "[ERROR] need europe1 and america1 streaming before sync on (now=$count)" >&2
      show_status
      exit 1
    fi
    if ! test -f "$REPO_SYNC_EXAMPLE"; then
      echo "[ERROR] missing $REPO_SYNC_EXAMPLE" >&2
      exit 1
    fi
    scp -q "$REPO_SYNC_EXAMPLE" "$PRIMARY_SSH:/tmp/isucon-sync.conf"
    ssh "$PRIMARY_SSH" "bash -s" <<EOF
set -eu
desired=/tmp/isucon-sync.conf
target=$REMOTE_SYNC
if test -f "\$target" && cmp -s "\$desired" "\$target"; then
  echo "[INFO] sync conf already applied"
else
  sudo cp "\$desired" "\$target"
  sudo chmod 644 "\$target"
  sudo -u postgres psql -c "SELECT pg_reload_conf();" >/dev/null
  echo "[INFO] installed \$target and reloaded"
fi
EOF
    show_status
    names=$(ssh "$PRIMARY_SSH" "sudo -u postgres psql -tAc \"SHOW synchronous_standby_names\"" | tr -d '[:space:]')
    case "$names" in
      *europe1*america1*|*america1*europe1*) ;;
      *)
        echo "[ERROR] synchronous_standby_names not applied: [$names]" >&2
        exit 1
        ;;
    esac
    ;;
  *)
    echo "usage: $0 on|off|status" >&2
    exit 2
    ;;
esac
