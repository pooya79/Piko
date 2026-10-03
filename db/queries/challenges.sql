-- name: CreateAccountChallenge :exec
INSERT INTO account_challenges (nonce, token_hash, user_id, purpose, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteAccountChallengeForUser :exec
DELETE FROM account_challenges WHERE user_id = $1 AND purpose = $2;

-- name: GetAccountChallengeByNonce :one
SELECT c.nonce, c.user_id, c.purpose, c.expires_at, u.email, u.preferred_language
FROM account_challenges c JOIN users u ON u.id = c.user_id
WHERE c.nonce = $1 AND c.expires_at > now();

-- name: GetAccountChallengeByToken :one
SELECT c.nonce, c.user_id, c.purpose, c.expires_at, u.email
FROM account_challenges c JOIN users u ON u.id = c.user_id
WHERE c.token_hash = $1 AND c.expires_at > now();

-- name: GetAccountChallengeByTokenForUpdate :one
SELECT c.nonce, c.user_id, c.purpose, c.expires_at, u.email
FROM account_challenges c JOIN users u ON u.id = c.user_id
WHERE c.token_hash = $1 AND c.expires_at > now()
FOR UPDATE OF c;

-- name: DeleteAccountChallenge :exec
DELETE FROM account_challenges WHERE nonce = $1;

-- name: DeleteExpiredAccountChallenges :execrows
DELETE FROM account_challenges WHERE expires_at <= now();

-- name: GetAccountEmailRequest :one
SELECT last_requested_at FROM account_email_requests WHERE user_id = $1 AND purpose = $2;

-- name: UpsertAccountEmailRequest :exec
INSERT INTO account_email_requests (user_id, purpose, last_requested_at)
VALUES ($1, $2, now())
ON CONFLICT (user_id, purpose) DO UPDATE SET last_requested_at = EXCLUDED.last_requested_at;

-- name: CreateSignupReceipt :exec
INSERT INTO signup_receipts (token_hash, user_id, expires_at) VALUES ($1, $2, $3);

-- name: GetRecentSignup :one
SELECT u.id, u.email, u.display_name, u.preferred_language
FROM signup_receipts s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now() AND u.email_verified_at IS NULL;

-- name: DeleteExpiredSignupReceipts :execrows
DELETE FROM signup_receipts WHERE expires_at <= now();
