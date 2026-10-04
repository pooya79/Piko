-- Legacy reply-only runs have no Draft change or Undo snapshot.
ALTER TABLE builder_runs ADD COLUMN result TEXT NOT NULL DEFAULT '';
ALTER TABLE builder_runs ADD COLUMN before_definition TEXT;
ALTER TABLE builder_runs ADD COLUMN after_definition TEXT;
ALTER TABLE builder_runs ADD COLUMN after_revision INTEGER;
