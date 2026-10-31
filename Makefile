# アプリ配布・再起動（issue #13）
#
# 前提: 競技ホスト上（または isucon-s* に ssh できるマシン）で実行する。
# 配布先: APP_HOSTS（asia1/europe1/america1 = s1/s2/s3）。s4 は共有 PostgreSQL primary。
# s2/s3 は同期スタンバイ。配置先: REMOTE_APP_BIN（isureal-go.service の ExecStart と一致）
#
#   make deploy   # ビルド → 各 APP_HOSTS へ rsync → systemctl restart（PG は触らない。チーム作業向き）
#   make gogo     # 全 APP 停止 → スタンバイ/primary 停止 → ビルド → 配布 → ログ消去 → PG/APP/nginx 起動
#                 # stop-the-world。チームのベンチと被せないこと。
#   make replica/ensure               # streaming / 同期 / READ_HOST を冪等にそろえる
#   make replica/basebackup/europe1   # 未構築時のみ basebackup（FORCE=1 で取り直し）
#   make replica/basebackup/america1
#   make replica/sync/on|off|status
#   make replica/read-host/on|off|status
#   make replica/status
#
APPNAME := isureal-go.service
APP_BIN := go/isureal
REMOTE_APP_BIN := /home/isucon/webapp/go/isureal
APP_HOSTS ?= isucon-s1 isucon-s2 isucon-s3
DB_HOST := isucon-s4
STANDBY_DB_HOSTS ?= isucon-s2 isucon-s3
# 競技サーバは linux/amd64。Mac から make deploy しても Exec format error にしない。
GOOS ?= linux
GOARCH ?= amd64

.PHONY: *
gogo: stop-services deploy/sync logs/clear start-services

stop-services:
	$(foreach h,$(APP_HOSTS),ssh $(h) "sudo systemctl stop nginx $(APPNAME)";)
	$(foreach h,$(STANDBY_DB_HOSTS),ssh $(h) "sudo systemctl stop postgresql";)
	ssh $(DB_HOST) "sudo systemctl stop postgresql"

build:
	cd go && GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o isureal cmd/isureal/main.go

# ビルドしてバイナリだけ配る（サービスは触らない）。gogo から呼ぶ。
deploy/sync: build
	$(foreach h,$(APP_HOSTS),rsync -az $(APP_BIN) $(h):$(REMOTE_APP_BIN);)

# ビルド → 配布 → 再起動（nginx / PostgreSQL は触らない）
# 実行中バイナリの ETXTBSY を避けるため .new 経由で差し替える。
deploy: build
	@for h in $(APP_HOSTS); do \
		rsync -az $(APP_BIN) $$h:$(REMOTE_APP_BIN).new && \
		ssh $$h "mv $(REMOTE_APP_BIN).new $(REMOTE_APP_BIN) && sudo systemctl restart $(APPNAME)"; \
	done

# 初期データの画像を asia1 で DB から書き出し、europe1 / america1 へ配る。画像の取得とエクスポートが使う。
# 初期化では書き出さないので、アプリの起動後、ベンチの前に実行する。足りないファイルだけを扱うので繰り返してよい。
POST_IMAGE_DIR := /home/isucon/webapp/post-images
POST_IMAGE_SOURCE := isucon-s1
POST_IMAGE_TARGETS := isucon-s2 isucon-s3
post-images/sync:
	ssh $(POST_IMAGE_SOURCE) "curl -fsS -X POST http://127.0.0.1:8080/internal/post-image-cache"
	$(foreach h,$(POST_IMAGE_TARGETS),ssh $(POST_IMAGE_SOURCE) "rsync -a --ignore-existing --exclude='*.tmp' $(POST_IMAGE_DIR)/ $(h):$(POST_IMAGE_DIR)/";)

logs: limit=10000
logs: opts=
logs:
	journalctl -ex --since "$(shell systemctl status $(APPNAME) | grep "Active:" | awk '{print $$6, $$7}')" -n $(limit) -q $(opts)

logs/error:
	$(MAKE) logs opts='--grep "(error|panic|- 500)" --no-pager'

logs/clear:
	$(foreach h,$(APP_HOSTS),ssh $(h) "sudo journalctl --rotate && sudo journalctl --vacuum-size=1K && sudo truncate --size 0 /var/log/nginx/access.log && sudo truncate --size 0 /var/log/nginx/error.log";)
	ssh $(DB_HOST) "sudo -u postgres truncate -s 0 /var/log/postgresql/postgresql-16-main.log"
	$(foreach h,$(STANDBY_DB_HOSTS),ssh $(h) "sudo -u postgres truncate -s 0 /var/log/postgresql/postgresql-16-main.log";)

start-services:
	ssh $(DB_HOST) "sudo systemctl start postgresql"
	$(foreach h,$(STANDBY_DB_HOSTS),ssh $(h) "sudo systemctl start postgresql";)
	$(foreach h,$(APP_HOSTS),ssh $(h) "sudo systemctl daemon-reload && sudo systemctl start $(APPNAME) && sudo systemctl start nginx";)

etc-sync/pull:
	./mybin/etc-sync/etc-sync.sh pull

etc-sync/deploy:
	./mybin/etc-sync/etc-sync.sh deploy

etc-sync/deploy/apply:
	./mybin/etc-sync/etc-sync.sh deploy --apply

etc-sync/diff:
	./mybin/etc-sync/etc-sync.sh diff

# レプリカ構成。何度流しても安全（basebackup の取り直しだけ FORCE=1）。
replica/ensure:
	./mybin/postgres/replica-ensure.sh

replica/basebackup/europe1:
	./mybin/postgres/replica-basebackup.sh europe1

replica/basebackup/america1:
	./mybin/postgres/replica-basebackup.sh america1

replica/sync/on:
	./mybin/postgres/replica-sync.sh on

replica/sync/off:
	./mybin/postgres/replica-sync.sh off

replica/sync/status:
	./mybin/postgres/replica-sync.sh status

replica/read-host/on:
	./mybin/postgres/replica-read-host.sh on

replica/read-host/off:
	./mybin/postgres/replica-read-host.sh off

replica/read-host/status:
	./mybin/postgres/replica-read-host.sh status

replica/status:
	ssh $(DB_HOST) "sudo -u postgres psql -c \"SELECT application_name, client_addr, state, sync_state, replay_lag FROM pg_stat_replication ORDER BY 1;\" -c \"SELECT slot_name, active, restart_lsn FROM pg_replication_slots;\""
	$(foreach h,$(STANDBY_DB_HOSTS),ssh $(h) "echo '--- $(h) ---'; sudo -u postgres psql -c \"SELECT pg_is_in_recovery(); SELECT status, slot_name, sender_host FROM pg_stat_wal_receiver;\" 2>&1 || echo 'pg down';")

# 録画・再生で変更を20秒前後で確かめる (.agents/skills/isubench/SKILL.md)
isubench/%:
	./mybin/isubench/isubench.sh $*
