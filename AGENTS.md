# Repository Guidelines

## Structure and boundaries

`cmd/server` is the HTTP entry point and `internal/app` composes concrete dependencies. Keep each future feature cohesive under `internal/<feature>` with handlers, services, repositories, and templ views. Shared HTTP concerns live in `internal/web`; PostgreSQL, Redis, logging, and generated sqlc adapters live in `internal/platform`. Account email delivery and cleanup use `internal/jobs`, `internal/worker`, and `internal/mail`.

Keep HTTP parsing in handlers, business rules and transaction boundaries in services, and SQL in named statements under `db/queries`. PostgreSQL is the source of truth; Redis is for rate limiting/cache. Edit `.templ` and SQL sources, then regenerate their Go output. Keep static assets in `static/` and CSS sources in `assets/`. See `README.md` for development/production setup and auth invariants.

## Development and validation

Use `make dev` and `make worker` for development; `make prod` and `make prod-worker` require production configuration. `make infra-up` starts PostgreSQL, Redis, and development mail. `make migrate-up` applies application and River migrations. `make seed` is development-only. The schema baseline requires a fresh database; preserve existing databases and volumes unless their deletion is explicitly authorized.

Run `make generate`, `make test`, and `make lint` before submission. Run `make build` when changing application wiring or build tooling. Tests live beside implementation as `*_test.go`; prefer table-driven unit tests and `httptest`. PostgreSQL integration tests use isolated schemas in the disposable database configured by `BUILDX_TEST_DATABASE_URL` and skip clearly when unset. Cover validation, authentication, CSRF, session revocation, expired/single-use links, and transactional errors.

## Code and security

Format Go with `gofmt`. Use short lowercase package names, `PascalCase` exports, and `camelCase` internals. Explain non-obvious invariants and decisions with concise comments. Use `decimal.Decimal` if future features introduce money or quantities. Until the first public release, replace superseded interfaces in place and remove obsolete code.

Keep session/password operations in `internal/auth`. Every state-changing route requires POST and CSRF validation. Enforce authorization in services and owner-filtered queries for any future user-owned data. Keep credentials, password hashes, cookies, CSRF tokens, and account-link tokens out of logs. Production requires secure cookies, HTTPS public links, and SMTP STARTTLS.

## Collaboration

Use GitHub Issues for specs. Commit subjects use `Type(scope): Imperative message`, with `Feat`, `Fix`, `Docs`, `Refactor`, `Test`, or `Chore`. Keep commits focused, link relevant issues, and use `.github/pull_request_template.md`; include screenshots for UI changes. Preserve installed skills under `.agents/skills` and `skills-lock.json`.

@RTK.md
