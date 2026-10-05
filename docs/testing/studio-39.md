# Builder run continuity in the studio (#39)

The studio uses the existing authenticated HTTP routes and Go/templ fragments.
Enhanced Send, Stop, interrupted recovery, and Undo forms send same-origin,
form-encoded POSTs with CSRF. Ordinary forms retain server navigation. A successful
admission returns `X-Piko-Accepted: true` and a new composer request key; validation,
busy, and stale-action responses retain editable input. Transport errors do not
repeat a POST.

SSE snapshots show disposable text/progress. General reply text streams after its
prefix is distinguishable from a private JSON build-routing decision. Routing
JSON and tool arguments remain hidden. Only committed fragments update history,
Bot association, Draft revision, and action availability. The active chat holding
a Bot's shared fence is linked and observed from its other Builder chats.

Terminal snapshots, returning to the page, and visible-page reconciliation every
five seconds share the same read-only refresh boundary. Identical committed
responses leave the DOM in place. A failed refresh keeps the current studio and
provides an explicit refresh control. Reconciliation never admits or retries work.

## State boundary for later pane slices

`window.pikoStudio.registerPaneState(name, { capture, restore })` registers a
synchronous adapter and returns an unregister function. `capture(currentStudio)`
returns local pane state; `restore(nextStudio, snapshot)` reinstates it after the
server fragment is mounted, before focus/reading position are restored. Store
selection, zoom, or isolated Preview UI state here; committed identity and action
availability still come from the server. `piko:studio-updated` is dispatched after
the update with `event.detail.studio`. `window.pikoStudio.refresh()` returns the
queued refresh promise for pane actions that need committed reconciliation. The
promise remains pending if IME composition postpones the request or mounting.

The boundary preserves composer text, caret/direction, composer scroll, focused
controls (with a pane/composer fallback when an action disappears), open details,
selected mobile view, and reading positions including a temporarily hidden view.
Active IME composition delays replacement. Text typed during admission survives;
only the unchanged admitted message is cleared. Draft text is stored per owner
and chat in session storage when available.

## Verification

`internal/app/studio_runs_integration_test.go` exercises fragment forms, request-key
rotation, CSRF, other-chat busy state, Stop/retry restrictions, provisional versus
committed identity, stale Draft conflict, explicit interrupted recovery, and
provisional general reply streaming, including JSON and brace-leading prose. Existing studio, ownership, publication,
background-run, and stream integration tests cover durable association,
disconnect/reconnect, accounting, authorization, and live-operation fences.

Use the isolated environment described in [studio-38.md](studio-38.md), changing
`DATABASE_PATH` to a separate `/tmp/piko-studio-39.db`. The local provider fixture
now includes synthetic staged creation/edit, held requests, failures, and a
read-only call counter. No model or Telegram calls are made. Browser output must
stay in `/tmp`.

After starting the fixture and development server, run from `/tmp`:

```sh
rtk playwright-cli -s=piko39 open http://127.0.0.1:18088/register
rtk playwright-cli -s=piko39 run-code --filename=/home/mint/oss/buildx/scripts/studio-browser-check.js
rtk playwright-cli -s=piko39 run-code --filename=/home/mint/oss/buildx/scripts/studio-runs-browser-check.js
rtk playwright-cli -s=piko39 run-code --filename=/home/mint/oss/buildx/scripts/studio-interruption-start.js
```

Restart only this temporary development server on the same temporary database,
leaving the browser and provider fixture open. Then run:

```sh
rtk playwright-cli -s=piko39 run-code --filename=/home/mint/oss/buildx/scripts/studio-interruption-recover.js
rtk playwright-cli -s=piko39 run-code --filename=/home/mint/oss/buildx/scripts/studio-refresh-browser-check.js
rtk playwright-cli -s=piko39 close
```

Observed on 2026-10-05 at 1440×1000 and 390×844: staged creation/edit remained
provisional until commit; terminal revision/actions refreshed; unsent input,
caret/focus, pane selection/state, and reading positions survived updates. Stop,
failure restrictions, disconnect/reconnect, other-chat busy completion, lost
streams, IME composition, refresh failures, unavailable EventSource, and explicit
restart recovery passed without replaying provider calls.
Desktop/mobile light/dark layouts had no horizontal overflow or page errors.
