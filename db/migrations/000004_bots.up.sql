CREATE TABLE bots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    telegram_id INTEGER NOT NULL UNIQUE CHECK (telegram_id > 0),
    name TEXT NOT NULL,
    username TEXT NOT NULL,
    encrypted_token BLOB NOT NULL,
    has_webhook INTEGER NOT NULL CHECK (has_webhook IN (0, 1)),
    pending_updates INTEGER NOT NULL CHECK (pending_updates >= 0),
    verified_at INTEGER NOT NULL
);
CREATE INDEX bots_owner_id ON bots(owner_id, id);
