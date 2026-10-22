# SPDX-License-Identifier: AGPL-3.0-only
#
# FeatherDesk developer entry points (DEP-007). `make help` lists targets.
# No extra tools: Go, Node (corepack/pnpm) and Docker only.

SHELL := /bin/bash
.DEFAULT_GOAL := help

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Reproducible image timestamps (DEP-002): the last commit time.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
IMAGE     ?= featherdesk:local
PLATFORMS ?= linux/amd64,linux/arm64
LDFLAGS    = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
BIN       := bin/featherdesk
PNPM      := corepack pnpm
DEV_DATA  ?= $(CURDIR)/data/dev
DEV_PORT  ?= 8080
PG_PORT   ?= 55432
PG_NAME   := fd-test-postgres
# Postgres 18 for the second engine, tuned like the CI service container.
PG_IMAGE  := postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
TEST_PG_URL := postgres://st:st@127.0.0.1:$(PG_PORT)/fd_test?sslmode=disable

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ------------------------------------------------------------------ dev ----

.PHONY: dev dev-api dev-web dev-deps
dev: ## Go API (auto-restart on change, FD_DEV=true) + Vite on :5173 → open http://localhost:5173/agent
	@$(MAKE) -j2 --no-print-directory dev-api dev-web

dev-web: web/node_modules
	cd web && FD_DEV_BACKEND=http://localhost:$(DEV_PORT) $(PNPM) dev --port 5173 --strictPort

# Polling reload loop (no extra tool): rebuilds and restarts the server when a
# .go/.sql/.tmpl/openapi file changes; checks once per second.
dev-api:
	@mkdir -p bin $(DEV_DATA); stamp=bin/.dev-stamp; pid=; \
	trap 'kill $$pid 2>/dev/null; exit 0' INT TERM; \
	while true; do \
	  touch $$stamp; \
	  if go build -o bin/featherdesk-dev ./cmd/featherdesk; then \
	    FD_DEV=true FD_PUBLIC_URL=http://localhost:5173 FD_LISTEN_ADDR=127.0.0.1:$(DEV_PORT) \
	    FD_DATA_DIR=$(DEV_DATA) FD_LOG_FORMAT=text FD_INSECURE_COOKIES=true FD_AUTH_STAFF_LOCAL=true \
	      bin/featherdesk-dev serve & pid=$$!; \
	  else echo "build failed; waiting for changes"; pid=; fi; \
	  while sleep 1; do \
	    if [ -n "$$(find cmd internal api web/embed.go -newer $$stamp \( -name '*.go' -o -name '*.sql' -o -name '*.tmpl' -o -name '*.yaml' \) 2>/dev/null | head -1)" ]; then \
	      echo "change detected, restarting"; break; fi; \
	    if [ -n "$$pid" ] && ! kill -0 $$pid 2>/dev/null; then pid=; fi; \
	  done; \
	  [ -n "$$pid" ] && { kill $$pid; wait $$pid 2>/dev/null; }; \
	done

dev-deps: ## Start only Authentik + Mailpit (for make dev with SSO/email)
	docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d mailpit authentik-server authentik-worker

.PHONY: seed
seed: ## Seed the dev database with demo data (TICKETS=50)
	@mkdir -p $(DEV_DATA)
	FD_DEV=true FD_DATA_DIR=$(DEV_DATA) go run ./cmd/featherdesk seed --tickets $${TICKETS:-50}

.PHONY: demo
demo: ## Fill the dev database with demo data for every feature (wipes an earlier demo; DEMO_ARGS=--smtp-mailpit)
	@mkdir -p $(DEV_DATA)
	FD_DEV=true FD_PUBLIC_URL=http://localhost:5173 FD_DATA_DIR=$(DEV_DATA) FD_AUTH_STAFF_LOCAL=true \
	  go run ./cmd/featherdesk seed --demo --force $(DEMO_ARGS)

# ---------------------------------------------------------------- build ----

web/node_modules: web/package.json web/pnpm-lock.yaml
	cd web && $(PNPM) install --frozen-lockfile
	@touch $@

.PHONY: web
web: web/node_modules ## Build the SPA into web/dist (embedded by go:embed)
	cd web && $(PNPM) build

.PHONY: build
build: web ## Build bin/featherdesk (static, SPA embedded)
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/featherdesk
	@ls -l $(BIN)

.PHONY: image image-multiarch
image: ## Build the container image for this machine (IMAGE=featherdesk:local)
	docker buildx build --load --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
	  --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) -t $(IMAGE) .

image-multiarch: ## Build amd64+arm64 (needs a docker-container builder; result stays in the cache)
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
	  --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) -t $(IMAGE) .

# ----------------------------------------------------------------- test ----

.PHONY: test test-sqlite test-postgres pg-up pg-down
test: ## go test on SQLite AND Postgres 18 (starts a throwaway container) + web unit tests
	@$(MAKE) --no-print-directory pg-up
	@set -e; trap '$(MAKE) --no-print-directory pg-down' EXIT; \
	  FD_TEST_POSTGRES_URL=$${FD_TEST_POSTGRES_URL:-$(TEST_PG_URL)} go test -race -count=1 ./...
	cd web && $(PNPM) test

test-sqlite: ## go test, SQLite only (Postgres legs skip locally)
	go test -race -count=1 ./...

pg-up:
	@docker rm -f $(PG_NAME) >/dev/null 2>&1 || true
	@docker run -d --name $(PG_NAME) -p 127.0.0.1:$(PG_PORT):5432 -e POSTGRES_USER=st -e POSTGRES_PASSWORD=st -e POSTGRES_DB=fd_test \
	  --tmpfs /var/lib/postgresql $(PG_IMAGE) -c shared_buffers=32MB -c max_connections=60 -c work_mem=2MB -c fsync=off >/dev/null
	@for i in $$(seq 1 60); do docker exec $(PG_NAME) pg_isready -U st -d fd_test >/dev/null 2>&1 && exit 0; sleep 0.5; done; echo "postgres did not start" >&2; exit 1

pg-down:
	@docker rm -f $(PG_NAME) >/dev/null 2>&1 || true

.PHONY: e2e
e2e: web ## Playwright (chromium) against a fresh binary on a temp SQLite DB
	cd web && $(PNPM) exec playwright install chromium && $(PNPM) exec playwright test --project=chromium

# ----------------------------------------------------------------- lint ----

GOLANGCI_IMAGE := golangci/golangci-lint:v2.15.0

.PHONY: lint
lint: web/node_modules ## gofmt, go vet, golangci-lint, eslint, vue-tsc, SPDX headers, dependency gate
	@out=$$(gofmt -l cmd internal api tools web/embed.go); \
	  if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	@# Only issues new relative to main fail (66 pre-existing findings are debt, TD-DEP-01).
	@if command -v golangci-lint >/dev/null; then golangci-lint run --new-from-merge-base=$${LINT_BASE:-main} ./...; \
	  else docker run --rm -v $(CURDIR):/src -w /src $(GOLANGCI_IMAGE) golangci-lint run --new-from-merge-base=$${LINT_BASE:-main} ./...; fi
	cd web && $(PNPM) lint && $(PNPM) typecheck
	scripts/spdx.sh --check
	scripts/depgate.sh
	go test -count=1 -run 'TestDependencyGate' ./cmd/featherdesk/
	scripts/check-test-images.sh

.PHONY: vulncheck
vulncheck: ## govulncheck over every package (needs network for the vuln DB; CI hygiene job)
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

# ------------------------------------------------------------- measure -----

.PHONY: footprint loadtest k8s-validate proxy-test
footprint: ## Image size, idle RSS, idle CPU, cold start on the prod compose (IMAGE=…, ENGINE=sqlite|postgres)
	scripts/footprint.sh --image $(IMAGE) --engine $${ENGINE:-sqlite}

loadtest: ## 50 agents, 200 WS, 10 req/s against the prod compose (DURATION=10m)
	scripts/loadtest.sh --image $(IMAGE) --duration $${DURATION:-10m}

k8s-validate: ## helm lint/template + kubeconform on deploy/k8s and the chart
	scripts/k8s-validate.sh

proxy-test: ## Caddy, nginx, Traefik examples: TLS, Secure cookie, client IP, WebSocket
	scripts/proxy-test.sh --image $(IMAGE)

.PHONY: clean
clean: ## Remove build output and dev data
	rm -rf bin data/dev
