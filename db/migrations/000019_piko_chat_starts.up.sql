-- Reserve the welcome submission's conversation before admitting its first run.
ALTER TABLE builder_chats ADD COLUMN start_key TEXT;
CREATE UNIQUE INDEX piko_chat_start_key ON builder_chats(owner_id, start_key) WHERE start_key IS NOT NULL;
