.PHONY: restart

restart:
	git pull origin main
	rm -rf bin/
	mkdir -p bin
	go build -o /home/stone/bin/stone-bot ./cmd
	systemctl restart stone
	systemctl status stone --no-pager -l
