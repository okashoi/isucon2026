#!/bin/sh -eu
#
# replica-ensure.sh
#   レプリカ構成を冪等にそろえる。何度流しても安全（破壊的な取り直しはしない）。
#   data dir の作り直しが必要なら先に FORCE=1 make replica/basebackup/... を使う。
#
# 使い方:
#   ./mybin/postgres/replica-ensure.sh
#
# 手順:
#   1. europe1 / america1 が streaming でなければ basebackup（未構築時のみ）
#   2. 同期 ON
#   3. s2/s3 に ISUREAL_DB_READ_HOST（s1 からは除去）

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

./mybin/postgres/replica-basebackup.sh europe1
./mybin/postgres/replica-basebackup.sh america1
./mybin/postgres/replica-sync.sh on
./mybin/postgres/replica-read-host.sh on
./mybin/postgres/replica-sync.sh status
./mybin/postgres/replica-read-host.sh status

echo "[INFO] replica ensure done"
