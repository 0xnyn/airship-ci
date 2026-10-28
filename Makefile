# Airship's front door for local development. Every target wraps a pnpm
# script, a Go command or Docker, so `pnpm <script>` keeps working the same
# way. Works with the GNU Make 3.81 that ships with macOS.
#
#   make setup   once, after cloning
#   make dev     every day
#   make help    everything else
#
# The guide is docs/deploy/development/.

SHELL := /bin/bash
.DEFAULT_GOAL := help

API_DIR := apps/api
GO_TOOL := go tool -modfile=tools/go.mod

# The local Postgres from docker-compose.yml. Tests create one database per
# test on this server. Override on the command line or in the shell.
TEST_DATABASE_URL ?= postgres://airship:airship@localhost:5432/airship?sslmode=disable
export TEST_DATABASE_URL

# A throwaway Postgres 17 for `make test-pg17` (hosted Supabase runs 17).
PG17_CONTAINER := airship-pg17-test
PG17_PORT ?= 55417

# Local Supabase (`make supabase-up`), the closest match to the pilot's
# database: docs/deploy/stage-0/local.md. A `supabase` on PATH wins over
# the one pnpm fetches on demand.
SUPABASE ?= $(shell command -v supabase 2>/dev/null || echo "pnpm dlx supabase")
SUPABASE_DATABASE_URL ?= postgres://airship:airship@127.0.0.1:54322/postgres?sslmode=disable

# moto, the AWS emulator, for `make test-aws-emulator`.
MOTO_PORT ?= 5055

# Docker images for `make images`.
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
NEXT_PUBLIC_API_URL ?=
PLATFORM ?= linux/amd64

##@ Getting started

.PHONY: help
help: ## Show this list
	@awk 'BEGIN {FS = ":.*## "} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5); next} \
		/^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo

.PHONY: doctor
doctor: ## Check that Go, Node, pnpm and Docker are installed and recent enough
	@ok=1; \
	check() { if command -v "$$1" >/dev/null 2>&1; then printf "  %-8s %s\n" "$$1" "$$($$2 2>&1 | head -1)"; else printf "  %-8s \033[31mmissing\033[0m (%s)\n" "$$1" "$$3"; ok=0; fi; }; \
	check go "go -C $(API_DIR) version" "any recent Go; it fetches the go.mod toolchain itself"; \
	check node "node --version" "24+, see .node-version"; \
	check pnpm "pnpm --version" "corepack enable, or npm i -g pnpm@11.22.0"; \
	check docker "docker --version" "Docker Desktop, OrbStack or Colima"; \
	if command -v docker >/dev/null 2>&1 && ! docker info >/dev/null 2>&1; then echo "  docker   installed, but the daemon is not running"; ok=0; fi; \
	if command -v node >/dev/null 2>&1; then major=$$(node -p 'process.versions.node.split(".")[0]'); [ "$$major" -ge 24 ] || { echo "  node     $$major is too old: need 24+"; ok=0; }; fi; \
	echo "  optional: uvx (moto, AWS emulator), cloudflared or ngrok (webhook tunnel), supabase (local Postgres 17)"; \
	[ $$ok = 1 ] && echo "All required tools found." || { echo "Install the missing tools above, then rerun make doctor."; exit 1; }

.PHONY: setup
setup: doctor install env db-up db-migrate ## First run: install, create env files, start and migrate Postgres
	@echo
	@echo "Ready. Run 'make dev', then open http://localhost:3000 and choose Development sign-in."

.PHONY: install
install: ## Install dependencies (also generates apps/dash/src/env.ts)
	pnpm install

.PHONY: env
env: ## Create the local env files from their examples (never overwrites)
	@for pair in "apps/api/.env.example:apps/api/.env" "apps/dash/.env.example:apps/dash/.env.local" "apps/home/.env.example:apps/home/.env"; do \
		src=$${pair%%:*}; dst=$${pair##*:}; \
		if [ -f "$$dst" ]; then echo "  keep    $$dst (already exists)"; \
		else cp "$$src" "$$dst" && echo "  create  $$dst"; fi; \
	done

##@ Running

.PHONY: dev
# The docs site is deliberately not part of this: Mintlify's preview is slow
# to start and rarely wanted alongside the API. `make docs` runs it on :4100.
dev: ## Run the API (:8080), dashboard (:3000) and marketing site (:4000) with live type regeneration
	pnpm dev

.PHONY: dev-api
dev-api: ## Run only the API, rebuilt on every save
	pnpm turbo run dev --filter=@airship/api --filter=@airship/client

.PHONY: dev-dash
dev-dash: ## Run only the dashboard (expects an API on NEXT_PUBLIC_API_URL)
	pnpm turbo run dev --filter=@airship/dash

.PHONY: dev-home
dev-home: ## Run only the marketing site
	pnpm --filter @airship/home dev

.PHONY: dev-docs
dev-docs: ## Run only the documentation site (Mintlify preview)
	pnpm --filter @airship/docs dev

.PHONY: tunnel
tunnel: ## Expose the API on a public https URL for GitHub webhooks (cloudflared quick tunnel)
	cloudflared tunnel --url http://localhost:8080

.PHONY: cacheproxy
cacheproxy: ## Build the runner cache proxy into the API (needed for cache: true while testing)
	pnpm --filter @airship/api build:cacheproxy

##@ Database

.PHONY: db-up
db-up: ## Start Postgres in Docker on :5432
	pnpm db:up

.PHONY: db-down
db-down: ## Stop Postgres, keeping its data
	pnpm db:down

.PHONY: db-migrate
db-migrate: ## Apply migrations
	pnpm db:migrate

.PHONY: db-rollback
db-rollback: ## Undo the last migration
	pnpm db:rollback

.PHONY: db-status
db-status: ## Show which migrations are applied
	pnpm --filter @airship/api db:status

.PHONY: db-new
db-new: ## Create a migration: make db-new NAME=add_thing
	@[ -n "$(NAME)" ] || { echo "usage: make db-new NAME=add_thing"; exit 1; }
	pnpm db:new $(NAME) sql

.PHONY: db-psql
db-psql: ## Open psql on the local database
	docker compose exec postgres psql -U airship -d airship

.PHONY: db-reset
db-reset: ## Delete the local database and start over empty (asks first)
	@read -r -p "Delete the local Postgres volume and every row in it? [y/N] " answer; [ "$$answer" = y ] || { echo "Cancelled."; exit 1; }
	docker compose down --volumes
	$(MAKE) db-up db-migrate

.PHONY: supabase-up
supabase-up: ## Start a local Supabase Postgres 17 on :54322 and migrate it
	$(SUPABASE) start
	cd $(API_DIR) && DATABASE_URL='$(SUPABASE_DATABASE_URL)' go run ./cmd/migrate up
	@echo
	@echo "Postgres 17 on :54322, the version hosted Supabase runs. In apps/api/.env:"
	@echo "  DATABASE_URL=$(SUPABASE_DATABASE_URL)"

.PHONY: supabase-down
supabase-down: ## Stop local Supabase, keeping its data
	$(SUPABASE) stop

.PHONY: supabase-reset
supabase-reset: ## Recreate the local Supabase database, empty, and migrate it
	$(SUPABASE) db reset
	cd $(API_DIR) && DATABASE_URL='$(SUPABASE_DATABASE_URL)' go run ./cmd/migrate up

.PHONY: supabase-psql
supabase-psql: ## Open psql on the local Supabase database
	@if command -v psql >/dev/null 2>&1; then psql '$(SUPABASE_DATABASE_URL)'; \
	else docker exec -it supabase_db_airship psql -U airship -d postgres; fi

##@ Code generation

.PHONY: generate
generate: ## sqlc, then openapi.json, then the TypeScript client and the docs' API spec
	pnpm generate

.PHONY: check-generated
check-generated: ## Fail if a generated file is stale
	pnpm check:generated

##@ Quality

.PHONY: lint
lint: ## Biome (TypeScript, JSON, CSS, Markdown) and golangci-lint (Go)
	pnpm lint

.PHONY: format
format: ## Fix formatting and auto-fixable lint everywhere
	pnpm format

.PHONY: typecheck
typecheck: ## tsc and go vet across every package
	pnpm typecheck

.PHONY: check
check: ## Run before pushing: lint, typecheck, test, generated files
	pnpm check

##@ Tests

.PHONY: test
test: ## Every package's tests, against the local Postgres (make db-up first)
	pnpm test

.PHONY: test-go
test-go: ## Only the API's Go tests, with the race detector
	cd $(API_DIR) && go test -race ./...

.PHONY: test-ts
test-ts: ## Only the TypeScript tests
	pnpm turbo run test --filter=@airship/dash --filter=@airship/client

.PHONY: test-pg17
test-pg17: ## The API's Go tests on a throwaway Postgres 17, the version hosted Supabase runs
	@docker rm -f $(PG17_CONTAINER) >/dev/null 2>&1 || true
	docker run -d --rm --name $(PG17_CONTAINER) -e POSTGRES_PASSWORD=postgres -p $(PG17_PORT):5432 postgres:17-alpine >/dev/null
	@until docker exec $(PG17_CONTAINER) pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done; sleep 1
	cd $(API_DIR) && TEST_DATABASE_URL='postgres://postgres:postgres@localhost:$(PG17_PORT)/postgres?sslmode=disable' go test ./...; \
		status=$$?; docker rm -f $(PG17_CONTAINER) >/dev/null; exit $$status

.PHONY: test-aws-emulator
test-aws-emulator: ## The EC2 provider against moto (needs uvx)
	@cd $(API_DIR) && { uvx --from 'moto[server]' moto_server -p $(MOTO_PORT) >/dev/null 2>&1 & moto=$$!; \
		trap 'kill $$moto 2>/dev/null' EXIT; \
		until curl -fs http://localhost:$(MOTO_PORT)/moto-api/ >/dev/null 2>&1; do sleep 1; done; \
		AIRSHIP_AWS_ENDPOINT=http://localhost:$(MOTO_PORT) go test -v -run TestAgainstEmulator ./internal/cloud/aws; }

.PHONY: test-live
test-live: ## A live cloud suite, which costs money: make test-live CLOUD=aws|gcp|azure|daytona (reads apps/api/.env.live)
	@case "$(CLOUD)" in aws) t=AWS;; gcp) t=GCP;; azure) t=Azure;; daytona) t=Daytona;; *) echo "usage: make test-live CLOUD=aws|gcp|azure|daytona (variables: docs/deploy/development/7-tests.md)"; exit 1;; esac; \
	cd $(API_DIR) && { if [ -f .env.live ]; then set -a; . ./.env.live; set +a; else echo "no apps/api/.env.live: using the shell's AIRSHIP_LIVE_* variables (template: apps/api/.env.live.example)"; fi; \
		go test -v -count=1 -run "TestAgainstLive$$t" ./internal/cloud/$(CLOUD); }

##@ Documentation

.PHONY: docs
docs: ## Preview the documentation site on :4100
	pnpm --filter @airship/docs dev

.PHONY: docs-check
docs-check: ## Check the docs: navigation, links, frontmatter and names
	pnpm --filter @airship/docs lint

.PHONY: docs-validate
docs-validate: ## Mintlify's own check: content, OpenAPI spec, strict build
	pnpm --filter @airship/docs build

.PHONY: docs-links
docs-links: ## Check every link in the docs, external ones included (needs the network)
	pnpm --filter @airship/docs links

##@ Building

.PHONY: build
build: ## Build every package (API binaries in apps/api/bin, Next.js, Astro)
	pnpm build

.PHONY: images
images: ## Production images: make images NEXT_PUBLIC_API_URL=https://api.example.com
	@[ -n "$(NEXT_PUBLIC_API_URL)" ] || { echo "usage: make images NEXT_PUBLIC_API_URL=https://api.example.com [VERSION=...] [PLATFORM=...]"; exit 1; }
	docker build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -t airship-api:$(VERSION) $(API_DIR)
	docker build --platform $(PLATFORM) -f apps/dash/Dockerfile --build-arg NEXT_PUBLIC_API_URL=$(NEXT_PUBLIC_API_URL) -t airship-dash:$(VERSION) .

.PHONY: og
og: ## Re-render the marketing site's social image and video (Playwright + ffmpeg)
	pnpm --filter @airship/home og

.PHONY: clean
clean: ## Remove build output and caches (keeps node_modules, env files and the database)
	rm -rf .turbo apps/*/.turbo packages/*/.turbo apps/dash/.next apps/home/dist apps/home/.astro apps/docs/.mintlify $(API_DIR)/bin $(API_DIR)/tmp
