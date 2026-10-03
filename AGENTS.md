# Repository Guidelines

## Structure and boundaries

`cmd/server` is the HTTP entry point and `internal/app` composes concrete dependencies. Keep each future feature cohesive under `internal/<feature>` with handlers, services, repositories, and templ views. Shared HTTP concerns live in `internal/web`; SQLite, logging, and generated sqlc adapters live in `internal/platform`. Account email delivery and cleanup use `internal/jobs`, `internal/worker`, and `internal/mail`.

Keep HTTP parsing in handlers, business rules and transaction boundaries in services, and SQL in named statements under `db/queries`. SQLite stores accounts, sessions, durable email jobs, and rate limits in one file. Use the configured immediate transactions for account changes and queue inserts. Edit `.templ` and SQL sources, then regenerate their Go output. Keep static assets in `static/` and CSS sources in `assets/`. See `README.md` for development/production setup and auth invariants.

## Development and validation

Use `make dev` and `make worker` for development; `make prod` and `make prod-worker` require production configuration. `make infra-up` starts development mail. `make migrate-up` applies SQLite migrations. `make seed` is development-only. The SQLite baseline requires a new database file; preserve existing databases and volumes unless their deletion is explicitly authorized.

Run `make generate`, `make test`, and `make lint` before submission. Run `make build` when changing application wiring or build tooling. Tests live beside implementation as `*_test.go`; prefer table-driven unit tests and `httptest`. SQLite integration tests use migrated files under `t.TempDir()` and run without external services. Cover validation, authentication, CSRF, session revocation, expired/single-use links, and transactional errors.

## Code and security

Format Go with `gofmt`. Use short lowercase package names, `PascalCase` exports, and `camelCase` internals. Explain non-obvious invariants and decisions with concise comments. Use `decimal.Decimal` if future features introduce money or quantities. Until the first public release, replace superseded interfaces in place and remove obsolete code.

Keep session/password operations in `internal/auth`. Every state-changing route requires POST and CSRF validation. Enforce authorization in services and owner-filtered queries for any future user-owned data. Keep credentials, password hashes, cookies, CSRF tokens, and account-link tokens out of logs. Production requires secure cookies, HTTPS public links, and SMTP STARTTLS.

## Collaboration

Use GitHub Issues for specs. Commit subjects use `Type(scope): Imperative message`, with `Feat`, `Fix`, `Docs`, `Refactor`, `Test`, or `Chore`. Keep commits focused, link relevant issues, and use `.github/pull_request_template.md`; include screenshots for UI changes. Preserve installed skills under `.agents/skills` and `skills-lock.json`.

@RTK.md

## Agent skills

### Issue tracker

Issues and specs live in GitHub Issues. For ticket operations, read
`docs/agents/issue-tracker.md`.

### Triage labels

Use the five default triage labels. Before triaging, read
`docs/agents/triage-labels.md`.

### Domain docs

Use a single-context layout: root `CONTEXT.md` and `docs/adr/`.
Before exploring domain concepts or decisions, read `docs/agents/domain.md`.
