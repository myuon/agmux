PORT ?= 4321

.PHONY: build build-frontend dev clean test install restart preview preview-stop

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
	go install -a ./cmd/agmux

restart: install
	@launchctl kickstart -k "gui/$$(id -u)/com.myuon.agmux"
	@sleep 2
	@if lsof -ti :$(PORT) > /dev/null 2>&1; then echo "agmux restarted via launchd (pid $$(lsof -ti :$(PORT)))"; else echo "ERROR: agmux failed to start. Check ~/.agmux/agmux.log"; tail -20 ~/.agmux/agmux.log; exit 1; fi

preview:
ifndef PR
	$(error PR is required. Usage: make preview PR=<number>)
endif
	@WORKTREE_DIR=".worktrees/pr-$(PR)"; \
	if [ -d "$$WORKTREE_DIR" ]; then \
		echo "ERROR: Worktree $$WORKTREE_DIR already exists. Run 'make preview-stop PR=$(PR)' first."; \
		exit 1; \
	fi; \
	echo "Fetching PR #$(PR) branch..."; \
	BRANCH=$$(gh pr view $(PR) --json headRefName -q .headRefName) || exit 1; \
	git fetch origin "$$BRANCH" || exit 1; \
	echo "Creating worktree at $$WORKTREE_DIR..."; \
	git worktree add "$$WORKTREE_DIR" "origin/$$BRANCH" || exit 1; \
	echo "Building in worktree..."; \
	$(MAKE) -C "$$WORKTREE_DIR" build || exit 1; \
	PREVIEW_PORT=$$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()"); \
	echo "Starting server on port $$PREVIEW_PORT..."; \
	nohup "$$WORKTREE_DIR/agmux" serve --port "$$PREVIEW_PORT" > /tmp/agmux-preview-$(PR).log 2>&1 & \
	echo $$! > "$$WORKTREE_DIR.pid"; \
	echo "$$PREVIEW_PORT" > "$$WORKTREE_DIR.port"; \
	sleep 2; \
	if kill -0 $$(cat "$$WORKTREE_DIR.pid") 2>/dev/null; then \
		echo "Preview for PR #$(PR) running at http://localhost:$$PREVIEW_PORT"; \
	else \
		echo "ERROR: Server failed to start. Check /tmp/agmux-preview-$(PR).log"; \
		cat /tmp/agmux-preview-$(PR).log; \
		rm -f "$$WORKTREE_DIR.pid" "$$WORKTREE_DIR.port"; \
		git worktree remove "$$WORKTREE_DIR" --force; \
		exit 1; \
	fi

preview-stop:
ifndef PR
	$(error PR is required. Usage: make preview-stop PR=<number>)
endif
	@WORKTREE_DIR=".worktrees/pr-$(PR)"; \
	PID_FILE="$$WORKTREE_DIR.pid"; \
	PORT_FILE="$$WORKTREE_DIR.port"; \
	if [ ! -f "$$PID_FILE" ]; then \
		echo "ERROR: No preview found for PR #$(PR) ($$PID_FILE not found)"; \
		exit 1; \
	fi; \
	PID=$$(cat "$$PID_FILE"); \
	PREVIEW_PORT=$$(cat "$$PORT_FILE" 2>/dev/null); \
	echo "Stopping preview server (PID $$PID)..."; \
	kill "$$PID" 2>/dev/null || true; \
	if [ -n "$$PREVIEW_PORT" ]; then \
		echo "Waiting for port $$PREVIEW_PORT to be released..."; \
		while lsof -ti :"$$PREVIEW_PORT" > /dev/null 2>&1; do sleep 1; done; \
	fi; \
	echo "Removing worktree..."; \
	git worktree remove "$$WORKTREE_DIR" --force 2>/dev/null || true; \
	rm -f "$$PID_FILE" "$$PORT_FILE"; \
	echo "Preview for PR #$(PR) stopped."
