# Piko studio integrated acceptance (#48)

Verified on 2026-10-05 against baseline `f389a11468042da177b6f8d5976151909b811931`.
Specifications: [#48](https://github.com/pooya79/Piko/issues/48) and
[parent #34](https://github.com/pooya79/Piko/issues/34). Prerequisites #40–43 and
#45–47 were closed when checked.

**Local functional and browser acceptance passed. Controlled live acceptance
remains pending.** The existing recorded setup limit still applies: no securely
configured model access or dedicated Telegram test Bot was available. No live
model or Telegram check was performed, and this report does not certify the
parent's live-before-rollout gate.

## Functional evidence

`make test` passed; `internal/app` completed in 185.383 seconds. The suites use
application HTTP, migrated temporary SQLite, the real generation adapter and
synthetic external OpenRouter/Telegram wire servers. Preceding tickets retain
their regression tests; this acceptance work does not replace them.

| Journey | Existing suites exercised by the full run |
| --- | --- |
| General questions, explicit intent, history, owner isolation | `piko_chat_integration_test.go`, `studio_integration_test.go` |
| Same-chat creation, failed/stopped builds without Bots, deduplication, interruption and reconnect | `piko_build_integration_test.go`, `builder_background_integration_test.go`, `builder_stream_integration_test.go` |
| Inquiry, Registration and Booking request creation/edit/Preview/Undo; supported Question types and unsupported candidate rejection | `piko_journey_integration_test.go`, `builder_form_integration_test.go` |
| Selected Blocks, committed Changes, guarded Undo and source-revision Preview | `studio_flow_integration_test.go`, `studio_changes_integration_test.go`, `studio_preview_integration_test.go`, `builder_undo_integration_test.go` |
| Confirmations, stale proposals, partial deployment and activation retry, pause persistence and original Participant Flow versions | `action_proposal_integration_test.go`, `deploy_integration_test.go`, `piko_journey_integration_test.go`, `interaction_integration_test.go` |
| Credentials, settings, deletion scope, Submissions and pagination | `connection_guidance_integration_test.go`, `settings_integration_test.go`, `submission_integration_test.go` |
| Authentication, CSRF, session lifecycle, cleanup and forward compatibility | Authentication/cleanup suites in `internal/app`, `internal/auth`, `internal/web`; migration tests in `internal/platform/database` and application suites |

Focused `TestPiko` and studio/settings/deletion tests also passed. Generation,
Go vet/staticcheck (`make lint`), and both binaries (`make build`) passed.
Staticcheck's Persian format-character warning in the new fixture was corrected
with equivalent Unicode escapes. Tool-generated package-manager lockfile and
unrelated CSS drift were discarded; no dependency, schema or first-page layout
change is included.

## Browser evidence

The saved Playwright CLI checks passed against isolated local application
instances. All accounts, answers, provider replies and Telegram identities were
synthetic. No existing application database was used or reset.

| Check | Observed result |
| --- | --- |
| `studio-browser-check.js` | Direct entry, all three examples fill/focus without submitting, saved chats, composer/caret/reading preservation, keyboard controls, desktop/mobile switching, system/light/dark and reduced motion |
| `studio-runs-browser-check.js` | Provisional creation/edit stays uncommitted, Stop, continuation after navigation, stream reconnect without repeated calls, busy chat recovery and lost-stream reconciliation |
| `studio-refresh-browser-check.js` | IME composition waits for reconciliation, recoverable fragment failure without replay, fallback when EventSource is unavailable |
| `studio-flow-browser-check.js` | Actual saved graph, keyboard selection/zoom, selected-Block handoff and request, committed refresh, stale selection rejection |
| `studio-preview-browser-check.js` | Isolated confirmation, old snapshot restart versus fresh latest Draft, visible staleness, preserved answer/caret/scroll, failed request recovery without repeating POST |
| `studio-changes-browser-check.js` | Committed differences, one-step Undo, newer manual edit rejects stale Undo, failed run makes no invented changes, preserved mobile pane/composer |
| `studio-interruption-start.js` + `studio-interruption-recover.js` | Preserved same SQLite file across server restart, interrupted outcome, no automatic provider replay, explicit retry, Stop and unsent-message retention |
| `connection-browser-check.js` | Initial connection, masked LTR credential fields, validation clears secrets, wrong-identity rejection, replace/disconnect/reconnect, unchanged composer and no implicit activation |
| `settings-browser-check.js` | Name validation/focus, manual collapsed-field validation, stale editor conflict, sticky savebar does not obscure focused fields, cancelled/confirmed chat and Bot deletion, unrelated work retained |
| `submission-browser-check.js` | Real runtime-created synthetic Submissions, 50/1 pagination, frozen labels/version, escaped mixed-language answers, LTR Participant identifier, keyboard confirmation/cancel, deletion isolation and honest empty/invalid-cursor states |
| `acceptance-browser-check.js` | General question without a Bot, personalized example, same-chat creation without reload, completion of that Draft's embedded Preview without real Submissions, connection guidance, stale rename approval, unchecked deployment consent, keyboard Deploy/Pause/Resume, Deploy retains pause, mobile modal focus containment/return |

The new opt-in `TestAcceptanceBrowserFixture` uses the same application HTTP and
external-wire seam as the integration suites, including conversation compaction.
It adds no production routes. Expected deliberately aborted requests and
400/404/409/422 responses appear in browser console logs; the journey assertions
and page-error checks passed.

Harness corrections revealed by the pass:

- The Submissions script now derives the origin without a `URL` global, which
  is unavailable in the Playwright CLI script sandbox.
- The older studio script checks the implemented embedded Preview launcher and
  independently verifies the retained standalone HTTP destination.
- Changes screenshots use actual theme buttons and assert the applied theme;
  directly mutating theme storage/attributes could race system-theme events.
- Restart staging verifies the complete unsent message, and recovery checks it
  before and after read-only reconciliation. An initial text mismatch did not
  reproduce in ten focused refresh probes or the repeated full restart check;
  no application persistence fix is claimed.
- Studio help now accurately describes embedded Preview and separate manual
  settings.

## Visual inspection

Desktop 1440×1000 and mobile 390×844 screenshots were captured in light and dark;
applicable secondary-screen scripts also exercised system mode. Images were
inspected individually and in contact sheets against the archived reference's
`chat-desktop.png`, `chat-mobile.png` and `home-desktop.png`. This is visual
inspection, not a pixel-diff claim or a blanket accessibility certification.

The Go/templ adaptation retains Vazirmatn/Manrope typography, coral actions,
lilac/light surfaces, neutral dark surfaces, shared spacing, rounded panels and
RTL composition. The first page retains its hero/illustration, metric-card and
workspace treatment with honest unavailable data. No Dashboard, shared shell or
CSS source was redesigned. Mobile uses full-width studio views; the composer
remains visible, and long Flow/Preview/history content scrolls within its pane.
No checked journey produced document horizontal overflow. Focus rings, modal
focus containment/return, Latin identifiers, escaped answers, reduced motion,
empty states and injected loading/error states were checked.

Representative temporary artifacts from this run:

- `/tmp/piko48/dashboard-{desktop,mobile}-{light,dark}.png`
- `/tmp/piko48/confirmations-{desktop,mobile}-{light,dark}.png`
- `/tmp/piko-studio-{desktop,mobile}-{light,dark}.png`
- `/tmp/piko-flow-{desktop,mobile}-{light,dark}.png`
- `/tmp/piko-changes-{desktop,mobile}-{light,dark,system}.png`
- `/tmp/piko40-browser-output/{desktop,mobile}-{light,dark}[-controls].png`
- `/tmp/piko-connection-*`, `/tmp/piko-settings-*`, `/tmp/piko-submissions-*`

Artifacts are temporary browser output, not archived production/customer data.
The saved scripts reproduce them. The production entry points still render
Go/templ and focused local JavaScript; archived React/demo state remains a design
reference. No new runtime capability or production frontend dependency was added.

## Reproduce the new integrated browser scenario

From the repository root, start this opt-in fixture in one terminal:

```sh
rtk mkdir -p /tmp/piko48
rtk env GOFLAGS=-buildvcs=false GOCACHE=/tmp/piko-go-cache \
  PIKO_ACCEPTANCE_BROWSER_ADDR=127.0.0.1:18094 \
  go test ./internal/app -run '^TestAcceptanceBrowserFixture$' \
  -v -count=1 -timeout=30m
```

It remains serving until stopped. Use a fresh fixture for each complete replay.
The script logs into its synthetic account and uses only its synthetic token.
In another terminal with `/tmp` as its working directory, use the absolute
repository script path (replace `/absolute/path/to/Piko` below):

```sh
rtk playwright-cli -s=piko48accept open http://127.0.0.1:18094/login --headed
rtk playwright-cli -s=piko48accept goto http://127.0.0.1:18094/login
rtk playwright-cli -s=piko48accept run-code --filename=/absolute/path/to/Piko/scripts/acceptance-browser-check.js
rtk playwright-cli -s=piko48accept close
```

Existing connection/settings/Submissions scripts identify their respective
opt-in fixture/environment variable in their first line. Studio scripts use
`studio-browser-provider.mjs` on loopback port 18089 and a separately migrated
`/tmp` development database. Run refresh fallback checks in a separate browser
tab/session: their EventSource override intentionally persists on that page.

## Remaining live acceptance

Securely configure model access and connect a dedicated test Bot through its
credential screen. Follow the bounded three-Template live protocol in README's
"Supported Piko journey evidence (#37)" section. Record actual model identity,
configured allowance/call/timeout bounds, observed saved behavior, synthetic
private-chat Submission, pause/resume and original Flow-version behavior.
Keep tokens, session credentials, customer data and Participant answers out of
the evidence. Do not increase configured limits or manufacture receiver/credential
failures. Repeat live checks only where subsequent changes justify them.

Until this evidence exists, #48's complete live acceptance and #34's rollout gate
remain unresolved. Deterministic replies establish orchestration/runtime
correctness; they do not establish real-model interpretation or reliability.
