#!/bin/sh -eu
#
# etc-sync.sh
#   conf.yaml に書いた複数サーバの /etc 設定を infra/<host>/etc に収集し、
#   必要に応じてリポジトリからサーバへ反映します。
# 使い方:
#   ./mybin/etc-sync/etc-sync.sh pull [host...]
#   ./mybin/etc-sync/etc-sync.sh diff [host...]
#   ./mybin/etc-sync/etc-sync.sh deploy [--apply] [host...]
#   ./mybin/etc-sync/etc-sync.sh hosts

REPO_ROOT=${ETC_SYNC_REPO_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}
CONFIG_FILE=${ETC_SYNC_CONFIG:-"${REPO_ROOT}/conf.yaml"}
INFRA_DIR=${ETC_SYNC_INFRA_DIR:-"${REPO_ROOT}/infra"}
RSYNC_PATH=${ETC_SYNC_RSYNC_PATH:-"sudo rsync"}

usage() {
  cat <<EOF
Usage:
  $0 pull [host...]
  $0 diff [host...]
  $0 deploy [--apply] [host...]
  $0 hosts

Options:
  --apply       Actually apply deploy changes. deploy is dry-run by default.
  --config FILE Use another conf.yaml (or set ETC_SYNC_CONFIG).
  --help        Show this help.

Examples:
  $0 pull
  $0 pull s1
  $0 diff s1 s2
  $0 deploy s1
  $0 deploy --apply s1
EOF
}

die() {
  echo "[ERROR] $*" >&2
  exit 1
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    die "$1 が見つかりません"
  fi
}

trim() {
  sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

unquote() {
  sed 's/^"//; s/"$//; s/^'\''//; s/'\''$//'
}

read_config() {
  [ -f "${CONFIG_FILE}" ] || die "設定ファイルが見つかりません: ${CONFIG_FILE}"

  awk '
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    /^[^[:space:]][^:]*:[[:space:]]*$/ {
      section=$1
      sub(/:$/, "", section)
      host=""
      next
    }
    section == "hosts" && /^[[:space:]]+[^:#][^:]*:[[:space:]]*[^[:space:]]/ {
      line=$0
      sub(/^[[:space:]]+/, "", line)
      key=line
      sub(/:.*/, "", key)
      value=line
      sub(/^[^:]*:[[:space:]]*/, "", value)
      print "host\t" key "\t" value
      next
    }
    (section == "includes" || section == "excludes") && /^[[:space:]]+-[[:space:]]+/ {
      value=$0
      sub(/^[[:space:]]+-[[:space:]]+/, "", value)
      print section "\t" value
      next
    }
    (section == "host_includes" || section == "host_excludes") && /^  [^:#][^:]*:[[:space:]]*$/ {
      host=$0
      sub(/^[[:space:]]+/, "", host)
      sub(/:[[:space:]]*$/, "", host)
      next
    }
    (section == "host_includes" || section == "host_excludes") && host != "" && /^    -[[:space:]]+/ {
      value=$0
      sub(/^[[:space:]]+-[[:space:]]+/, "", value)
      print section "\t" host "\t" value
      next
    }
  ' "${CONFIG_FILE}"
}

config_lines() {
  read_config | while IFS='	' read -r kind first second; do
    case "${kind}" in
      host)
        host=$(printf '%s' "${first}" | trim | unquote)
        ssh=$(printf '%s' "${second}" | trim | unquote)
        [ -n "${host}" ] || die "hosts に空のホスト名があります"
        [ -n "${ssh}" ] || die "hosts.${host} のSSH接続先が空です"
        printf 'host\t%s\t%s\n' "${host}" "${ssh}"
        ;;
      includes|excludes)
        value=$(printf '%s' "${first}" | trim | unquote)
        [ -n "${value}" ] || die "${kind} に空の値があります"
        printf '%s\t%s\n' "${kind}" "${value}"
        ;;
      host_includes|host_excludes)
        host=$(printf '%s' "${first}" | trim | unquote)
        value=$(printf '%s' "${second}" | trim | unquote)
        [ -n "${host}" ] || die "${kind} に空のホスト名があります"
        [ -n "${value}" ] || die "${kind}.${host} に空の値があります"
        printf '%s\t%s\t%s\n' "${kind}" "${host}" "${value}"
        ;;
    esac
  done
}

host_exists() {
  target=$1
  config_lines | awk -F '	' -v target="${target}" '$1 == "host" && $2 == target { found=1 } END { exit found ? 0 : 1 }'
}

all_hosts() {
  config_lines | awk -F '	' '$1 == "host" { print $2 }'
}

host_ssh() {
  target=$1
  config_lines | awk -F '	' -v target="${target}" '$1 == "host" && $2 == target { print $3; found=1 } END { exit found ? 0 : 1 }'
}

includes() {
  config_lines | awk -F '	' '$1 == "includes" { print $2 }'
}

host_specific_includes() {
  target=$1
  config_lines | awk -F '	' -v target="${target}" '$1 == "host_includes" && $2 == target { print $3 }'
}

includes_for_host() {
  host=$1
  {
    includes
    host_specific_includes "${host}"
  } | awk 'NF && !seen[$0]++ { print }'
}

excludes() {
  config_lines | awk -F '	' '$1 == "excludes" { print $2 }'
}

host_specific_excludes() {
  target=$1
  config_lines | awk -F '	' -v target="${target}" '$1 == "host_excludes" && $2 == target { print $3 }'
}

excludes_for_host() {
  host=$1
  {
    excludes
    host_specific_excludes "${host}"
  } | awk 'NF && !seen[$0]++ { print }'
}

select_hosts() {
  if [ "$#" -eq 0 ]; then
    all_hosts
    return
  fi

  for host in "$@"; do
    if ! host_exists "${host}"; then
      die "conf.yaml に未定義のホストです: ${host}"
    fi
    printf '%s\n' "${host}"
  done
}

ensure_config_has_required_values() {
  [ -n "$(all_hosts)" ] || die "conf.yaml の hosts が空です"
}

run_rsync() {
  echo "+ $*"
  "$@"
}

pull_host() {
  host=$1
  ssh=$(host_ssh "${host}")
  dest="${INFRA_DIR}/${host}"
  mkdir -p "${dest}"

  [ -n "$(includes_for_host "${host}")" ] || die "${host} の includes が空です"

  includes_for_host "${host}" | while IFS= read -r path; do
    [ -n "${path}" ] || continue
    set -- -a --no-owner --no-group "--rsync-path=${RSYNC_PATH}" -R --delete
    while IFS= read -r pattern; do
      [ -n "${pattern}" ] || continue
      set -- "$@" "--exclude=${pattern}"
    done <<EOF_EXCLUDES
$(excludes_for_host "${host}")
EOF_EXCLUDES
    set -- "$@" "${ssh}:${path}" "${dest}/"
    run_rsync rsync "$@"
  done
}

diff_host() {
  host=$1
  ssh=$(host_ssh "${host}")
  dest="${INFRA_DIR}/${host}"

  [ -n "$(includes_for_host "${host}")" ] || die "${host} の includes が空です"

  includes_for_host "${host}" | while IFS= read -r path; do
    [ -n "${path}" ] || continue
    set -- -a --no-owner --no-group "--rsync-path=${RSYNC_PATH}" -R -n -i
    while IFS= read -r pattern; do
      [ -n "${pattern}" ] || continue
      set -- "$@" "--exclude=${pattern}"
    done <<EOF_EXCLUDES
$(excludes_for_host "${host}")
EOF_EXCLUDES
    set -- "$@" "${ssh}:${path}" "${dest}/"
    run_rsync rsync "$@"
  done
}

deploy_host() {
  host=$1
  dry_run=$2
  ssh=$(host_ssh "${host}")

  [ -n "$(includes_for_host "${host}")" ] || die "${host} の includes が空です"

  includes_for_host "${host}" | while IFS= read -r path; do
    [ -n "${path}" ] || continue
    src="${INFRA_DIR}/${host}${path}"
    [ -e "${src}" ] || die "反映元がありません。先に pull してください: ${src}"

    if [ "${dry_run}" = "1" ]; then
      set -- -a --no-owner --no-group "--rsync-path=${RSYNC_PATH}" -n -i
    else
      set -- -a --no-owner --no-group "--rsync-path=${RSYNC_PATH}"
    fi
    while IFS= read -r pattern; do
      [ -n "${pattern}" ] || continue
      set -- "$@" "--exclude=${pattern}"
    done <<EOF_EXCLUDES
$(excludes_for_host "${host}")
EOF_EXCLUDES

    if [ -d "${src}" ]; then
      set -- "$@" --delete "${src}/" "${ssh}:${path}/"
    else
      set -- "$@" "${src}" "${ssh}:${path}"
    fi
    run_rsync rsync "$@"
  done
}

list_hosts() {
  config_lines | awk -F '	' '$1 == "host" { print $2 "\t" $3 }'
}

main() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --config)
        [ "$#" -ge 2 ] || die "--config にはファイルパスが必要です"
        CONFIG_FILE=$2
        shift 2
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        break
        ;;
    esac
  done

  command=${1:-}
  [ -n "${command}" ] || {
    usage
    exit 1
  }
  shift || true

  need_cmd awk
  need_cmd sed

  case "${command}" in
    pull)
      if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
        usage
        exit 0
      fi
      need_cmd rsync
      ensure_config_has_required_values
      select_hosts "$@" | while IFS= read -r host; do
        pull_host "${host}"
      done
      ;;
    diff)
      if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
        usage
        exit 0
      fi
      need_cmd rsync
      ensure_config_has_required_values
      select_hosts "$@" | while IFS= read -r host; do
        diff_host "${host}"
      done
      ;;
    deploy)
      if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
        usage
        exit 0
      fi
      need_cmd rsync
      dry_run=1
      if [ "${1:-}" = "--apply" ]; then
        dry_run=0
        shift
      fi
      ensure_config_has_required_values
      select_hosts "$@" | while IFS= read -r host; do
        deploy_host "${host}" "${dry_run}"
      done
      ;;
    hosts)
      ensure_config_has_required_values
      list_hosts
      ;;
    help)
      usage
      ;;
    *)
      usage >&2
      die "未知のサブコマンドです: ${command}"
      ;;
  esac
}

main "$@"
