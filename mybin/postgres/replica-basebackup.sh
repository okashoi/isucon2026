#!/bin/sh -eu
#
# replica-basebackup.sh
#   s4 primary から指定スタンバイへ pg_basebackup し、PostgreSQL を起動する。
#   既に正しい slot で streaming 中なら何もしない（冪等）。
#   取り直すときだけ FORCE=1 を付ける。
#
# 使い方:
#   ./mybin/postgres/replica-basebackup.sh europe1
#   ./mybin/postgres/replica-basebackup.sh america1
#   FORCE=1 ./mybin/postgres/replica-basebackup.sh europe1
#
# 前提:
#   - s4 で replicator 用 pg_hba がある（infra/s4/.../pg_hba.conf）
#   - s4 の PostgreSQL を止めない（make gogo と重ねない）
#
# 環境変数:
#   PRIMARY_HOST        (既定: 192.168.4.10)
#   PRIMARY_SSH         (既定: isucon-s4)
#   REPLICATOR_PASSWORD (既定: isureal)
#   FORCE               (既定: 空。1 なら data dir を消して取り直し)

PRIMARY_HOST=${PRIMARY_HOST:-192.168.4.10}
PRIMARY_SSH=${PRIMARY_SSH:-isucon-s4}
REPLICATOR_PASSWORD=${REPLICATOR_PASSWORD:-isureal}
FORCE=${FORCE:-}
NAME=${1:-}

case "$NAME" in
  europe1)
    TARGET_HOST=isucon-s2
    SLOT=europe1
    ;;
  america1)
    TARGET_HOST=isucon-s3
    SLOT=america1
    ;;
  *)
    echo "usage: FORCE=1 $0 europe1|america1" >&2
    exit 2
    ;;
esac

echo "[INFO] ensure replicator role and slot $SLOT on $PRIMARY_SSH"
# ローカル heredoc で渡す。$do$ を使い、リモート sh の $$=PID 展開を避ける。
ssh "$PRIMARY_SSH" sudo -u postgres psql -v ON_ERROR_STOP=1 <<EOF
DO \$do\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'replicator') THEN
    CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${REPLICATOR_PASSWORD}';
  END IF;
END
\$do\$;
SELECT CASE
  WHEN EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = '${SLOT}')
  THEN 'slot exists'
  ELSE pg_create_physical_replication_slot('${SLOT}')::text
END;
EOF

healthy=0
if [ -z "$FORCE" ]; then
  if ssh "$TARGET_HOST" "sudo -u postgres psql -tAc \"SELECT pg_is_in_recovery()\" 2>/dev/null" | grep -qx t \
    && ssh "$TARGET_HOST" "sudo -u postgres psql -tAc \"SELECT status||' '||COALESCE(slot_name,'') FROM pg_stat_wal_receiver\" 2>/dev/null" | grep -qx "streaming ${SLOT}" \
    && ssh "$PRIMARY_SSH" "sudo -u postgres psql -tAc \"SELECT state FROM pg_stat_replication WHERE application_name='${NAME}' ORDER BY 1 LIMIT 1\" 2>/dev/null" | grep -qx streaming
  then
    healthy=1
  fi
fi

if [ "$healthy" -eq 1 ]; then
  echo "[INFO] skip basebackup: $NAME already streaming via slot $SLOT (FORCE=1 to rebuild)"
  ssh "$PRIMARY_SSH" "sudo -u postgres psql -c \"SELECT application_name, client_addr, state, sync_state, replay_lag FROM pg_stat_replication WHERE application_name='${NAME}';\""
  exit 0
fi

if [ -n "$FORCE" ]; then
  echo "[INFO] FORCE=1: rebuilding $NAME"
else
  echo "[INFO] basebackup $NAME -> $TARGET_HOST (slot=$SLOT)"
fi

ssh "$TARGET_HOST" "bash -s" <<EOF
set -eu
sudo systemctl stop postgresql 2>/dev/null || true
sudo rm -rf /var/lib/postgresql/16/main.bak
sudo rm -rf /var/lib/postgresql/16/main
sudo -u postgres env PGPASSWORD='${REPLICATOR_PASSWORD}' pg_basebackup \\
  -h ${PRIMARY_HOST} -U replicator \\
  -D /var/lib/postgresql/16/main \\
  -X stream -R -S ${SLOT} -P \\
  -d "application_name=${NAME}"
sudo tee /etc/postgresql/16/main/conf.d/isucon.conf >/dev/null <<CONF
listen_addresses = '*'
max_connections = 600
hot_standby = on
CONF
sudo systemctl enable postgresql >/dev/null 2>&1 || true
sudo systemctl start postgresql
sleep 2
sudo -u postgres psql -c "SELECT pg_is_in_recovery(); SELECT status, slot_name, sender_host FROM pg_stat_wal_receiver;"
EOF

echo "[INFO] primary view"
ssh "$PRIMARY_SSH" "sudo -u postgres psql -c \"SELECT application_name, client_addr, state, sync_state, replay_lag FROM pg_stat_replication ORDER BY 1;\""
