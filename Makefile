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
	-lsof -ti :4321 | xargs kill
	@sleep 1
	@nohup agmux serve > /dev/null 2>&1 &
	@echo "agmux restarted"
