ALTER TABLE bots ADD COLUMN paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0,1));
ALTER TABLE bot_updates ADD COLUMN accepted_while_paused INTEGER NOT NULL DEFAULT 0 CHECK (accepted_while_paused IN (0,1));
