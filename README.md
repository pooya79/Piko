# Piko

A small Go application with registration, login, logout, and a minimal protected Dashboard at `/dashboard`. Registration asks for a Display name, email, and password and signs the user in immediately. The retained `/account` page shows the saved name and email. Public and signed-in screens support Persian and English and system/light/dark themes.

## Structure

- `cmd/server`: HTTP entry point; `internal/app`: configuration, composition, and expired-record cleanup.
- `internal/auth`: account and session handlers, services, repositories, and templ views.
- `internal/dashboard`: minimal protected Dashboard; `internal/web`: middleware and shared layouts.
- `internal/locale`: UI copy; `internal/platform`: SQLite, logging, and generated sqlc adapters.
- `db/migrations`, `db/queries`, `db/seeds`: schema, SQL, and optional development account.
- `assets`, `static`: Tailwind/daisyUI source and local browser assets.
- `.agents/skills`: retained development skills.

Add future features under `internal/<feature>` and wire them in `internal/app`. Keep HTTP parsing in handlers, rules/transactions in services, and SQL in repositories via sqlc.

## Development

Use the Go version in `go.mod`, Node.js, and pnpm (pinned in `package.json`).

```sh
cp .env.example .env
# Set a unique SESSION_SECRET of at least 32 characters.
pnpm install --frozen-lockfile
make migrate-up
make dev
```

Open the app at http://localhost:8080. `make seed` optionally creates the account `alice@example.test` with password `DevPassword123!`; it is development-only. Registration and login need no SMTP configuration, Mailpit, Docker Compose, or separate worker. Verification and password recovery are outside the current scope; their former URLs return not-found responses. An email address is an account identifier, not proof of mailbox ownership.

Server and migration commands must use the same local `DATABASE_PATH`. For new installations use `./data/piko.db`; existing SQLite installations should retain their path and apply `make migrate-up` before starting the updated server. Keep the existing `SESSION_SECRET` so anonymous form tokens remain consistent. The migrator enables persistent WAL mode before migration transactions. Server startup verifies WAL; run `make migrate-up` to repair disabled WAL. SQLite uses a pure Go driver, with no database server, SQLite CLI, or C toolchain required.

## Upgrading existing SQLite installations

Migration `000002_immediate_accounts` preserves the database file, user IDs, Display names, normalized emails, password hashes, language preferences, and session credentials/expiration. Previously unverified users can sign in with their unchanged passwords, and unexpired sessions continue to work. The applied baseline is unchanged. No database or volume deletion is required.

The migration removes obsolete verification metadata, email challenges, signup receipts, mail cooldown records, queued emails, and mail/link rate budgets. Retired email links and transient mail data are intentionally discarded. `make migrate-down` rolls back only the latest migration: it restores the retired schema with empty mail tables and unknown verification status; it cannot restore discarded data. Run `make migrate-up` before restarting this version after a rollback. Rolling back the original baseline removes application tables. PostgreSQL data is not imported by these SQLite migrations; preserve any existing PostgreSQL database and use a separate SQLite file.

## Production

`make build` creates `bin/piko` and `bin/piko-migrate`. The Dockerfile builds the same binaries and static assets; its default entry point runs the server. Use `/app/piko-migrate up` to apply migrations before starting `/app/piko`.

Configure `APP_ENV=production`, a persistent `DATABASE_PATH`, a unique session secret, and `COOKIE_SECURE=true`. Apply `make migrate-up`, then use `make prod`. For binaries, load the same environment and run `bin/piko-migrate up` before starting the server. Obsolete `PUBLIC_BASE_URL`, SMTP, and Mailpit settings can be removed from existing environment files; they are no longer read.

Containers default to `/data/piko.db`. Mount the persistent directory at `/data` in the migrator and server, and ensure the `piko` user can write it. Keep processes on the same host with a local filesystem: SQLite WAL uses shared file locks and does not support network filesystems. See [SQLite WAL documentation](https://www.sqlite.org/wal.html). Back up with SQLite's online backup API or `VACUUM INTO`; copying only a live `.db` file can omit committed WAL data. Alternatively, stop the server cleanly before copying the database.

Terminate HTTPS at a reverse proxy. Set `TRUSTED_PROXY=true` only when that proxy is the sole ingress and overwrites forwarding headers.

## Checks and auth invariants

Run `make generate`, `make test`, `make lint`, and `make build` to verify generated adapters, Go tests, static analysis, and binaries. `make test-race` enables the race detector; `make security` scans dependencies. HTTP/SQLite integration tests use migrated temporary files and run without external services.

Passwords use Argon2id. Session and CSRF tokens are hash-stored; state-changing routes require POST and CSRF validation. SQLite enforces login and registration rate limits with atomic counters shared by server processes. Successful registration creates the account and initial 24-hour session in one immediate transaction; session creation failure rolls back the account. Duplicate registration returns email-field feedback with a login link, preserves the existing credentials, and grants no session for that account. Logout revokes the session. Expiration is enforced at access time; expired sessions and rate limits are also cleaned transactionally at server startup and hourly. Shutdown cancels and joins cleanup before closing SQLite. Never log passwords, cookies, tokens, or secrets.

The accepted [accounts-without-verification decision](docs/adr/0001-accounts-without-email-verification.md) and [domain glossary](CONTEXT.md) describe account access and the Dashboard/Display name terminology.
