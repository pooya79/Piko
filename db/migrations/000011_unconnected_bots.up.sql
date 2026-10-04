-- The migrator disables foreign keys on its dedicated connection before the
-- transaction, and checks them before commit, so child data survives this rebuild.
CREATE TABLE bots_rebuilt (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 telegram_id INTEGER  UNIQUE CHECK (telegram_id > 0),
 name TEXT NOT NULL,
 username TEXT NOT NULL,
 encrypted_token BLOB NOT NULL,
 has_webhook INTEGER NOT NULL CHECK (has_webhook IN (0,1)),
 pending_updates INTEGER NOT NULL CHECK (pending_updates >= 0),
 verified_at INTEGER NOT NULL,
 webhook_is_piko INTEGER NOT NULL DEFAULT 0 CHECK (webhook_is_piko IN (0,1)),
 paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0,1)),
 CHECK (telegram_id IS NOT NULL OR (username = '' AND length(encrypted_token) = 0 AND has_webhook = 0 AND pending_updates = 0 AND verified_at = 0 AND webhook_is_piko = 0 AND paused = 0))
);
-- Preserve the high-water mark even if the latest Bot was deleted.
CREATE TEMP TABLE bot_sequence AS SELECT seq FROM sqlite_sequence WHERE name = 'bots';
INSERT INTO bots_rebuilt SELECT * FROM bots;
DROP TABLE bots;
ALTER TABLE bots_rebuilt RENAME TO bots;
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM bot_sequence), 0)) WHERE name = 'bots';
DROP TABLE bot_sequence;
CREATE INDEX bots_owner_id ON bots(owner_id, id);
