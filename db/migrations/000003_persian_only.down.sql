-- Retired choices cannot be recovered; restored accounts default to Persian.
ALTER TABLE users ADD COLUMN preferred_language TEXT NOT NULL DEFAULT 'fa' CHECK (preferred_language IN ('fa', 'en'));
