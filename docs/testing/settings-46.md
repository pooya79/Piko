# Bot settings, manual configuration and deletion (#46)

Settings use the existing studio fonts, surfaces, borders and accent with sections
for the workspace name, manual Draft configuration, Telegram connection and
permanent deletion. Name validation retains the entered value and field focus;
successful rename returns to settings with feedback. Credential entry remains on
its dedicated connection screen.

The manual editor retains the existing menu/Form schema, parsing, validation and
shared Draft revision guard. Section links and a sticky save control make long
Forms easier to navigate. Native validation opens collapsed Form/Question
containers before focusing an invalid field. Manual saves remain unpublished;
studio inspection and a fresh Preview load the committed revision.

`/bots/{botID}/settings/delete` is an owner-authorized confirmation screen. It
identifies the Bot and lists Drafts, published versions, Previews, participant
data, credentials and all attached Builder chats, including earlier general
discussion. The unchecked required confirmation submits to the existing POST/CSRF
deletion action. Validation and storage errors retain recovery controls. Copy
states that unrelated general chats, other Bots, Telegram identity and account
usage records remain. Chat deletion identifies the selected chat and explains
that committed Draft changes and the Bot remain.

No schema, runtime Block, dependency or database replacement was introduced.

## Verification

Application HTTP tests cover name ownership, POST/CSRF, malformed and invalid
names, saved feedback, retained manual Forms/menu order, stale saves, unpublished
state, studio inspection and fresh Preview. Deletion tests cover confirmation
ownership/recovery and the removal of converted discussion and all attached
chats while preserving independent history and other Bots. Existing regressions
cover concurrent revisions, migrations, cancellation of active work, retained
accounting, and chat deletion without reverting committed Drafts.

For browser QA, run the opt-in fixture with a disposable migrated database:

```sh
rtk env PIKO_SETTINGS_BROWSER_ADDR=127.0.0.1:18090 GOCACHE=/tmp/piko-go-cache GOFLAGS=-buildvcs=false go test ./internal/app -run '^TestConnectionBrowserFixture$' -count=1 -timeout=30m -v
```

From `/tmp`, run:

```sh
rtk playwright-cli -s=piko46 open http://127.0.0.1:18090/login
rtk playwright-cli -s=piko46 run-code --filename=/home/pouya/projects/Piko/scripts/settings-browser-check.js
rtk playwright-cli -s=piko46 close
```

Observed on 2026-10-05: 1440×1000 desktop and 390×844 mobile RTL passed in
light/dark/system themes. Checks included keyboard name save, error focus, all
three supported Forms, validation in collapsed Questions, two-editor revision
conflict, visible focused fields above the save bar, unchecked confirmation,
cancel, chat-only deletion, Bot deletion and preserved unrelated work. Reduced
motion and system-dark settings were exercised. No page errors or horizontal
overflow occurred; the expected invalid-name and stale-save requests returned
422 and 409.

Screenshots were inspected at `/tmp/piko-settings-desktop-light-settings.png`,
`/tmp/piko-settings-mobile-dark-delete.png` and
`/tmp/piko-settings-mobile-light-manual.png`. Additional theme/layout screenshots
and a full long-form image are under `/tmp/piko-settings-*`.

All browser data and service responses were synthetic. Existing databases and
installed skills were preserved.

## Repository validation

`make generate`, `make test`, `make lint` and `make build` passed, along with
focused HTTP regressions and browser QA. The full application suite completed in
158.6 seconds. Its first run identified an existing conversion test that still
looked for deletion copy on settings; that test now follows the dedicated
confirmation screen, retaining the original earlier-discussion assertion. The
focused conversion regression, lint and full suite then passed.

## Review

Two independent code-review agents reviewed the staged changes against
`e526ba57fb994be6eba79f5d6f7b4ed179c8c2cf`, the commit before this implementation.

### Standards

No actionable documented-standard violations or baseline smells were found.

### Spec

No actionable omissions, scope creep or incorrect behavior were found. The
review also checked the follow-up regression-test destination change to the
dedicated confirmation screen; its earlier-discussion assertion remains intact.

Standards: 0 findings. Spec: 0 findings.
