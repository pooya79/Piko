-- Accounting survives local history and Bot deletion. Owner deletion removes it.
CREATE TABLE builder_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_id INTEGER REFERENCES bots(id) ON DELETE SET NULL,
    chat_id INTEGER REFERENCES builder_chats(id) ON DELETE SET NULL,
    day TEXT NOT NULL,
    model TEXT NOT NULL,
    draft_revision INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','timeout','interrupted')),
    created_at INTEGER NOT NULL,
    lease_until INTEGER NOT NULL,
    finished_at INTEGER,
    model_calls INTEGER NOT NULL DEFAULT 0 CHECK (model_calls >= 0)
);
CREATE UNIQUE INDEX builder_one_active_bot ON builder_runs(bot_id) WHERE status = 'running';
CREATE INDEX builder_owner_day ON builder_runs(owner_id, day);

CREATE TABLE builder_calls (
    run_id INTEGER NOT NULL REFERENCES builder_runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    input_tokens INTEGER CHECK (input_tokens >= 0),
    output_tokens INTEGER CHECK (output_tokens >= 0),
    total_tokens INTEGER CHECK (total_tokens >= 0),
    cost TEXT,
    PRIMARY KEY (run_id, sequence)
);
