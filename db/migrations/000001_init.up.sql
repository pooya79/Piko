CREATE TABLE users (
 id BIGSERIAL PRIMARY KEY,
 email TEXT NOT NULL UNIQUE CHECK (email = lower(email)),
 display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 80),
 password_hash TEXT NOT NULL,
 email_verified_at TIMESTAMPTZ,
 preferred_language TEXT NOT NULL DEFAULT 'fa' CHECK (preferred_language IN ('fa', 'en')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
 token_hash BYTEA PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 csrf_hash BYTEA NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

-- Only challenge hashes are stored; the email worker derives links from the nonce.
CREATE TABLE account_challenges (
 nonce BYTEA PRIMARY KEY,
 token_hash BYTEA NOT NULL UNIQUE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 purpose TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE (user_id, purpose)
);
CREATE INDEX account_challenges_expires_at_idx ON account_challenges(expires_at);

-- Keep mail cooldowns even after a challenge has been consumed.
CREATE TABLE account_email_requests (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 purpose TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
 last_requested_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY (user_id, purpose)
);

CREATE TABLE signup_receipts (
 token_hash BYTEA PRIMARY KEY,
 user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX signup_receipts_expires_at_idx ON signup_receipts(expires_at);
