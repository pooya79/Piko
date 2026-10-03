# Issue tracker: GitHub

Issues and specs live in GitHub Issues. Use the `gh` CLI, prefixed
with `rtk`. Infer the repository from the Git remote.

## Conventions

- Create: `rtk gh issue create --title "..." --body-file <file>`.
- Read: `rtk gh issue view <number> --comments`; also fetch labels.
- List: `rtk gh issue list --state open --json number,title,body,labels,comments`.
- Comment: `rtk gh issue comment <number> --body-file <file>`.
- Labels: `rtk gh issue edit <number> --add-label "..." --remove-label "..."`.
- Close: `rtk gh issue close <number> --comment "..."`.

Write multiline bodies to a temporary file and pass `--body-file`.
“Publish to the issue tracker” means create a GitHub issue.
“Fetch the relevant ticket” means read the issue and its comments.

## Pull requests as a triage surface

**PRs as a request surface: no.**

If enabled later, use the equivalent `gh pr` operations and include
the diff when reading a PR. Triage external contributions from
CONTRIBUTOR, FIRST_TIME_CONTRIBUTOR, or NONE; exclude OWNER, MEMBER,
and COLLABORATOR.

GitHub shares numbering between issues and PRs. Resolve ambiguous
references with `gh pr view`, falling back to `gh issue view`.

## Wayfinding operations

- Map: one issue labelled `wayfinder:map`, containing Notes,
  Decisions-so-far, and Fog.
- Child tickets: link as GitHub sub-issues. If unavailable, use a
  task list in the map and `Part of #<map>` in each child.
- Types: `wayfinder:research`, `wayfinder:prototype`,
  `wayfinder:grilling`, or `wayfinder:task`.
- Blocking: use native GitHub issue dependencies through `gh api`.
  Dependency edges require the blocker’s numeric database ID.
  If unavailable, record `Blocked by: #<number>` in the child.
- Frontier: choose the first open child in map order with no open
  blockers and no assignee.
- Claim: assign the ticket to `@me`.
- Resolve: comment with the answer, close the ticket, and append
  a summary and link to the map’s Decisions-so-far.
