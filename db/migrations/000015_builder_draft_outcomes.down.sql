-- Keep Drafts, histories and accounting; only retire outcome/Undo metadata.
ALTER TABLE builder_runs DROP COLUMN after_revision;
ALTER TABLE builder_runs DROP COLUMN after_definition;
ALTER TABLE builder_runs DROP COLUMN before_definition;
ALTER TABLE builder_runs DROP COLUMN result;
