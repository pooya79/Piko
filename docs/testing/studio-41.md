# Flow inspection verification (#41)

The studio adapts the approved `docs/design/piko-studio/qa/bot-flow.png`
reference to the existing Go/templ conversation and adjacent pane. The saved
approved Flow supplies every menu destination, message, sequential Question,
review and acknowledgement. Runtime return, skip, back, edit, confirmation and
cancel/restart paths are named explicitly. No capacity decisions or arbitrary
branches are supplied by the UI.

Native details provide keyboard inspection and remain usable without script.
Zoom ranges from 75% to 150%. Canvas overflow is confined to its scrollable
viewport; the conversation and mobile view switch remain independent. The Flow
and Preview tabs preserve their state through committed studio reconciliation.
Valid inspection identities survive saves with refreshed details. A pending
change request becomes stale after any intervening Draft revision and requires
fresh selection, even when its Block still exists. Its old reference and a
warning remain visible in the composer until the owner explicitly clears or
replaces it; repeated Send continues to reject the stale target. Restart paths
lead through Welcome before Menu, matching the runtime.

Selected references encode the Block kind and exact identity; Questions also
include their Form identity. The service resolves the owner-owned chat and its
Bot, loads the committed Draft, and validates selection inside the same immediate
transaction as admission. The provider receives server-derived Block content,
Bot identity and Draft revision. Client-supplied prose cannot substitute for that
validation. Migration 000021 retains references for explicit interrupted retries;
deduplication binds them to the original message and request key.

## Automated verification

`internal/app/studio_flow_integration_test.go` uses the application HTTP seam,
migrated temporary SQLite files and a fake external provider. It covers actual
Form/message projections, manual refresh, same-ID Questions in different Forms,
outgoing authorized provider context, maximum approved Unicode identities,
stale/removed/malformed selections,
duplicate requests, CSRF and owner isolation, preserved historical runs through
migration, and retry context/revalidation across restart. The historical Preview
migration scenario is pinned to its original schema using the existing fixture
helper rather than assuming it is the latest migration.

## Browser verification

Use the isolated configuration and fake provider in [studio-38.md](studio-38.md),
with a fresh `/tmp/piko-studio-41.db` database. Start the provider and run migrations
before `make dev`. No live model or Telegram credentials are needed. From `/tmp`:

```sh
rtk playwright-cli -s=piko41 open http://127.0.0.1:18088/register
rtk playwright-cli -s=piko41 run-code --filename=/home/mint/oss/buildx/scripts/studio-flow-browser-check.js
rtk playwright-cli -s=piko41 run-code --filename=/home/mint/oss/buildx/scripts/studio-preview-browser-check.js
rtk playwright-cli -s=piko41 close
```

The Flow scenario saves synthetic approved content through HTTP, including all
six Question types and a separate message destination. It checks keyboard zoom
and native-details selection, preserved unsent prose/focus, the submitted
selection fields, a new follow-up target selected during admission, valid
zoom/selection through a terminal outcome, provisional
versus committed graph state, updated/removed Blocks, stale POST rejection,
mobile handoff, pane switching, themes, and document overflow.

Observed on 2026-10-05 at 1440×1000 and 390×844: the Flow checks passed with no
page errors or document overflow. The existing embedded Preview browser suite
also passed, including answer/progress/focus preservation through run fragments.
The expected stale POST returned 409. Visual
inspection covered the saved screenshots:

- `/tmp/piko-flow-desktop-light.png`
- `/tmp/piko-flow-desktop-dark.png`
- `/tmp/piko-flow-mobile-light.png`
- `/tmp/piko-flow-mobile-dark.png`

Browser evidence uses the deterministic local provider; it does not establish
live-model reliability or Telegram delivery. Keep browser output under `/tmp`.

## Repository checks and review

`make generate`, `make test`, `make lint` and `make build` passed. Focused HTTP
regressions and the Flow browser scenario passed again after review fixes.
The independent Standards review found no actionable issues. The Spec review's
stale-context and restart-path findings were corrected and re-reviewed with no
remaining actionable gaps. Restart links also have regression coverage for
fragment URL encoding. The baseline for both reviews is `93f809e`.
