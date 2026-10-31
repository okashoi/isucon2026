#!/bin/sh -eu
#
# replica-basebackup.sh
#   s4 primary から指定スタンバイへ pg_basebackup し、PostgreSQL を起動する。
#   既存の data dir は消す（main.bak も消してディスクを空ける）。
#
# 使い方:
#   ./mybin/postgres/replica-basebackup.sh europe1   # isucon-s2 / slot europe1
#   ./mybin/postgres/replica-basebackup.sh america1  # isucon-s3 / slot america1
#
# 前提:
#   - s4 で replicator 用 pg_hba がある（infra/s4/.../pg_hba.conf）
#   - s4 の PostgreSQL を止めない（make gogo と重ねない）
#
# 環境変数:
#   PRIMARY_HOST        (既定: 192.168.4.10)
#   PRIMARY_SSH         (既定: isucon-s4)
#   REPLICATOR_PASSWORD (既定: isureal)

PRIMARY_HOST=${PRIMARY_HOST:-192.168.4.10}
PRIMARY_SSH=${PRIMARY_SSH:-isucon-s4}
REPLICATOR_PASSWORD=${REPLICATOR_PASSWORD:-isureal}
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
    echo "usage: $0 europe1|america1" >&2
    exit 2
    ;;
esac

echo "[INFO] ensure replicator role and slot $SLOT on $PRIMARY_SSH"
ssh "$PRIMARY_SSH" "sudo -u postgres psql -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'replicator') THEN
    CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${REPLICATOR_PASSWORD}';
  END IF;
END\$\$;
SELECT CASE
  WHEN EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = '${SLOT}')
  THEN 'slot exists'
  ELSE pg_create_physical_replication_slot('${SLOT}')::text
END;
SQL"

echo "[INFO] basebackup $NAME -> $TARGET_HOST (slot=$SLOT)"
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
max_connections = 300
hot_standby = on
CONF
sudo systemctl start postgresql
sleep 2
sudo -u postgres psql -c "SELECT pg_is_in_recovery(); SELECT status, slot_name, sender_host FROM pg_stat_wal_receiver;"
EOF

echo "[INFO] primary view"
ssh "$PRIMARY_SSH" "sudo -u postgres psql -c \"SELECT application_name, client_addr, state, sync_state, replay_lag FROM pg_stat_replication ORDER BY 1;\""
