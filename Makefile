.PHONY: dev prod prod-worker worker build test test-race vet lint security generate css css-watch templ sqlc infra-up infra-down migrate-up migrate-down migrate-create seed

GOCACHE ?= /tmp/buildx-go-cache
GO := GOFLAGS=-buildvcs=false GOCACHE=$(GOCACHE) go

dev: templ css
	$(GO) run github.com/air-verse/air@v1.67.4 -c .air.toml
# Load local configuration only for this process, leaving the caller's shell unchanged.
worker:
	@set -a; . ./.env; set +a; $(GO) run ./cmd/worker
# Production uses the same entry points with validated production configuration.
prod: build
	@set -a; . ./.env; set +a; \
		test "$$APP_ENV" = "production" || { echo "make prod requires APP_ENV=production"; exit 1; }; \
		./bin/buildx
prod-worker: build
	@set -a; . ./.env; set +a; \
		test "$$APP_ENV" = "production" || { echo "make prod-worker requires APP_ENV=production"; exit 1; }; \
		./bin/buildx-worker
build: css
	$(GO) build -o bin/buildx ./cmd/server
	$(GO) build -o bin/buildx-worker ./cmd/worker
	$(GO) build -o bin/buildx-migrate ./cmd/migrate
test:
	$(GO) test ./...
test-race:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
lint: vet
	$(GO) run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
security:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
generate: css templ sqlc
css:
	pnpm run css
css-watch:
	pnpm run css:watch
templ:
	$(GO) run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
sqlc:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
infra-up:
	docker compose up -d mailpit
infra-down:
	docker compose down
migrate-up:
	@set -a; . ./.env; set +a; $(GO) run ./cmd/migrate up
# Roll back only the latest application migration; this removes its tables.
migrate-down:
	@set -a; . ./.env; set +a; $(GO) run ./cmd/migrate down
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=description" && exit 1)
	$(GO) run ./cmd/migrate create "$(name)"
seed:
	@set -a; . ./.env; set +a; $(GO) run ./cmd/migrate seed
