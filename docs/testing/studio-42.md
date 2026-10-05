# Committed Draft changes and guarded Undo (#42)

The Changes tab shows this chat's stored Builder outcomes and differences from
persisted before/after Draft definitions. It never interprets assistant claims or
streamed candidates as saved changes. Entries identify the Bot, current Draft
revision, run and original before/after revisions. Informational replies,
identical saved content, failures, invalid proposals, Stop and conflicts retain
their distinct feedback. Successful initial creation shows the actual additions
without offering Undo; a failed initial creation has neither saved changes nor
an Undo action. Undone entries explicitly describe historical changes.

The comparison uses approved Flow identities, with Question IDs scoped to their
Form. It reports additions, removals, changed settings and changes to the relative
order of retained menu choices, messages, Forms and Questions. Expandable entries
show before/after values, escaped and direction-isolated. History, the committed
Draft used for Flow inspection, and revision-based Undo availability are read in
one SQLite transaction, preventing a workspace assembled from different Draft
snapshots. There is no new schema or dependency.

Undo reuses the existing owner-only POST/CSRF action and transactional revision
guard. A manual save or another chat's save makes an older action unavailable;
the pane explains why and stale POSTs still return a conflict. Successful Undo
refreshes the workspace and marks retained Preview snapshots stale. Published
Flows, Telegram activation and paused state remain under their existing actions.

## Automated verification

The application HTTP seam covers truthful diffs despite contradictory assistant
prose, scoped Question settings and ordering, escaped text, informational and
identical content, failed/invalid candidates, rollback, provisional results,
stale-result conflicts, initial creation and initial failures. The existing Undo
suite covers owner authorization, POST/CSRF, intervening manual/other-chat edits,
restarts, repeat actions, races, rollback and separation from live behavior.
The Changes test also checks the committed Undo fragment and retained Preview
staleness.

## Browser verification

Use the isolated environment in [studio-38.md](studio-38.md), with a newly created
temporary database. Start the deterministic `studio-browser-provider.mjs`
fixture, migrate the database, then start the app. From `/tmp`:

```sh
rtk playwright-cli -s=piko42 open http://127.0.0.1:18088/register
rtk playwright-cli -s=piko42 run-code --filename=/home/mint/oss/buildx/scripts/studio-changes-browser-check.js
rtk playwright-cli -s=piko42 close
```

Observed on 2026-10-05 at 1440×1000 and 390×844: initial and later committed
changes, provisional isolation, Undo, stale Undo rejection, Preview identity and
staleness, preserved unsent text/caret/focus, expanded changes, pane switching,
mobile layout, light/dark/system themes and reduced motion passed. There were no
page errors or horizontal document overflow. The expected stale Undo POST
returned 409. Screenshots were inspected at:

- `/tmp/piko-changes-desktop-light.png`
- `/tmp/piko-changes-mobile-dark.png`

Additional light/dark/system screenshots are under `/tmp/piko-changes-*`.
Browser verification uses synthetic local data and establishes no live model or
Telegram behavior. Existing databases were preserved.

## Repository validation

`make generate`, `make test`, `make lint` and `make build` passed, along with the
focused HTTP and browser checks. The full application suite completed in about
144 seconds. After review, focused Changes and Undo regressions and lint were
rerun for the same-chat guard explanation correction.

## Review

Both axes reviewed `be9d411b27a4b176f7e701e4c4296bc45d6e09ee...HEAD`.

### Standards

No actionable violations of repository standards or baseline smells were found.

### Spec

The review found one copy issue: the unavailable Undo explanation omitted newer
requests in the same chat. The copy now covers manual saves, newer requests in
this or another chat, and prior Undo. An HTTP regression checks that an older run
loses its action with this explanation while the newest run remains eligible.
No material omissions or scope creep were found.
