CREATE TABLE builder_chats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX builder_chats_bot ON builder_chats(bot_id, id);

-- Sequence is chat-local and independent of timestamps, including tied timestamps.
CREATE TABLE builder_messages (
    chat_id INTEGER NOT NULL REFERENCES builder_chats(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    role TEXT NOT NULL CHECK (role IN ('owner', 'model', 'result')),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 32768),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, sequence)
);
