# hi:begin: written by hi init; change it in the template, not here
.DEFAULT_GOAL := help
.PHONY: help check fix lint test

help: ## Show the targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

check: ## Packages and their advisories, format, lint, types, tests, and build; must pass before a change is done
	@test ! -e package-lock.json || { echo "package-lock.json: this project uses Bun. Run bun install, which turns it into bun.lock, then delete it."; exit 1; }
	bun install --frozen-lockfile --silent
	bun audit --audit-level=high
	bun run biome ci .
	bun check
	bun test
	bun run build

fix: ## Format and fix what Biome can
	bun run biome check --write .

lint: ## Format check and lint only
	bun run biome ci .

test: ## Tests only
	bun test
# hi:end

# Project targets below.
.PHONY: run dev

run: ## Serve on 0.0.0.0:$PORT (3000 by default), as previews and production do
	bun install --frozen-lockfile --silent
	NODE_ENV=production bun src/server.ts

dev: ## Serve with hot reload
	bun --hot src/server.ts
