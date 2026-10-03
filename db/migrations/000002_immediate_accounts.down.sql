-- Restores the old schema, but retired links/jobs and verification status cannot be recovered.
ALTER TABLE users ADD COLUMN email_verified_at INTEGER;
-- Only challenge hashes are stored; the email worker derives links from the nonce.
CREATE TABLE account_challenges (
 nonce BLOB NOT NULL PRIMARY KEY,
 token_hash BLOB NOT NULL UNIQUE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 purpose TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
 expires_at INTEGER NOT NULL,
 created_at INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec') * 1000 AS INTEGER)),
 UNIQUE (user_id, purpose)
);
CREATE INDEX account_challenges_expires_at_idx ON account_challenges(expires_at);

-- Keep mail cooldowns even after a challenge has been consumed.
CREATE TABLE account_email_requests (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 purpose TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
 last_requested_at INTEGER NOT NULL,
 PRIMARY KEY (user_id, purpose)
);

CREATE TABLE signup_receipts (
 token_hash BLOB NOT NULL PRIMARY KEY,
 user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
 expires_at INTEGER NOT NULL,
 created_at INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec') * 1000 AS INTEGER))
);
CREATE INDEX signup_receipts_expires_at_idx ON signup_receipts(expires_at);

-- Jobs store challenge identifiers only; bearer links are derived when sending.
CREATE TABLE email_jobs (
 id INTEGER PRIMARY KEY,
 nonce TEXT NOT NULL UNIQUE,
 attempts INTEGER NOT NULL DEFAULT 0,
 available_at INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec') * 1000 AS INTEGER)),
 lease_token TEXT,
 lease_until INTEGER,
 failed_at INTEGER
);
CREATE INDEX email_jobs_available_idx ON email_jobs(available_at) WHERE failed_at IS NULL;
