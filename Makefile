APPNAME := isureal-go.service

.PHONY: *
gogo: stop-services build logs/clear start-services

stop-services:
	sudo systemctl stop nginx
	sudo systemctl stop $(APPNAME)
	# sudo systemctl stop postgresql
	ssh isucon-s4 "sudo systemctl stop postgresql"

build:
	cd go && make
	# cd go && make
	# cd go && go build -o isuhoge

logs: limit=10000
logs: opts=
logs:
	journalctl -ex --since "$(shell systemctl status $(APPNAME) | grep "Active:" | awk '{print $$6, $$7}')" -n $(limit) -q $(opts)

logs/error:
	$(MAKE) logs opts='--grep "(error|panic|- 500)" --no-pager'

logs/clear:
	sudo journalctl --rotate && sudo journalctl --vacuum-size=1K
	sudo truncate --size 0 /var/log/nginx/access.log
	sudo truncate --size 0 /var/log/nginx/error.log
	# sudo truncate --size 0 /var/log/mysql/mysql-slow.log && sudo chmod 666 /var/log/mysql/mysql-slow.log
	# sudo truncate --size 0 /var/log/mysql/error.log
	ssh isucon-s4 "sudo -u postgres truncate -s 0 /var/log/postgresql/postgresql-16-main.log"

start-services:
	sudo systemctl daemon-reload
	ssh isucon-s4 "sudo systemctl start postgresql"
	# sudo systemctl start postgresql
	sudo systemctl start $(APPNAME)
	sudo systemctl start nginx

etc-sync/pull:
	./mybin/etc-sync/etc-sync.sh pull

etc-sync/deploy:
	./mybin/etc-sync/etc-sync.sh deploy

etc-sync/deploy/apply:
	./mybin/etc-sync/etc-sync.sh deploy --apply

etc-sync/diff:
	./mybin/etc-sync/etc-sync.sh diff


# 録画・再生で変更を20秒前後で確かめる (.agents/skills/isubench/SKILL.md)
isubench/%:
	./mybin/isubench/isubench.sh $*
