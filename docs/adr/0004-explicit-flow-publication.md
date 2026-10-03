# Publish bot flow changes explicitly

Owners configure and test bot behavior as drafts, then explicitly publish a flow for live Telegram interactions. Saving an edit does not change the published flow, and owners can pause a bot. This separates configuration work from customer interactions at the cost of an extra publication step, which matters as multiple forms and a future builder agent can change bot behavior.

Initial testing uses an interactive Piko preview backed by the same flow engine with isolated test data. A restricted live Telegram testing mode is deferred, avoiding a separate pre-publication participant-access system in the first milestone.

Published flow versions are immutable for the lifetime of their interactions: an unfinished interaction completes against the version it started, while new interactions use the latest published version. Progress survives server restarts and expires after 24 hours of inactivity; pausing a bot retains progress until that normal expiry. Keeping old versions is a deliberate storage cost to avoid changing questions or interpreting answers differently halfway through an interaction.
