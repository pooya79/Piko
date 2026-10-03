# Domain docs

## Before exploring

Read root `CONTEXT.md` and ADRs in `docs/adr/` relevant to the work.

If `CONTEXT-MAP.md` exists later, follow its pointers to relevant
context glossaries and context-specific ADRs.

When these files are absent, proceed silently. Domain-modeling
creates them lazily when terms or decisions are resolved.

## Layout

This repo uses a single-context layout:

- `CONTEXT.md`: domain glossary.
- `docs/adr/NNNN-description.md`: architectural decisions.

## Vocabulary and decisions

Use glossary terms in issue titles, proposals, hypotheses, and
test names. Reconsider unfamiliar terminology or note a real gap
for domain-modeling.

Explicitly identify any proposal that contradicts an existing ADR,
including the ADR reference and reason for reopening the decision.
