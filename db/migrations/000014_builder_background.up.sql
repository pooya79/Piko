-- Legacy runs have no reliable request-message association; keep it unknown.
ALTER TABLE builder_runs ADD COLUMN request_sequence INTEGER;

-- Fence work before a Bot's cascading chat deletion removes its resources.
-- Accounting remains attached to the owner after chat_id/bot_id become NULL.
CREATE TRIGGER interrupt_deleted_builder_chat
BEFORE DELETE ON builder_chats
BEGIN
    UPDATE builder_runs SET status = 'interrupted',
        finished_at = CAST(strftime('%s', 'now') AS INTEGER)
    WHERE chat_id = OLD.id AND status = 'running';
END;
