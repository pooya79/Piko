-- Existing snapshots cannot be reliably matched to a past Draft revision.
-- Zero records an unknown source; preserve their definitions and progress.
ALTER TABLE bot_previews ADD COLUMN source_revision INTEGER NOT NULL DEFAULT 0 CHECK (source_revision >= 0);
