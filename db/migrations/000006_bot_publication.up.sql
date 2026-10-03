CREATE TABLE bot_publications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    definition TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (bot_id, version)
);
CREATE TRIGGER immutable_bot_publication BEFORE UPDATE ON bot_publications
BEGIN SELECT RAISE(ABORT, 'Published flow versions are immutable'); END;
