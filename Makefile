.PHONY: build build-frontend dev clean test

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
