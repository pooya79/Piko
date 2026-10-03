-- name: CreateSession :execrows
INSERT INTO sessions (token_hash, user_id, csrf_hash, expires_at)
SELECT $1, id, $3, $4 FROM users WHERE id = $2;

-- name: GetSession :one
SELECT s.user_id, s.csrf_hash, s.expires_at, u.email, u.display_name, u.email_verified_at, u.preferred_language
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteAccountSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();

-- name: UpdateSessionCSRF :execrows
UPDATE sessions SET csrf_hash = $2 WHERE token_hash = $1 AND expires_at > now();
