ALTER TABLE bot_participants ADD COLUMN interaction TEXT NOT NULL DEFAULT '{}';
ALTER TABLE bot_participants ADD COLUMN attempt_id TEXT NOT NULL DEFAULT '';
CREATE TABLE bot_submissions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    participant_id INTEGER NOT NULL,
    publication_id INTEGER NOT NULL REFERENCES bot_publications(id),
    attempt_id TEXT NOT NULL,
    answers TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (bot_id, attempt_id)
);
CREATE INDEX bot_submissions_list ON bot_submissions(bot_id, id DESC);
