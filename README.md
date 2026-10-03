# buildx

A small Go application with registration, email verification, login, logout, and password reset. Successful login opens `/account`, which shows the user's name and email. Public forms support Persian and English and light/dark themes.

## Structure

- `cmd/server`: HTTP entry point; `internal/app`: configuration and composition.
- `internal/auth`: handlers, services, repositories, and templ views.
- `internal/web`: middleware and shared layouts; `internal/locale`: UI and email copy.
- `internal/platform`: SQLite, logging, and generated sqlc adapters.
- `internal/jobs`, `internal/worker`, `internal/mail`: durable account emails and expired-session cleanup.
- `db/migrations`, `db/queries`, `db/seeds`: schema, SQL, and optional development account.
- `assets`, `static`: Tailwind/daisyUI source and local browser assets.
- `.agents/skills`: retained development skills.

Add future features under `internal/<feature>` and wire them in `internal/app`. Keep HTTP parsing in handlers, rules/transactions in services, and SQL in repositories via sqlc.

## Development

Use the Go version in `go.mod`, Node.js, pnpm (pinned in `package.json`), and Docker Compose.

```sh
cp .env.example .env
# Set a unique SESSION_SECRET of at least 32 characters.
pnpm install --frozen-lockfile
make infra-up
make migrate-up
make dev
```

Run `make worker` in a second terminal so registration and reset emails are delivered. Open the app at http://localhost:8080 and development mail at http://localhost:8025. `make seed` optionally creates the verified account `alice@example.test` with password `DevPassword123!`; it is development-only.

Set `DATABASE_PATH=./data/buildx.db` in `.env`. The migrator enables persistent WAL mode before starting the first schema migration transaction, then creates the SQLite schema, including the email queue and rate-limit counters. Server and worker verify WAL at startup; if it is disabled, run `make migrate-up`. Server, worker, and migration commands must use the same file; keep `SESSION_SECRET` and `PUBLIC_BASE_URL` consistent too. SQLite uses a pure Go driver, so no database server, SQLite CLI, or C toolchain is required.

This is a fresh SQLite baseline. Existing PostgreSQL data is not imported, and existing databases and Docker volumes remain intact. Choose a new SQLite file for this setup. `make migrate-down` rolls back the latest migration and removes its tables; `make migrate-create name=description` creates the next pair of SQL files.

## Production

`make build` creates `bin/buildx`, `bin/buildx-worker`, and `bin/buildx-migrate`. The Dockerfile builds the same binaries and static assets; its default entry point runs the server. Run a separate container with entry point `/app/buildx-worker` for account emails, and use `/app/buildx-migrate up` to apply migrations.

Configure a production `.env` with `APP_ENV=production`, a persistent `DATABASE_PATH`, a unique session secret, an HTTPS `PUBLIC_BASE_URL`, `COOKIE_SECURE=true`, and SMTP credentials with `SMTP_REQUIRE_TLS=true`. Apply `make migrate-up`, then use `make prod` and `make prod-worker`. For binaries, load the same environment and run `bin/buildx-migrate up` before starting the server and worker.

Containers default to `/data/buildx.db`. Mount the same persistent directory at `/data` in the migrator, server, and worker, and ensure the `buildx` user can write it. Keep all processes on the same host with a local filesystem: SQLite WAL uses shared file locks and does not support network filesystems. See [SQLite WAL documentation](https://www.sqlite.org/wal.html). Back up with SQLite's online backup API or `VACUUM INTO`; copying only a live `.db` file can omit committed WAL data. Alternatively, stop both server and worker cleanly before copying the database.

Terminate HTTPS at a reverse proxy. Set `TRUSTED_PROXY=true` only when that proxy is the sole ingress and overwrites forwarding headers.

## Checks and auth invariants

`make generate`, `make test`, `make lint`, and `make build` verify generated adapters, Go tests, static analysis, and binaries. `make test-race` enables the race detector; `make security` scans dependencies. SQLite integration tests use migrated temporary files and run automatically, including the complete HTTP auth journey, concurrent rate limits, queue retries, and transaction rollback.

Passwords use Argon2id. Sessions and one-use account links are hash-stored, forms require CSRF tokens, and SQLite limits login and email requests with atomic counters shared by server processes. Accounts verify their mailbox before accessing `/account`. A password reset consumes its one-hour link and revokes all sessions. The SQLite queue records email jobs in the same transaction as the account challenge. Workers claim jobs with one-minute leases, allow 30 seconds for delivery, and retry failures up to ten attempts with exponential backoff. Obsolete links are skipped. Failed jobs are retained for 24 hours; expired sessions, challenges, receipts, and rate limits are cleaned hourly. Server and worker share the secret used to derive those links. Never log passwords, cookies, link tokens, or secrets.
