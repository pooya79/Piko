# Telegram credential lifecycle (#45)

The studio Deployment panel guides an Unconnected Bot into its dedicated token
form and a Disconnected Bot into reconnection. The overview and connection page
separate credential verification from explicit Deployment, activation and Resume.
A retained Published flow can be activated without republishing. Connection,
replacement and reconnection retain the pause choice and saved work.

Connection forms use masked LTR secret inputs within the RTL workspace, with
empty values after failures, associated field errors, saved verified identity and
studio return links. Disconnection has a separate owner-only confirmation at
`/bots/{id}/connection/disconnect`; its POST still uses the existing CSRF-protected
lifecycle service. It explains credential removal and retention of identity,
Drafts, publications, chats and Submissions. Storage failures keep a retry form;
activation failures offer inspection/retry and credential repair links. Existing
identity, uniqueness, foreign-webhook and service authorization checks remain.
No migration or production dependency was added.

## Automated verification

`connection_guidance_integration_test.go` uses the App HTTP seam with disposable
migrated SQLite and fake Telegram. It covers focused identity errors and retry,
revoked/unavailable/rejected/forbidden verification for replacement/reconnection,
dedicated disconnection and retention, owner access and harmless GET, storage
failure recovery, studio/deployment guidance, paused reconnection, and activation
recovery. Existing connection, lifecycle, delivery and deployment suites cover
credentials at rest, CSRF/POST, uniqueness, remote conflicts, restart, concurrent
changes, Submissions and immutable publication.

## Browser verification

Start the opt-in fixture from the repository root:

```sh
rtk env PIKO_CONNECTION_BROWSER_ADDR=127.0.0.1:18088 GOFLAGS=-buildvcs=false GOCACHE=/tmp/piko-go-cache go test ./internal/app -run '^TestConnectionBrowserFixture$' -count=1 -v -timeout=20m
```

It creates temporary migrated SQLite, a synthetic owner, an Unconnected Bot and a
saved Builder chat. Its Telegram adapter uses a local fake; no model or real
Telegram calls occur. The ordinary test suite skips the interactive fixture.
From `/tmp`, run the journey on a fresh fixture:

```sh
rtk playwright-cli -s=piko45 open http://127.0.0.1:18088/login
rtk playwright-cli -s=piko45 run-code --filename=/home/pouya/projects/Piko/scripts/connection-browser-check.js
rtk playwright-cli -s=piko45 close
```

Observed on 2026-10-05: initial connection, malformed-token recovery,
wrong-identity replacement and corrected retry, cancel/disconnect/reconnect,
identity retention, empty secret fields, keyboard submission access, and return
to the preserved unsent studio composer passed. Connection and disconnection
screens passed at 1440×1000 and 390×844 in light/dark/system themes, with reduced
motion, no horizontal overflow and no page errors. Expected failed form requests
produced 422. Desktop light and mobile dark screenshots were visually inspected.
Screenshots remain under `/tmp/piko-connection-*.png`.

## Repository validation and review

`make generate`, `make test`, `make lint` and `make build` passed. The final
application suite completed in about 160 seconds. Focused connection/lifecycle,
deployment, navigation, credential recovery and shutdown regressions passed.
Shutdown activation recovery uses its already-authorized Bot snapshot after
request cancellation, preserving failure feedback without a cancelled DB read.

Both review axes checked the implementation against starting commit
`cff6785b17e22c4b062b5e630a0aa81f17c4e789`, including the final recovery corrections.

### Standards

No actionable documented-standard violations or baseline smells were found.

### Spec

No actionable missing/partial requirements, scope creep or incorrect behavior
were found.

Review totals: Standards 0 findings; Spec 0 findings.
