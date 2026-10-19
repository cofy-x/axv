PNPM ?= npx --yes pnpm@12.11.2
GO ?= go
E2E_ARGS ?=

.PHONY: help deps install dev build build-dashboard build-go test test-go typecheck format format-check lint lint-fix lint-fix-format test-e2e smoke-production verify-codex-live security audit-js audit-go clean

help:
	@echo "make deps | install | dev | build | test | typecheck | format | format-check | lint | lint-fix | lint-fix-format | test-e2e | smoke-production | verify-codex-live | security"

deps:
	$(PNPM) install --frozen-lockfile
	cd apps/server && GOWORK=off $(GO) mod download

install: deps
	$(MAKE) build
	node scripts/install.mjs

dev:
	$(PNPM) run dev

build:
	$(PNPM) run build:production

build-dashboard:
	$(PNPM) run build

build-go:
	GO="$(GO)" node scripts/build-go.mjs

test: test-go
	$(PNPM) run test

test-go:
	GO="$(GO)" node scripts/test-go.mjs

typecheck:
	$(PNPM) run typecheck

format:
	$(PNPM) run format

format-check:
	$(PNPM) run format:check

lint:
	$(PNPM) run lint

lint-fix:
	$(PNPM) run lint:fix

lint-fix-format:
	$(PNPM) run lint:fix:format

test-e2e:
	$(PNPM) exec playwright install chromium firefox webkit
	$(PNPM) run test:e2e $(E2E_ARGS)

smoke-production:
	$(PNPM) run smoke:production

verify-codex-live:
	$(PNPM) run verify:codex-live

security: audit-js audit-go

audit-js:
	$(PNPM) audit --audit-level=low

audit-go: build
	GO="$(GO)" node scripts/audit-go.mjs

clean:
	rm -rf dist apps/dashboard/dist apps/server/internal/webui/dist coverage test-results playwright-report
