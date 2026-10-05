-- Refuse a rollback that would discard general conversations. Delete them only
-- by an explicit owner action before choosing to return to the Bot-only schema.
CREATE TEMP TABLE require_associated_chats (valid INTEGER CHECK (valid = 1));
INSERT INTO require_associated_chats SELECT 0 FROM builder_chats WHERE bot_id IS NULL LIMIT 1;
DROP TABLE require_associated_chats;
DROP INDEX builder_one_active_chat;
CREATE TABLE builder_chats_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
INSERT INTO builder_chats_old SELECT id, bot_id, title, created_at, updated_at FROM builder_chats;
-- Retain the allocation high-water mark even when the newest chat was deleted.
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'builder_chats'), 0)) WHERE name = 'builder_chats_old';
DROP TABLE builder_chats;
ALTER TABLE builder_chats_old RENAME TO builder_chats;
CREATE INDEX builder_chats_bot ON builder_chats(bot_id, id);
DROP INDEX bots_id_owner;
CREATE TRIGGER interrupt_deleted_builder_chat
BEFORE DELETE ON builder_chats
BEGIN
    UPDATE builder_runs SET status = 'interrupted',
        finished_at = CAST(strftime('%s', 'now') AS INTEGER)
    WHERE chat_id = OLD.id AND status = 'running';
END;
