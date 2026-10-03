# buildx

A small Go application with registration, email verification, login, logout, and password reset. Successful login opens `/account`, which shows the user's name and email. Public forms support Persian and English and light/dark themes.

## Structure

- `cmd/server`: HTTP entry point; `internal/app`: configuration and composition.
- `internal/auth`: handlers, services, repositories, and templ views.
- `internal/web`: middleware and shared layouts; `internal/locale`: UI and email copy.
- `internal/platform`: PostgreSQL, Redis, logging, and generated sqlc adapters.
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

The migration history is an auth-only baseline for a **fresh database**. It is not an upgrade path for the former finance application's database. Existing databases and Docker volumes are not reset automatically; point `DATABASE_URL` at a new database and apply the baseline there. The compose migrator uses the compose PostgreSQL database. Keep server and worker `DATABASE_URL`, `SESSION_SECRET`, and `PUBLIC_BASE_URL` consistent.

Compose names the PostgreSQL role and database `buildx` and uses the `buildx_postgres-data` volume. Earlier database volumes remain intact.

## Production

`make build` creates `bin/buildx`, `bin/buildx-worker`, and `bin/buildx-river-migrate`. The Dockerfile builds the same binaries and static assets; its default entry point runs the server. Run a separate container with entry point `/app/buildx-worker` for account emails.

Configure a production `.env` with `APP_ENV=production`, PostgreSQL/Redis URLs, a unique session secret, an HTTPS `PUBLIC_BASE_URL`, `COOKIE_SECURE=true`, and SMTP credentials with `SMTP_REQUIRE_TLS=true`. After applying the application and River migrations, use `make prod` for the server and `make prod-worker` for the worker. `make migrate-up` applies both migration sets to local compose PostgreSQL; for an external database run the application migrator against `DATABASE_URL`, then `bin/buildx-river-migrate` with that environment loaded.

Terminate HTTPS at a reverse proxy. Set `TRUSTED_PROXY=true` only when that proxy is the sole ingress and overwrites forwarding headers. Production PostgreSQL/Redis should be private; the compose services are intended for local infrastructure.

## Checks and auth invariants

`make generate`, `make test`, `make lint`, and `make build` verify generated adapters, Go tests, static analysis, and binaries. `make test-race` enables the race detector; `make security` scans dependencies. PostgreSQL integration tests isolate their schema and skip unless `BUILDX_TEST_DATABASE_URL` points at a disposable database. The complete HTTP auth journey also requires `BUILDX_TEST_REDIS_URL` for a disposable Redis instance.

Passwords use Argon2id. Sessions and one-use account links are hash-stored, forms require CSRF tokens, and Redis limits login and email requests. Accounts verify their mailbox before accessing `/account`. A password reset consumes its one-hour link and revokes all sessions. River records email jobs in the same transaction as the account challenge. Server and worker share the secret used to derive those links. Never log passwords, cookies, link tokens, or secrets.
