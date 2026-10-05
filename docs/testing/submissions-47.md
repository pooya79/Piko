# Submission workspace (#47)

The Bot overview and shared Bot navigation lead into a paginated inbox of real
confirmed Submissions. List rows show saved answer summaries, Telegram Participant
identifiers, published Flow version and receipt date. Detail breadcrumbs link back
through the inbox; the answer panel explains that questions and answers retain
their original published version. Older pages offer a return to the latest records,
and an exhausted cursor has a distinct empty state.

Deletion opens `/bots/{botID}/submissions/{submissionID}/delete`, identifying the
Bot and selected record with a saved summary and metadata. Explicit confirmation
submits to the existing owner-authorized POST/CSRF action; cancellation returns to
the detail. Copy describes permanent removal of that Submission and preservation
of neighboring records, settings, Telegram credentials, Drafts and ongoing
Interactions. The existing runtime's completed-attempt and Telegram retry guards
remain in place.

Views use the approved workspace fonts, theme roles, rounded surfaces and official
Phosphor icons. Escaped answers and labels use `bdi`; numeric Participant identifiers
use LTR isolation. Responsive list/detail/confirmation layouts and visible keyboard
focus use the existing Go/templ stack.

## Application verification

`submission_integration_test.go` exercises the real application HTTP boundary,
migrated temporary SQLite and fake Telegram. It covers 51 confirmed attempts with
a 50-record first page and one older record, cursor boundaries and invalid values,
owner navigation, escaped answers, original question labels and Flow version after
publication, dedicated confirmation, cancellation by harmless GET, anonymous and
cross-owner access, mismatched Bot IDs, POST/CSRF, missing records, storage/read
failures, deletion during an ongoing Interaction, and preserved workspace data.
Existing deletion tests retain neighboring records and reject both exact Telegram
retries and obsolete confirmation callbacks after restart. Inquiry, Registration,
Booking request and studio Preview regressions cover Preview isolation.

## Browser verification

Start the disposable fixture from the repository root:

```sh
rtk env PIKO_SUBMISSION_BROWSER_ADDR=127.0.0.1:8097 GOCACHE=/tmp/piko-go-cache GOFLAGS=-buildvcs=false go test ./internal/app -run '^TestSubmissionBrowserFixture$' -count=1 -v -timeout=30m
```

From `/tmp`, run on a fresh fixture:

```sh
rtk /home/pouya/.codex/skills/playwright/scripts/playwright_cli.sh --session=piko-submissions open http://127.0.0.1:8097/login
rtk /home/pouya/.codex/skills/playwright/scripts/playwright_cli.sh --session=piko-submissions run-code --filename=/home/pouya/projects/Piko/scripts/submission-browser-check.js
rtk /home/pouya/.codex/skills/playwright/scripts/playwright_cli.sh --session=piko-submissions close
```

Observed on 2026-10-05: the owner journey passed at 1440×1000 and 390×844 in
light/dark/system themes. Checks covered overview-to-inbox navigation, pagination,
frozen detail after publication, literal script-like answer text, Participant
isolation, dedicated confirmation and cancellation, visible keyboard focus,
keyboard deletion, preserved neighboring detail/Draft/connection, empty inbox and
exhausted cursor, reduced motion, and invalid/missing record errors. No horizontal
overflow or JavaScript page errors occurred. Expected missing records returned 404
and an invalid cursor returned 400.

Screenshots remain under `/tmp/piko-submissions-{desktop,mobile}-{light,dark,system}-{inbox,detail,delete,empty}.png`.
Desktop light inbox, mobile dark detail, desktop dark confirmation, mobile light
inbox and empty-state screenshots were visually inspected. Error/end-state images
are `/tmp/piko-submissions-empty-cursor.png` and
`/tmp/piko-submissions-invalid-cursor.png`.

All browser records were produced by the real runtime against fake Telegram with
a temporary database. Existing databases and installed skills were preserved.

## Repository validation

`make generate`, `make test`, `make lint` and `make build` passed. The full
application suite completed in 188.5 seconds. Focused Submission regressions
passed; lint's hidden Persian separator findings were resolved with explicit
Unicode escapes in test literals.

## Review

Two code-review agents reviewed changes against starting commit
`e7796001ea40cd8d8c08e4c30309bda8eba4360f` before committing.

### Standards

No actionable documented-standard breaches or baseline smells were found.

### Spec

No actionable missing requirements, scope creep or incorrect behavior were found.

Review totals: Standards 0 findings; Spec 0 findings.
