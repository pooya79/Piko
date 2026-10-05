-- Retain selected-Block identity for explicit interrupted-run recovery.
ALTER TABLE builder_runs ADD COLUMN selected_block TEXT NOT NULL DEFAULT '';
ALTER TABLE builder_runs ADD COLUMN selected_revision INTEGER NOT NULL DEFAULT 0 CHECK (selected_revision >= 0);
