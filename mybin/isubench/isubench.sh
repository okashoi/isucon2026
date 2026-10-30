#!/bin/bash
# 公式ベンチの通信を録画し、変更したアプリに再生して「壊れていないか」と「軽くなったか」を見る。
# 問題ごとの設定は mybin/isubench/isubench.env に書く。手順は .agents/skills/isubench/SKILL.md。
#
#   isubench.sh record-start   nginx の向き先を録画用の中継に替える。このあと公式ベンチを1回回す
#   isubench.sh record-stop    nginx を元に戻し、録画を確定する (基準は消える)
#   isubench.sh baseline       今デプロイしているアプリを正常版として2回再生し、基準を作る (録画の先頭20秒)
#   isubench.sh check          今デプロイしているアプリを再生し、基準と比べる。壊れていたら exit 1 (20秒)
#   isubench.sh baseline-full  録画を全部再生して基準を作る (公式ベンチ1回分の時間 × 2)
#   isubench.sh check-full     録画を全部再生して比べる。後半の負荷でしか効かない改善も見える
#   isubench.sh status         録画中かどうかと、録画・基準の有無を出す
#   isubench.sh guess          isubench.env に書く値の候補を、このサーバーから探して出す
set -euo pipefail
cd "$(dirname "$0")"
SRC=$PWD
# shellcheck source=/dev/null
[ -f isubench.env ] && . ./isubench.env

if [ "${1:-}" = guess ]; then
  echo "== ISUBENCH_NGINX_CONF / ISUBENCH_UPSTREAM の候補 (proxy_pass と upstream の server)"
  sudo grep -rnE '^\s*(proxy_pass|server\s+[0-9a-z.]+:[0-9]+)' /etc/nginx/ 2>/dev/null | grep -v '#' || echo "  見つからない (nginx 以外のリバースプロキシかもしれない)"
  echo "== ISUBENCH_TARGET の候補 (nginx の listen)"
  sudo grep -rnE '^\s*listen' /etc/nginx/ 2>/dev/null | sort -u
  echo "== ISUBENCH_SERVICES の候補 (動いているサービス)"
  systemctl list-units --type=service --state=running --no-legend | awk '{print "  " $1}' | grep -vE 'systemd|ssh|cron|dbus|getty|snapd|polkit|rsyslog|udisks|multipathd|networkd|resolved|timesyncd|unattended|chrony|irqbalance|ModemManager|serial|user@|amazon|acpid|atd|fwupd|packagekit|qemu|lvm'
  exit 0
fi

DATA=${ISUBENCH_DATA:-$HOME/isubench}
NGINX_CONF=${ISUBENCH_NGINX_CONF:?isubench.env に ISUBENCH_NGINX_CONF を書く}
# sites-enabled はふつう sites-available へのリンク。リンクのまま書き換えると普通のファイルに置き換わるので実体を触る。
NGINX_CONF=$(readlink -f "$NGINX_CONF")
UPSTREAM=${ISUBENCH_UPSTREAM:?isubench.env に ISUBENCH_UPSTREAM (nginx の proxy_pass 先, 例 localhost:8080) を書く}
LISTEN=${ISUBENCH_LISTEN:-127.0.0.1:18080}
TIMEOUT=${ISUBENCH_RECORD_TIMEOUT:-600}
BIN=$DATA/isubench
mkdir -p "$DATA"

replay_args() {
  args=(-in "$DATA/record.jsonl" -target "${ISUBENCH_TARGET:-https://127.0.0.1}" -speed "${ISUBENCH_SPEED:-1}" -until "$1"
    -diff "$DATA/diff.txt" -services "${ISUBENCH_SERVICES:-}" -hosts "${ISUBENCH_HOSTS:-local}")
  [ -n "${ISUBENCH_HOST:-}" ] && args+=(-host "$ISUBENCH_HOST")
  [ -n "${ISUBENCH_STUB:-}" ] && args+=(-stub "$ISUBENCH_STUB")
  [ -n "${ISUBENCH_BARRIER:-}" ] && args+=(-barrier "$ISUBENCH_BARRIER")
  [ -n "${ISUBENCH_ID_REGEX:-}" ] && args+=(-id-regex "$ISUBENCH_ID_REGEX")
  for rw in ${ISUBENCH_REWRITE:-}; do args+=(-rewrite "$rw"); done
}

build() { (cd "$SRC" && go build -o "$BIN" .); }

restore_nginx() {
  if [ -f "$DATA/nginx.conf.orig" ]; then
    sudo cp "$DATA/nginx.conf.orig" "$NGINX_CONF"
    sudo rm -f "$DATA/nginx.conf.orig"
    sudo nginx -t -q && sudo systemctl reload nginx
    echo "nginx を元に戻しました"
  fi
}

case "${1:-}" in
record-start)
  [ -f "$DATA/nginx.conf.orig" ] && { echo "すでに録画中です。終わったら $0 record-stop"; exit 1; }
  grep -q "$UPSTREAM" "$NGINX_CONF" || { echo "$NGINX_CONF に $UPSTREAM がない。ISUBENCH_UPSTREAM を直す"; exit 1; }
  build
  rm -f "$DATA/record.tmp.jsonl"
  nohup "$BIN" record -listen "$LISTEN" -upstream "http://$UPSTREAM" -out "$DATA/record.tmp.jsonl" ${ISUBENCH_SKIP:+-skip "$ISUBENCH_SKIP"} > "$DATA/record.log" 2>&1 &
  echo $! > "$DATA/record.pid"
  sleep 1
  kill -0 "$(cat "$DATA/record.pid")" || { cat "$DATA/record.log"; exit 1; }
  sudo cp "$NGINX_CONF" "$DATA/nginx.conf.orig"
  sudo perl -pi -e "s/\\Q$UPSTREAM\\E/$LISTEN/g" "$NGINX_CONF"
  if ! sudo nginx -t -q; then restore_nginx; kill "$(cat "$DATA/record.pid")"; exit 1; fi
  sudo systemctl reload nginx
  # 戻し忘れると、録画を止めたあと nginx が 502 を返し続ける。時間が来たら自動で戻す。
  nohup bash -c "sleep $TIMEOUT; '$SRC/isubench.sh' record-stop" > "$DATA/watchdog.log" 2>&1 &
  echo $! > "$DATA/watchdog.pid"
  echo "録画を始めました。公式ベンチを1回回してから $0 record-stop を実行してください (${TIMEOUT}秒で自動停止)"
  ;;
record-stop)
  restore_nginx
  if [ -f "$DATA/watchdog.pid" ]; then
    wd=$(cat "$DATA/watchdog.pid")
    # 見張り役から呼ばれたときは、bash -c が exec するので自分自身が見張り役になっている。自分は止めない。
    if [ "$wd" != "$$" ] && [ "$wd" != "$PPID" ]; then kill "$wd" 2>/dev/null || true; fi
    rm -f "$DATA/watchdog.pid"
  fi
  if [ -f "$DATA/record.pid" ]; then
    pid=$(cat "$DATA/record.pid")
    kill -TERM "$pid" 2>/dev/null || true
    while kill -0 "$pid" 2>/dev/null; do sleep 0.5; done
    rm -f "$DATA/record.pid"
  fi
  [ -f "$DATA/record.tmp.jsonl" ] || { echo "録画ファイルがありません"; exit 1; }
  n=$(wc -l < "$DATA/record.tmp.jsonl")
  if [ "$n" -lt 100 ]; then
    echo "録画が $n 件しかないので採用しません (公式ベンチを回しましたか？)"
    exit 1
  fi
  [ -f "$DATA/record.jsonl" ] && mv "$DATA/record.jsonl" "$DATA/record.prev.jsonl"
  mv "$DATA/record.tmp.jsonl" "$DATA/record.jsonl"
  rm -f "$DATA/baseline.json" "$DATA/baseline-full.json"
  echo "initialize の本文 (外部サービスの URL があれば ISUBENCH_REWRITE で ISUBENCH_STUB に向ける):"
  grep -m1 -E "\"uri\":\"/api/initialize" "$DATA/record.jsonl" | grep -oE '"req_body":"[^"]*(\\.[^"]*)*"' || echo "  (見つからない)"
  echo "録画しました ($n 件)。基準を作り直すため、正常版のまま $0 baseline を実行してください"
  ;;
baseline | baseline-full)
  [ "$1" = baseline ] && { until=${ISUBENCH_UNTIL:-20s}; base=$DATA/baseline.json; } || { until=0; base=$DATA/baseline-full.json; }
  build
  replay_args "$until"
  rm -f "$base"
  for i in 1 2; do
    echo "== 正常版の再生 $i/2"
    "$BIN" replay "${args[@]}" -baseline "$base" | sed -n '/^wall_sec/,$p'
  done
  ;;
check | check-full)
  [ "$1" = check ] && { until=${ISUBENCH_UNTIL:-20s}; base=$DATA/baseline.json; } || { until=0; base=$DATA/baseline-full.json; }
  [ -f "$base" ] || { echo "基準がありません。正常版で $0 ${1/check/baseline} を先に"; exit 1; }
  build
  replay_args "$until"
  rc=0
  "$BIN" replay "${args[@]}" -out "$DATA/last.json" -against "$base" | sed -n '/^wall_sec/,$p' || rc=$?
  echo "(全エンドポイントの集計は $DATA/last.json、食い違いの例は $DATA/diff.txt)"
  exit $rc
  ;;
status)
  [ -f "$DATA/nginx.conf.orig" ] && echo "録画中" || echo "録画していない"
  [ -f "$DATA/record.jsonl" ] && echo "録画: $(wc -l < "$DATA/record.jsonl") 件 ($(date -r "$DATA/record.jsonl" '+%H:%M'))" || echo "録画: なし"
  for b in baseline baseline-full; do
    [ -f "$DATA/$b.json" ] && echo "$b: あり ($(date -r "$DATA/$b.json" '+%H:%M'))" || echo "$b: なし"
  done
  ;;
*)
  sed -n 2,12p "$0"
  exit 2
  ;;
esac
