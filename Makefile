PORT ?= 4321

.PHONY: build build-frontend dev clean test install restart

build: build-frontend
	go build -o agmux ./cmd/agmux

build-frontend:
	cd frontend && npm ci && npm run build

dev:
	cd frontend && npm run dev &
	go run ./cmd/agmux serve --dev

clean:
	rm -f agmux
	rm -rf frontend/dist

test:
	go test ./...

install: build-frontend
	go install ./cmd/agmux

restart: install
	-lsof -ti :$(PORT) | xargs kill
	@echo "Waiting for port $(PORT) to be released..."
	@while lsof -ti :$(PORT) > /dev/null 2>&1; do sleep 1; done
	@nohup agmux serve --port $(PORT) > /tmp/agmux-$(PORT).log 2>&1 &
	@sleep 2
	@if lsof -ti :$(PORT) > /dev/null 2>&1; then echo "agmux restarted on port $(PORT) (pid $$(lsof -ti :$(PORT)))"; else echo "ERROR: agmux failed to start. Check /tmp/agmux-$(PORT).log"; cat /tmp/agmux-$(PORT).log; exit 1; fi
