.PHONY: restart

restart:
	git pull --ff-only
	go build -o /home/stone/bin/stone-bot ./cmd
	sudo systemctl restart stone
	sudo systemctl status stone --no-pager -l
