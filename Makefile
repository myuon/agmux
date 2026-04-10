PORT ?= 4321

.PHONY: build build-frontend reload-frontend dev clean test install restart

build: build-frontend
	CGO_ENABLED=1 go build -ldflags "-X main.version=$$(git describe --tags --always 2>/dev/null || echo dev) -X main.commit=$$(git rev-parse --short HEAD 2>/dev/null || echo none) -X main.buildDate=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o agmux ./cmd/agmux

build-frontend:
	cd frontend && npm ci && npm run build

reload-frontend:
	cd frontend && npm run build

dev:
	cd frontend && npm run dev &
	go run ./cmd/agmux serve --dev

clean:
	rm -f agmux
	rm -rf frontend/dist

test:
	go test ./...

install: build-frontend
	CGO_ENABLED=1 go install -ldflags "-X main.version=$$(git describe --tags --always 2>/dev/null || echo dev) -X main.commit=$$(git rev-parse --short HEAD 2>/dev/null || echo none) -X main.buildDate=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./cmd/agmux

restart: install
	@launchctl kickstart -k "gui/$$(id -u)/com.myuon.agmux"
	@sleep 2
	@if lsof -ti :$(PORT) > /dev/null 2>&1; then echo "agmux restarted via launchd (pid $$(lsof -ti :$(PORT)))"; else echo "ERROR: agmux failed to start. Check ~/.agmux/agmux.log"; tail -20 ~/.agmux/agmux.log; exit 1; fi
