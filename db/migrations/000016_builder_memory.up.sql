CREATE TABLE builder_summaries (
    chat_id INTEGER PRIMARY KEY REFERENCES builder_chats(id) ON DELETE CASCADE,
    through_sequence INTEGER NOT NULL CHECK (through_sequence > 0),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 4000),
    FOREIGN KEY (chat_id, through_sequence) REFERENCES builder_messages(chat_id, sequence)
);
