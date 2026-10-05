# Conversation-led studio verification (#38)

The implementation adapts `docs/design/piko-studio/qa/chat-desktop.png` and
`chat-preview-mobile.png`: the existing sidebar, brand, fonts, coral actions,
neutral surfaces, primary conversation, bottom composer, and adjacent pane.
The pane links to implemented manual configuration and standalone Preview;
interactive Flow, Changes, and embedded Preview remain for subsequent slices.
The dashboard markup and styles retain their existing visual design.

## Automated checks

`internal/app/studio_integration_test.go` verifies direct entry without saving
work, first-message admission/history and deduplication after restart, validation and CSRF, saved-chat ownership
and association, and private committed-state fragments without replaying runs.
Existing chat, creation, generation, and runtime integration tests remain in use.

`scripts/studio-browser-check.js` exercises the actual development application
with a deterministic external-provider fixture. It checks direct dashboard
entry, all three editable examples without POSTs, keyboard submission, saved
general/Bot chat navigation, manual configuration and Preview access, preserving
unsent text/focus/caret and focused controls after completion, long-history composer visibility,
mobile pane switching and hidden-history reading position across completion,
system/light/dark themes, and reduced motion.

Observed at 1440×1000 and 390×844 on 2026-10-05: all checks passed,
no horizontal overflow, and no page errors. No real model or Telegram calls
were made. Screenshots are temporary browser output under `/tmp`:

- `/tmp/piko-studio-desktop-light.png`
- `/tmp/piko-studio-desktop-dark.png`
- `/tmp/piko-studio-mobile-light.png`
- `/tmp/piko-studio-mobile-dark.png`
- `/tmp/piko-studio-mobile-pane-dark.png`

## Reproduce

Run from the repository root. Use a separate temporary SQLite file; preserve any
existing file. Start the local fixture in one terminal:

```sh
rtk node scripts/studio-browser-provider.mjs
```

In another terminal, export this isolated development configuration. The key
values below are synthetic test values, for this temporary environment only.
Exporting overrides Air's `.env` values, including the optional tracing settings.

```sh
export APP_ENV=development HTTP_ADDR=127.0.0.1:18088
export DATABASE_PATH=/tmp/piko-studio-38.db
export SESSION_SECRET=studio-test-session-secret-at-least-32-characters
export BOT_ENCRYPTION_KEY=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=
export COOKIE_SECURE=false BOT_PUBLIC_URL=
export OPENROUTER_API_KEY=studio-test-key OPENROUTER_MODEL=studio-test
export OPENROUTER_BASE_URL=http://127.0.0.1:18089
export LANGFUSE_BASE_URL= LANGFUSE_PUBLIC_KEY= LANGFUSE_SECRET_KEY=
rtk env GOFLAGS=-buildvcs=false GOCACHE=/tmp/piko-go-cache go run ./cmd/migrate up
rtk make dev
```

From `/tmp`, open the app with Playwright CLI and run the check. The script uses
the signed-in test account or registers one, and adds only synthetic chats/Bots.
It disables browser asset caching while checking the current development build.

```sh
rtk playwright-cli -s=piko38 open http://127.0.0.1:18088/register
rtk playwright-cli -s=piko38 run-code --filename=/home/mint/oss/buildx/scripts/studio-browser-check.js
rtk playwright-cli -s=piko38 close
```

Adjust the absolute repository path for another checkout. Stop the development
server and fixture afterward; retain the temporary database if needed for review.
