-- Token validation now bounds model memory. Keep the independent message-sized
-- storage guard so a concise 2,048-token summary is not rejected at 4,000 chars.
CREATE TABLE builder_summaries_new (
    chat_id INTEGER PRIMARY KEY REFERENCES builder_chats(id) ON DELETE CASCADE,
    through_sequence INTEGER NOT NULL CHECK (through_sequence > 0),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 32768),
    FOREIGN KEY (chat_id, through_sequence) REFERENCES builder_messages(chat_id, sequence)
);
INSERT INTO builder_summaries_new (chat_id, through_sequence, content)
SELECT chat_id, through_sequence, content FROM builder_summaries;
DROP TABLE builder_summaries;
ALTER TABLE builder_summaries_new RENAME TO builder_summaries;
