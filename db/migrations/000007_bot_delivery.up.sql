ALTER TABLE bots ADD COLUMN webhook_is_piko INTEGER NOT NULL DEFAULT 0 CHECK (webhook_is_piko IN (0,1));
CREATE TABLE bot_delivery (
    bot_id INTEGER PRIMARY KEY REFERENCES bots(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'inactive' CHECK (state IN ('inactive','activating','active','error')),
    mode TEXT NOT NULL DEFAULT 'webhook' CHECK (mode IN ('webhook','polling')),
    encrypted_secret BLOB NOT NULL,
    activation_nonce TEXT NOT NULL DEFAULT '',
    activation_until INTEGER NOT NULL DEFAULT 0,
    worker_nonce TEXT NOT NULL DEFAULT '',
    worker_until INTEGER NOT NULL DEFAULT 0,
    retry_at INTEGER NOT NULL DEFAULT 0,
    polling_offset INTEGER NOT NULL DEFAULT 0,
    worker_error INTEGER NOT NULL DEFAULT 0 CHECK (worker_error IN (0,1))
);
CREATE TABLE bot_updates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    update_id INTEGER NOT NULL,
    participant_id INTEGER NOT NULL,
    payload TEXT NOT NULL,
    output TEXT,
    cursor INTEGER NOT NULL DEFAULT 0,
    complete INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0,1)),
    attempts INTEGER NOT NULL DEFAULT 0,
    retry_at INTEGER NOT NULL DEFAULT 0,
    terminal_failure INTEGER NOT NULL DEFAULT 0 CHECK (terminal_failure IN (0,1)),
    received_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (bot_id, update_id)
);
CREATE INDEX bot_updates_pending ON bot_updates(bot_id, complete, update_id);
CREATE INDEX bot_updates_participant ON bot_updates(bot_id, participant_id, complete, update_id);
CREATE VIEW bot_ready_updates AS
SELECT u.id, u.bot_id FROM bot_updates u
WHERE u.complete = 0 AND u.retry_at <= unixepoch()
AND NOT EXISTS (
    SELECT 1 FROM bot_updates earlier
    WHERE earlier.bot_id = u.bot_id AND earlier.participant_id = u.participant_id
    AND earlier.complete = 0 AND earlier.update_id < u.update_id
);
CREATE TABLE bot_participants (
    bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    participant_id INTEGER NOT NULL,
    chat_id INTEGER NOT NULL,
    publication_id INTEGER NOT NULL REFERENCES bot_publications(id),
    step_token TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (bot_id, participant_id)
);
