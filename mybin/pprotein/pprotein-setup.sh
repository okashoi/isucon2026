#!/bin/sh -eu
#
# pprotein-setup.sh
#   競技サーバ / 手元から pprotein 計測準備をコマンドで行う。
#   AWS SG の追加・変更はしない（人が手動で 19000/19001 を許可する）。
#
# 使い方:
#   sudo ./mybin/pprotein/pprotein-setup.sh nginx on|off
#   sudo ./mybin/pprotein/pprotein-setup.sh mysql on|off
#   sudo ./mybin/pprotein/pprotein-setup.sh agent install
#   ./mybin/pprotein/pprotein-setup.sh targets --host s1=IP [--host s2=IP] [--app-pprof s1]
#   ./mybin/pprotein/pprotein-setup.sh alp --group '^/api/items/[^/]+$' [--group ...]
#   ./mybin/pprotein/pprotein-setup.sh alp --groups-file matching_groups.txt
#   ./mybin/pprotein/pprotein-setup.sh verify [IP...]
#
# 環境変数:
#   PPROTEIN_URL       (targets/alp 用, 例 http://x.x.x.x:9000)
#   PPROTEIN_HTTPLOG   (既定: /var/log/nginx/access.log)
#   PPROTEIN_SLOWLOG   (既定: /var/log/mysql/mysql-slow.log)
#   NGINX_CONF         (既定: /etc/nginx/nginx.conf)
#   MYSQL_CNF          (既定: /etc/mysql/mysql.conf.d/mysqld.cnf)
#   MYSQL_SERVICE      (既定: mysql)
#   DURATION           (targets Duration, 既定: 10)

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)

PPROTEIN_HTTPLOG=${PPROTEIN_HTTPLOG:-/var/log/nginx/access.log}
PPROTEIN_SLOWLOG=${PPROTEIN_SLOWLOG:-/var/log/mysql/mysql-slow.log}
NGINX_CONF=${NGINX_CONF:-/etc/nginx/nginx.conf}
MYSQL_CNF=${MYSQL_CNF:-/etc/mysql/mysql.conf.d/mysqld.cnf}
MYSQL_SERVICE=${MYSQL_SERVICE:-mysql}
DURATION=${DURATION:-10}

usage() {
  sed -n '3,18p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[ERROR] $1 が見つかりません" >&2
    exit 1
  fi
}

sg_reminder() {
  echo "[NOTE] AWS SG / クラウド FW は変更しません。pprotein から TCP 19000/19001 が未許可なら人が追加してください。" >&2
}

backup_once() {
  path=$1
  if [ ! -f "${path}.pprotein-setup.orig" ]; then
    sudo cp -p "$path" "${path}.pprotein-setup.orig"
  fi
}

# --- nginx (LTSV) -----------------------------------------------------------

cmd_nginx() {
  state=${1:-}
  case "$state" in
    on|off) ;;
    *) echo "使い方: $0 nginx on|off" >&2; exit 2 ;;
  esac
  need_cmd nginx
  if [ ! -f "$NGINX_CONF" ]; then
    echo "[ERROR] $NGINX_CONF がありません" >&2
    exit 1
  fi
  backup_once "$NGINX_CONF"
  tmp=$(mktemp)
  # shellcheck disable=SC2016
  # Keep the managed log_format block across on/off. Only rewrite access_log lines.
  awk -v state="$state" -v logpath="$PPROTEIN_HTTPLOG" '
    BEGIN { has_block=0; seen_path=0; inserted=0 }
    /^[[:space:]]*# BEGIN pprotein-setup ltsv/ { has_block=1; print; next }
    /^[[:space:]]*# END pprotein-setup ltsv/ { print; next }
    {
      line=$0
      if (line ~ /^[[:space:]]*access_log[[:space:]]+/) {
        if (index(line, logpath) || line ~ /access_log[[:space:]]+off;/ || !seen_path) {
          if (seen_path && !index(line, logpath) && line !~ /access_log[[:space:]]+off;/) {
            print line
            next
          }
          indent=line; sub(/[^[:space:]].*/, "", indent)
          if (state == "on") {
            print indent "access_log " logpath " ltsv;"
          } else {
            print indent "access_log off;"
          }
          seen_path=1
          next
        }
      }
      if (state == "on" && !has_block && !inserted && line ~ /^[[:space:]]*http[[:space:]]*\{/) {
        print line
        print "    # BEGIN pprotein-setup ltsv"
        print "    log_format ltsv \"time:$time_local\""
        print "                    \"\\thost:$remote_addr\""
        print "                    \"\\tforwardedfor:$http_x_forwarded_for\""
        print "                    \"\\treq:$request\""
        print "                    \"\\tstatus:$status\""
        print "                    \"\\tmethod:$request_method\""
        print "                    \"\\turi:$request_uri\""
        print "                    \"\\tsize:$body_bytes_sent\""
        print "                    \"\\treferer:$http_referer\""
        print "                    \"\\tua:$http_user_agent\""
        print "                    \"\\treqtime:$request_time\""
        print "                    \"\\tcache:$upstream_http_x_cache\""
        print "                    \"\\truntime:$upstream_http_x_runtime\""
        print "                    \"\\tapptime:$upstream_response_time\""
        print "                    \"\\tvhost:$host\";"
        print "    # END pprotein-setup ltsv"
        inserted=1
        has_block=1
        next
      }
      print line
    }
  ' "$NGINX_CONF" >"$tmp"

  if [ "$state" = "on" ] && ! grep -q 'BEGIN pprotein-setup ltsv' "$tmp"; then
    echo "[ERROR] http { ブロックが見つからず LTSV を挿入できませんでした" >&2
    rm -f "$tmp"
    exit 1
  fi

  sudo cp "$tmp" "$NGINX_CONF"
  rm -f "$tmp"
  sudo nginx -t
  sudo systemctl reload nginx
  echo "nginx: access_log $state (ltsv path=$PPROTEIN_HTTPLOG)"
}

# --- mysql ------------------------------------------------------------------

cmd_mysql() {
  state=${1:-}
  case "$state" in
    on|off) ;;
    *) echo "使い方: $0 mysql on|off" >&2; exit 2 ;;
  esac
  if [ ! -f "$MYSQL_CNF" ]; then
    echo "[ERROR] $MYSQL_CNF がありません" >&2
    exit 1
  fi
  backup_once "$MYSQL_CNF"
  onoff=ON
  if [ "$state" = "off" ]; then
    onoff=OFF
  fi
  tmp=$(mktemp)
  awk -v onoff="$onoff" -v logpath="$PPROTEIN_SLOWLOG" '
    BEGIN { in_mysqld=0; wrote=0 }
    /^\[mysqld\]/ {
      print
      in_mysqld=1
      if (!wrote) {
        print "# BEGIN pprotein-setup slowlog"
        print "slow_query_log = " onoff
        print "slow_query_log_file = " logpath
        print "long_query_time = 0"
        print "# END pprotein-setup slowlog"
        wrote=1
      }
      next
    }
    /^\[/ { in_mysqld=0 }
    /^[[:space:]]*# BEGIN pprotein-setup slowlog/ { skip=1; next }
    /^[[:space:]]*# END pprotein-setup slowlog/ { skip=0; next }
    skip { next }
    in_mysqld && $0 ~ /^[[:space:]#]*slow_query_log([[:space:]]|=)/ { next }
    in_mysqld && $0 ~ /^[[:space:]#]*slow_query_log_file([[:space:]]|=)/ { next }
    in_mysqld && $0 ~ /^[[:space:]#]*long_query_time([[:space:]]|=)/ { next }
    { print }
  ' "$MYSQL_CNF" >"$tmp"

  if ! grep -q 'BEGIN pprotein-setup slowlog' "$tmp"; then
    echo "[ERROR] [mysqld] セクションが見つかりません" >&2
    rm -f "$tmp"
    exit 1
  fi

  sudo cp "$tmp" "$MYSQL_CNF"
  rm -f "$tmp"
  if [ "$state" = "on" ]; then
    sudo touch "$PPROTEIN_SLOWLOG" || true
    sudo chmod 666 "$PPROTEIN_SLOWLOG" || true
  fi
  sudo systemctl restart "$MYSQL_SERVICE"
  echo "mysql: slow_query_log $onoff (file=$PPROTEIN_SLOWLOG)"
}

# --- agent ------------------------------------------------------------------

cmd_agent() {
  action=${1:-}
  case "$action" in
    install)
      need_cmd sudo
      export PPROTEIN_HTTPLOG PPROTEIN_SLOWLOG
      export PPROTEIN_GIT_REPOSITORY=${PPROTEIN_GIT_REPOSITORY:-$REPO_ROOT}
      if [ ! -f "$PPROTEIN_HTTPLOG" ]; then
        echo "[ERROR] $PPROTEIN_HTTPLOG がありません。先に: $0 nginx on" >&2
        exit 1
      fi
      if [ ! -f "$PPROTEIN_SLOWLOG" ]; then
        echo "[ERROR] $PPROTEIN_SLOWLOG がありません。先に: $0 mysql on" >&2
        exit 1
      fi
      DEBIAN_FRONTEND=noninteractive sudo -E "$SCRIPT_DIR/setup.sh" install
      ;;
    *)
      echo "使い方: $0 agent install" >&2
      exit 2
      ;;
  esac
}

# --- targets / alp (API) ----------------------------------------------------

need_pprotein_url() {
  if [ -z "${PPROTEIN_URL:-}" ]; then
    echo "[ERROR] PPROTEIN_URL を設定してください (例 http://x.x.x.x:9000)" >&2
    exit 1
  fi
}

cmd_targets() {
  need_pprotein_url
  need_cmd curl
  need_cmd python3
  sg_reminder

  hosts=""
  app_pprof=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --host)
        shift
        hosts="${hosts} ${1:-}"
        ;;
      --app-pprof)
        shift
        app_pprof="${app_pprof} ${1:-}"
        ;;
      *)
        echo "不明な引数: $1" >&2
        exit 2
        ;;
    esac
    shift
  done
  hosts=$(echo "$hosts" | sed 's/^ *//')
  if [ -z "$hosts" ]; then
    echo "使い方: $0 targets --host label=ip [--host label=ip] [--app-pprof label]" >&2
    exit 2
  fi

  body=$(HOSTS="$hosts" APP_PPROF="$app_pprof" DURATION="$DURATION" python3 - <<'PY'
import json, os
hosts = os.environ["HOSTS"].split()
app = set(os.environ.get("APP_PPROF", "").split())
duration = int(os.environ["DURATION"])
out = []
for spec in hosts:
    if "=" not in spec:
        raise SystemExit(f"bad --host {spec!r}, want label=ip")
    label, ip = spec.split("=", 1)
    pprof_port = 19001 if label in app else 19000
    if label not in app:
        print(f"[NOTE] {label}: app pprof なし → agent :19000 を pprof に使用", file=__import__("sys").stderr, flush=True)
    out.append({"Type": "pprof", "Label": label, "URL": f"http://{ip}:{pprof_port}/debug/pprof/profile", "Duration": duration})
    out.append({"Type": "httplog", "Label": label, "URL": f"http://{ip}:19000/debug/log/httplog", "Duration": duration})
    out.append({"Type": "slowlog", "Label": label, "URL": f"http://{ip}:19000/debug/log/slowlog", "Duration": duration})
print(json.dumps(out))
PY
)

  echo "$body"
  echo "[NOTE] POST /api/group/targets はリスト全体を置き換えます（UI で足した行も消えます）。" >&2
  curl -fsS -X POST \
    -H 'Content-Type: application/json' \
    --connect-timeout 3 --max-time 10 \
    -d "$body" \
    "${PPROTEIN_URL%/}/api/group/targets" >/dev/null
  echo "targets: posted to ${PPROTEIN_URL%/}/api/group/targets"
  echo "webhook: ${PPROTEIN_URL%/}/api/group/collect"
}

cmd_alp() {
  need_pprotein_url
  need_cmd curl
  need_cmd python3
  groups=""
  groups_file=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --group)
        shift
        groups="${groups}
${1:-}"
        ;;
      --groups-file)
        shift
        groups_file=${1:-}
        ;;
      *)
        echo "不明な引数: $1" >&2
        exit 2
        ;;
    esac
    shift
  done
  if [ -n "$groups_file" ]; then
    if [ ! -f "$groups_file" ]; then
      echo "[ERROR] $groups_file がありません" >&2
      exit 1
    fi
    groups=$(grep -v '^[[:space:]]*#' "$groups_file" | grep -v '^[[:space:]]*$' || true)
  fi
  groups=$(echo "$groups" | sed '/^$/d')
  if [ -z "$groups" ]; then
    echo "使い方: $0 alp --group REGEX [...] または --groups-file FILE" >&2
    echo "ルート抽出は人が行い、regex を渡す（AI に頼らない）。" >&2
    exit 2
  fi

  # pprotein v1.2.4: /api/httplog/config is raw YAML (not JSON). Preserve other top-level keys.
  current=$(curl -fsS --connect-timeout 3 --max-time 10 \
    "${PPROTEIN_URL%/}/api/httplog/config" || true)
  body=$(CURRENT="$current" GROUPS="$groups" python3 - <<'PY'
import os, re
groups = [g for g in os.environ["GROUPS"].splitlines() if g.strip()]
current = os.environ.get("CURRENT", "")
# Drop existing matching_groups key and its indented list / nested block.
stripped = re.sub(
    r'(?m)^matching_groups\s*:.*(?:\n(?:[ \t]+|\n).*)*',
    "",
    current,
).rstrip()
block = ["matching_groups:"]
for g in groups:
    # Quote if needed for YAML safety
    special = ":#{}[],&*!|>'\"" + "%@" + chr(96)
    if any(c in g for c in special) or g.startswith(("-", "?", ":")):
        q = "'" + g.replace("'", "''") + "'"
        block.append(f"  - {q}")
    else:
        block.append(f"  - {g}")
parts = [p for p in (stripped, "\n".join(block)) if p]
print("\n\n".join(parts) + "\n")
PY
)

  echo "$body"
  curl -fsS -X POST \
    -H 'Content-Type: application/yaml' \
    --connect-timeout 3 --max-time 10 \
    --data-binary "$body" \
    "${PPROTEIN_URL%/}/api/httplog/config" >/dev/null
  echo "alp: posted matching_groups (YAML) to ${PPROTEIN_URL%/}/api/httplog/config"
}

# --- verify -----------------------------------------------------------------

probe() {
  url=$1
  code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 3 --max-time 3 "$url" || echo 000)
  echo "$code  $url"
  [ "$code" = "200" ]
}

cmd_verify() {
  sg_reminder
  need_cmd curl
  ips=$*
  if [ -z "$ips" ]; then
    ips=127.0.0.1
  fi
  ok=0
  for ip in $ips; do
    probe "http://${ip}:19000/debug/pprof/" || ok=1
    probe "http://${ip}:19001/debug/pprof/" || true
  done
  if [ "$ok" -ne 0 ]; then
    echo "[ERROR] agent :19000 の疎通に失敗。SG / プロセスを確認してください。" >&2
    exit 1
  fi
  echo "verify: ok"
}

# --- main -------------------------------------------------------------------

cmd=${1:-}
if [ -n "$cmd" ]; then
  shift
fi

case "$cmd" in
  nginx)  cmd_nginx "$@" ;;
  mysql)  cmd_mysql "$@" ;;
  agent)  cmd_agent "$@" ;;
  targets) cmd_targets "$@" ;;
  alp)    cmd_alp "$@" ;;
  verify) cmd_verify "$@" ;;
  -h|--help|help|"") usage ;;
  *)
    echo "不明なコマンド: $cmd" >&2
    usage
    ;;
esac
