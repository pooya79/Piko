-- Independent account ownership preserves every existing chat ID and child row.
-- The migration runner disables foreign keys during this atomic table rebuild.
CREATE UNIQUE INDEX bots_id_owner ON bots(id, owner_id);
CREATE TABLE piko_chats_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_id INTEGER REFERENCES bots(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (bot_id, owner_id) REFERENCES bots(id, owner_id) ON DELETE CASCADE
);
INSERT INTO piko_chats_new (id, owner_id, bot_id, title, created_at, updated_at)
SELECT c.id, b.owner_id, c.bot_id, c.title, c.created_at, c.updated_at
FROM builder_chats c JOIN bots b ON b.id = c.bot_id;
-- Retain the allocation high-water mark even when the newest chat was deleted.
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'builder_chats'), 0)) WHERE name = 'piko_chats_new';
DROP TABLE builder_chats;
ALTER TABLE piko_chats_new RENAME TO builder_chats;
CREATE INDEX builder_chats_bot ON builder_chats(bot_id, id);
CREATE INDEX builder_chats_owner ON builder_chats(owner_id, id);
CREATE UNIQUE INDEX builder_one_active_chat ON builder_runs(chat_id) WHERE status = 'running';
CREATE TRIGGER interrupt_deleted_builder_chat
BEFORE DELETE ON builder_chats
BEGIN
    UPDATE builder_runs SET status = 'interrupted',
        finished_at = CAST(strftime('%s', 'now') AS INTEGER)
    WHERE chat_id = OLD.id AND status = 'running';
END;
