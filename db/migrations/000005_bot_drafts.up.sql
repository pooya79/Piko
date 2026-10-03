CREATE TABLE bot_drafts (
 bot_id INTEGER PRIMARY KEY REFERENCES bots(id) ON DELETE CASCADE,
 definition TEXT NOT NULL,
 updated_at INTEGER NOT NULL
);

-- Test state is deliberately separate from future live Interactions/Submissions.
CREATE TABLE bot_previews (
 id TEXT PRIMARY KEY,
 bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
 definition TEXT NOT NULL,
 conversation TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,
 expires_at INTEGER NOT NULL
);
CREATE INDEX bot_previews_expiration ON bot_previews(expires_at);
