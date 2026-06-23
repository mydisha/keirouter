# KeiRouter developer tasks.
#
# Common entrypoints:
#   make dev        run backend + frontend together (hot reload)
#   make backend    run only the Go backend
#   make frontend   run only the Vite dev server
#   make build      build backend binary + frontend assets
#   make test       run backend tests
#   make bootstrap  create an initial API key
#   make setup      one-command: install deps + run (no .env needed)

BACKEND_DIR := backend
FRONTEND_DIR := frontend
BIN := keirouter

# Headroom proxy settings for local dev.
# Keep Headroom opt-in so `make dev`/`make setup` remain the same lightweight
# backend + dashboard flow unless explicitly requested.
KEIROUTER_HEADROOM_AUTO ?= 0
HEADROOM_PORT ?= 8787
HEADROOM_IMAGE := ghcr.io/chopratejas/headroom:latest

# ANSI colors for terminal output.
C_RESET  := \033[0m
C_BOLD   := \033[1m
C_DIM    := \033[2m
C_GREEN  := \033[32m
C_YELLOW := \033[33m
C_CYAN   := \033[36m

.PHONY: dev backend frontend build build-backend build-frontend test vet hooks bootstrap install setup quickstart clean docker headroom

## headroom: start the optional Headroom compression proxy (Docker/native).
headroom:
	@if command -v docker >/dev/null 2>&1; then \
		printf "$(C_BOLD)$(C_CYAN)Starting Headroom$(C_RESET) proxy via Docker (:$(HEADROOM_PORT))...\n"; \
		docker run --rm --name keirouter-headroom \
			-p 127.0.0.1:$(HEADROOM_PORT):$(HEADROOM_PORT) \
			-e HEADROOM_HOST=0.0.0.0 \
			-e HEADROOM_PORT=$(HEADROOM_PORT) \
			-e HEADROOM_MODE=optimize \
			-e HEADROOM_TELEMETRY=off \
			$(HEADROOM_IMAGE) \
			--port $(HEADROOM_PORT) --host 0.0.0.0; \
	elif command -v headroom >/dev/null 2>&1; then \
		printf "$(C_BOLD)$(C_CYAN)Starting Headroom$(C_RESET) proxy (native :$(HEADROOM_PORT))...\n"; \
		headroom proxy --port $(HEADROOM_PORT) --host 127.0.0.1; \
	else \
		printf "$(C_YELLOW)Headroom unavailable: need Docker or headroom CLI. Install Docker or run: pip install 'headroom-ai[proxy]'$(C_RESET)\n"; \
		exit 1; \
	fi

## dev: run backend and frontend concurrently; Ctrl-C stops all.
##      The backend starts first; frontend waits until the backend is healthy
##      so the Vite proxy never hits ECONNREFUSED on cold start.
##      Set KEIROUTER_HEADROOM_AUTO=1 to also start Headroom.
dev:
	@printf "$(C_BOLD)$(C_CYAN)Starting KeiRouter$(C_RESET) backend (:20180) + dashboard (:5180)...\n"
	@trap 'kill 0; docker stop keirouter-headroom 2>/dev/null || true' INT TERM EXIT; \
	if [ "$(KEIROUTER_HEADROOM_AUTO)" = "1" ]; then \
		( $(MAKE) --no-print-directory headroom ) & \
	fi; \
	( cd $(BACKEND_DIR) && go run ./cmd/keirouter ) & \
	( \
		printf "$(C_DIM)Waiting for backend...$(C_RESET)\n"; \
		ready=0; \
		for i in $$(seq 1 30); do \
			if curl -sf http://127.0.0.1:20180/healthz >/dev/null 2>&1; then \
				printf "$(C_GREEN)$(C_BOLD)Backend ready$(C_RESET)\n"; \
				ready=1; \
				break; \
			fi; \
			sleep 0.5; \
		done; \
		if [ "$$ready" -ne 1 ]; then \
			printf "$(C_YELLOW)$(C_BOLD)Backend did not become healthy$(C_RESET) on http://127.0.0.1:20180/healthz\n"; \
			exit 1; \
		fi; \
		cd $(FRONTEND_DIR) && npm run dev \
	) & \
	wait

## backend: run only the Go backend.
backend:
	cd $(BACKEND_DIR) && go run ./cmd/keirouter

## frontend: run only the Vite dev server.
frontend:
	cd $(FRONTEND_DIR) && npm run dev

## install: install frontend dependencies and download Go modules.
install:
	cd $(FRONTEND_DIR) && npm install
	cd $(BACKEND_DIR) && go mod download

## build: build the backend binary and the frontend assets.
build: build-frontend build-backend

build-backend:
	cd $(BACKEND_DIR) && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o ../$(BIN) ./cmd/keirouter

build-frontend:
	cd $(FRONTEND_DIR) && npm run build

## hooks: install git pre-push hook (runs typecheck + vet before push).
hooks:
	@mkdir -p "$(shell git rev-parse --git-dir)/hooks"
	@cp scripts/hooks/pre-push "$(shell git rev-parse --git-dir)/hooks/pre-push"
	@chmod +x "$(shell git rev-parse --git-dir)/hooks/pre-push"
	@echo "pre-push hook installed"

## test: run the backend test suite.
test:
	cd $(BACKEND_DIR) && go test ./...

## vet: run static analysis.
vet:
	cd $(BACKEND_DIR) && go vet ./...

## bootstrap: create an initial API key (printed once).
bootstrap:
	cd $(BACKEND_DIR) && go run ./cmd/keirouter -bootstrap

## docker: build the production image.
docker:
	docker build -f deploy/Dockerfile -t keirouter:latest .

## clean: remove build artifacts.
clean:
	rm -f $(BIN)
	rm -rf $(FRONTEND_DIR)/dist
	docker stop keirouter-headroom 2>/dev/null || true

## setup: install deps (if needed) then start dev servers.
##        Zero config -- no .env required for local use.
##        Usage: make setup
setup:
	@printf "$(C_BOLD)$(C_CYAN)KeiRouter$(C_RESET) -- one-command setup\n"
	@test -d $(FRONTEND_DIR)/node_modules || ( printf "$(C_DIM)Installing frontend deps...$(C_RESET)\n" && cd $(FRONTEND_DIR) && npm ci --quiet )
	@cd $(BACKEND_DIR) && go mod download 2>/dev/null || true
	@echo ""
	@printf "$(C_GREEN)$(C_BOLD)Dependencies ready.$(C_RESET) Starting dev servers...\n"
	@echo "   Backend   -> http://localhost:20180"
	@echo "   Dashboard -> http://localhost:5180"
	@echo "   Headroom  -> optional; run 'make headroom' or 'KEIROUTER_HEADROOM_AUTO=1 make dev'"
	@echo "   Password  -> keirouter"
	@echo ""
	@$(MAKE) dev

## quickstart: alias for setup.
quickstart: setup