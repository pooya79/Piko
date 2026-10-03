# Piko

A small Go application with registration, login, logout, and a protected desktop Dashboard at `/dashboard`. Registration asks for a Display name, email, and password and signs the user in immediately. The retained `/account` page shows the saved name and email. All application screens render Persian with RTL layout and system/light/dark themes. Latin identifiers remain LTR, and user-provided Display names are escaped and isolated.

## Structure

- `cmd/server`: HTTP entry point; `internal/app`: configuration, composition, and expired-record cleanup.
- `internal/auth`: account and session handlers, services, repositories, and templ views.
- `internal/dashboard`: protected Dashboard and honest empty states; `internal/web`: middleware and shared layouts.
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
# Set a separate BOT_ENCRYPTION_KEY using: openssl rand -base64 32
pnpm install --frozen-lockfile
make migrate-up
make dev
```

Open the app at http://localhost:8080. `make seed` optionally creates the account `alice@example.test` with password `DevPassword123!`; it is development-only. Registration and login need no SMTP configuration, Mailpit, Docker Compose, or separate worker. Verification and password recovery are outside the current scope; their former URLs return not-found responses. An email address is an account identifier, not proof of mailbox ownership.

Server and migration commands must use the same local `DATABASE_PATH`. For new installations use `./data/piko.db`; existing SQLite installations should retain their path and apply `make migrate-up` before starting the updated server. Keep the existing `SESSION_SECRET` so anonymous form tokens remain consistent. The migrator enables persistent WAL mode before migration transactions. Server startup verifies WAL; run `make migrate-up` to repair disabled WAL. SQLite uses a pure Go driver, with no database server, SQLite CLI, or C toolchain required.

## Upgrading existing SQLite installations

Migration `000002_immediate_accounts` preserves the database file, user IDs, Display names, normalized emails, password hashes, and session credentials/expiration. Previously unverified users can sign in with their unchanged passwords, and unexpired sessions continue to work. The applied baseline is unchanged. No database or volume deletion is required.

The migration removes obsolete verification metadata, email challenges, signup receipts, mail cooldown records, queued emails, and mail/link rate budgets. Retired email links and transient mail data are intentionally discarded. Rolling back migration `000002` restores the retired schema with empty mail tables and unknown verification status; it cannot restore discarded data. Run `make migrate-up` before restarting this version after a rollback. Rolling back the original baseline removes application tables. PostgreSQL data is not imported by these SQLite migrations; preserve any existing PostgreSQL database and use a separate SQLite file.

Migration `000003_persian_only` removes `users.preferred_language` without changing account or session records. Old `piko_language` cookies are ignored, the `/language` route is removed, and authentication no longer writes a language cookie. Run `make migrate-up` before starting the new binaries. `make migrate-down` rolls back only the latest migration; rolling back `000003` restores a Persian default for all accounts, because retired language choices cannot be recovered. The application remains Persian-only after rollback.

The shared visual foundation uses the approved coral P/speech-bubble mark, matching favicons, and self-hosted Vazirmatn/Manrope fonts with licenses under `static/fonts`. The archived design reference remains in `docs/design/piko-studio`. Browser evidence for account screens is in `docs/qa/issue-3`, desktop Dashboard evidence is in `docs/qa/issue-4`, and the final responsive account/Dashboard matrix is in `docs/qa/issue-5`.

The Dashboard uses the saved Display name in its greeting and account identity, an Iran-local Persian calendar date, the approved companion illustration, and owner-only saved Bot cards with honest empty states. Bot list, connection, and detail pages are available at `/bots`, `/bots/connect`, and `/bots/{id}`. Chat, analytics, billing, help, and Forms remain unavailable; saved Drafts can be published and delivery explicitly activated. Metrics, monitoring, conversations, activity, and notifications retain honest unavailable/empty states; token verification does not produce live health or analytics. The illustrative idea prompt accepts no input. Account navigation, POST/CSRF logout, and persisted system/light/dark theme controls work across all screens. Chart.js is selected for future populated charts and is not installed or loaded. The workspace components live in `internal/web/shell`. Below 768px, navigation opens in a native modal drawer with keyboard focus containment/return, background inertness, Escape, a close control, and backdrop dismissal. Metrics use two columns, bot content stacks, and secondary monitoring/activity panels are reduced below 1024px as in the reference. Empty notifications use a native popover that closes with Escape, its close control, outside interaction, or keyboard focus leaving the panel. These interactions require a current browser with dialog and Popover API support.

Workspace pages pass a `shell.Page` to `shell.Workspace`. The zero value retains the Dashboard identity; authenticated application routes inject real owner-authorized Bot navigation. A renderer without supplied navigation still leaves Bot links disabled. Feature handlers supply a plain-text title, ordered breadcrumbs, the active Dashboard/Bot section, and owner-authorized recent Bot links. Set `BotsURL` and individual link URLs only for implemented destinations; an omitted Bot URL leaves navigation disabled or renders its name as text. The final breadcrumb identifies the current page without a link. The shell itself supplies no Bot persistence, status, or metrics. The domain glossary and ADRs 0002–0006 are already tracked for subsequent Bot tickets. Shell verification and screenshots are in `docs/qa/issue-7`.

## Connecting Telegram Bots

An authenticated owner can follow BotFather guidance, submit a secret token, and save the identity returned by Telegram `getMe`. Piko also calls `getWebhookInfo`, saves whether a webhook exists and its pending-update count, and presents delivery conflicts on the Bot detail page. The observed state is timestamped, not live monitoring. An empty webhook does not prove another polling service is absent. Token possession establishes Bot API access, not ownership of the owner's BotFather account. Verification performs no `setWebhook`, `deleteWebhook`, or `getUpdates` calls and preserves Telegram delivery and pending updates. Activation is a separate explicit step after publication.

Telegram identities are globally unique in Piko, including repeated connections by the same owner. Duplicate submissions return feedback without changing the original Bot, ownership, or credentials. Owner-filtered list/detail queries return not-found responses for another account's Bots. Browser connection mutations require POST, a signed-in session, and valid CSRF, with a bounded connection rate and Telegram timeout. Failed verification or delivery inspection saves nothing. Upstream response descriptions and credential-bearing transport errors are never returned or logged. Telegram usernames/IDs are LTR; names are escaped and direction-isolated.

`BOT_ENCRYPTION_KEY` is required in development and production. Supply standard base64 encoding of 32 random bytes, generated separately from `SESSION_SECRET` (for example `openssl rand -base64 32`). Both configuration loading and application construction validate it. Tokens use AES-256-GCM with a fresh 12-byte nonce, followed by ciphertext and its authentication tag; associated data is `piko:bot:v1:<owner_id>:<telegram_id>`. The key is never stored in SQLite. Public Bot projections omit credentials, and stored webhook URLs are omitted because they may contain another service's secret. Keep this key stable and back it up securely with the database: replacing or losing it makes saved credentials unreadable. Key rotation is not implemented in this slice.

Apply forward migration `000004_bots` before starting the updated server. It adds Bot storage without changing accounts or sessions. Rolling it back removes saved Bots and their encrypted credentials; existing account/session records remain. Preserve existing SQLite files and volumes. Delivery workers start only with the server lifecycle and process explicitly activated Bots. Bot HTTP/fake-Telegram tests use temporary migrated SQLite; screenshots and verification notes live in `docs/qa/issue-8`.

## Configuring and previewing Drafts

Open a Bot's configuration at `/bots/{id}/draft` to save a welcome message, menu prompt, and one to six choices with distinct labels and configured replies. Messages allow up to 2,000 characters and labels up to 80. Leave both fields empty to remove a choice. Invalid edits preserve the previously saved Draft. Owner text is escaped and direction-isolated; system copy remains Persian.

Version 1 Flow definitions contain approved message and menu Blocks with validated IDs and choice references. The settings form constructs this structured definition; a CSRF-protected `definition` form field can also submit the same strict JSON schema. Unsupported versions, Blocks, fields, missing destinations, and duplicate IDs/labels are rejected. Version 1 permits only welcome → menu → chosen message → menu, bounding each action to two outgoing messages. The shared interpreter is in `internal/bot/runtime`; the simulated delivery adapter is in `internal/bot/preview`. Neither depends on individual Templates or executes scripts.

At `/bots/{id}/preview`, start an independent test of the latest saved Draft. Its snapshot and conversation are stored separately in `bot_previews`, with no real Participant state, Submissions, publication, activation, or Telegram API calls. Existing tests retain their snapshot after Draft edits and server restarts; start a fresh Preview to try the latest saved configuration. Restart clears only that test's conversation. Each test expires 24 hours after creation, retains at most 32 messages, and is removed by server startup/hourly cleanup. Owner-filtered access and atomic revision checks prevent cross-owner access and stale or concurrent buttons from changing the wrong state. All browser mutations require POST, authentication, and CSRF. Preview creation is rate limited.

Apply forward migration `000005_bot_drafts` before starting the updated server. It preserves accounts, sessions, Bot identities, encrypted credentials, and saved delivery observations. Rolling it back removes only Drafts and Preview test state. Existing SQLite files are preserved. HTTP verification and desktop/mobile screenshots are in `docs/qa/issue-9`.

## Publishing and activating a welcome Bot

On the Bot detail page, **Publish saved Draft** validates and creates an immutable Flow version. Saving later Draft edits leaves live behavior unchanged. Publication is separate from activation: open **Activate in Piko**, review the fresh Telegram delivery observation, and explicitly confirm that Piko will operate the Bot. A foreign webhook requires confirmation; if it changes before submission, Piko presents the new conflict again without switching delivery. An empty webhook cannot rule out another polling service. Verification continues to call only `getMe` and `getWebhookInfo`.

Production requires `BOT_PUBLIC_URL`, a public HTTPS origin on a Telegram-supported port (443, 80, 88, or 8443). The reverse proxy must forward `POST /telegram/bots/{numeric-id}` and `X-Telegram-Bot-Api-Secret-Token` unchanged. Each Bot receives its own random secret, encrypted with domain separation under `BOT_ENCRYPTION_KEY`. Tokens and webhook secrets never appear in incoming URLs or owner pages. This machine ingress authenticates before parsing the body and uses no browser session or CSRF cookie. Owner publication/activation still require authentication, ownership, POST, and CSRF. Requests over 256 KiB are rejected. Piko sets `max_connections=1`, restricts new updates to messages/callbacks, and never drops pending updates. The Bot page shows persisted publication, activation, and outbound-error states, rather than a health guarantee. A failed or timed-out activation can leave Telegram's result uncertain; inspect and retry from the activation page. A crashed activation can be retried after its 30-second lease expires.

With `BOT_PUBLIC_URL` empty in development, explicit activation removes the webhook without dropping pending updates and selects polling. Use a separate development Bot. Polling persists each update before advancing its offset and feeds the same interpreter and inbox as webhooks. Supplying an HTTPS origin in development permits webhook testing. Production cannot start without an HTTPS origin.

Private-chat `/start`, menu buttons, and configured replies use the same engine as Preview. Each Start selects the latest published version; an existing menu retains its original version through publication changes. New buttons carry an opaque token bound through durable state to their Bot, Participant, chat, menu step, and Flow version. Old, repeated, expired, or another Participant's buttons cannot change state and receive callback acknowledgements. Participant menu state expires after 24 hours of inactivity, enforced at access and startup/hourly cleanup. Groups, channels, Forms, and Submissions remain outside this slice.

Two bounded workers use SQLite leases to serialize each Bot's queued transitions and outgoing messages, including across server processes. The inbox deduplicates `(Bot, update_id)` before acknowledging webhook receipt, with at most 1,000 unfinished updates per Bot; temporary storage/backlog failures return 503 for Telegram retry. State changes and staged outputs commit together, and Telegram calls run outside transactions. Outgoing actions have durable progress and retry with backoff capped at 60 seconds. A retryable outgoing failure blocks later updates for that Participant until it succeeds; unrelated Participants continue. Telegram 403 recipient failures are recorded as terminal, with no repeated sends to a blocked recipient. Owners see persisted pending and terminal errors. Shutdown closes request admission, cancels active HTTP work, and joins both handlers (including activation-result persistence) and workers before SQLite closes. After a hard crash, a worker lease may delay recovery by up to 60 seconds. Delivery continues when the owner's browser closes. A timeout or crash after Telegram accepted a send but before its progress commit can produce a duplicate message: Piko does **not** promise exactly-once sends.

Apply forward migrations `000006_bot_publication` and `000007_bot_delivery` to the existing SQLite file before starting this version. They preserve accounts, sessions, Bot credentials, Drafts, and Previews. Completed inbox IDs are retained as deduplication tombstones; output bodies are compacted after 24 hours. Published versions are retained for existing Participant state. Monitor database storage as Bot traffic grows. Rolling back `000007` removes delivery credentials/inbox/Participant state; rolling back `000006` removes publications. Stop the server first; rolling back local state does not remove Telegram's remote webhook. Preserve the file and encryption key. Screenshots and verification notes are in `docs/qa/issue-10`.

## Production

`make build` creates `bin/piko` and `bin/piko-migrate`. The Dockerfile builds the same binaries and static assets; its default entry point runs the server. Use `/app/piko-migrate up` to apply migrations before starting `/app/piko`.

Configure `APP_ENV=production`, a persistent `DATABASE_PATH`, a unique session secret, a separate stable `BOT_ENCRYPTION_KEY`, and `COOKIE_SECURE=true`. Apply `make migrate-up`, then use `make prod`. For binaries, load the same environment and run `bin/piko-migrate up` before starting the server. Set `BOT_PUBLIC_URL` to the public HTTPS origin (for example `https://bots.example.com`), with no path, credentials, query, or fragment. Obsolete `PUBLIC_BASE_URL`, SMTP, and Mailpit settings can be removed from existing environment files; they are no longer read.

Containers default to `/data/piko.db`. Mount the persistent directory at `/data` in the migrator and server, and ensure the `piko` user can write it. Keep processes on the same host with a local filesystem: SQLite WAL uses shared file locks and does not support network filesystems. See [SQLite WAL documentation](https://www.sqlite.org/wal.html). Back up with SQLite's online backup API or `VACUUM INTO`; copying only a live `.db` file can omit committed WAL data. Alternatively, stop the server cleanly before copying the database.

Terminate HTTPS at a reverse proxy. Set `TRUSTED_PROXY=true` only when that proxy is the sole ingress and overwrites forwarding headers.

## Checks and auth invariants

Run `make generate`, `make test`, `make lint`, and `make build` to verify generated adapters, Go tests, static analysis, and binaries. `make test-race` enables the race detector; `make security` scans dependencies. HTTP/SQLite integration tests use migrated temporary files and run without external services.

Passwords use Argon2id. Session and CSRF tokens are hash-stored; state-changing routes require POST and CSRF validation. SQLite enforces login and registration rate limits with atomic counters shared by server processes. Successful registration creates the account and initial 24-hour session in one immediate transaction; session creation failure rolls back the account. Duplicate registration returns email-field feedback with a login link, preserves the existing credentials, and grants no session for that account. Logout revokes the session. Expiration is enforced at access time; expired sessions and rate limits are also cleaned transactionally at server startup and hourly. Shutdown cancels and joins cleanup before closing SQLite. Never log passwords, cookies, tokens, or secrets.

The accepted [accounts-without-verification decision](docs/adr/0001-accounts-without-email-verification.md) and [domain glossary](CONTEXT.md) describe account access and the Dashboard/Display name terminology.
