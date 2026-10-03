.PHONY: dev prod build test test-race vet lint security generate css css-watch templ sqlc migrate-up migrate-down migrate-create seed

GOCACHE ?= /tmp/piko-go-cache
GO := GOFLAGS=-buildvcs=false GOCACHE=$(GOCACHE) go

dev: templ css
	$(GO) run github.com/air-verse/air@v1.67.4 -c .air.toml
# Production uses the same entry points with validated production configuration.
prod: build
	@set -a; . ./.env; set +a; \
		test "$$APP_ENV" = "production" || { echo "make prod requires APP_ENV=production"; exit 1; }; \
		./bin/piko
build: css
	$(GO) build -o bin/piko ./cmd/server
	$(GO) build -o bin/piko-migrate ./cmd/migrate
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
migrate-up:
	@set -a; . ./.env; set +a; $(GO) run ./cmd/migrate up
# Roll back only the latest migration; retired email/link data cannot be restored.
migrate-down:
	@set -a; . ./.env; set +a; $(GO) run ./cmd/migrate down
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=description" && exit 1)
	$(GO) run ./cmd/migrate create "$(name)"
seed:
	@set -a; . ./.env; set +a; $(GO) run ./cmd/migrate seed
