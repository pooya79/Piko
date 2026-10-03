-- name: CreateUser :one
INSERT INTO users (email, display_name, password_hash, preferred_language)
VALUES (?1, ?2, ?3, COALESCE(sqlc.narg(preferred_language), 'fa')) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?1;

-- name: UpdateUserLanguage :execrows
UPDATE users SET preferred_language = ?2 WHERE id = ?1;

-- name: GetAccountByEmail :one
SELECT * FROM users WHERE email = ?1;

-- name: VerifyAccountEmail :execrows
UPDATE users SET email_verified_at = CAST(unixepoch('subsec') * 1000 AS INTEGER) WHERE id = ?1 AND email_verified_at IS NULL;

-- name: DeletePendingAccount :execrows
DELETE FROM users WHERE id = ?1 AND email_verified_at IS NULL;

-- name: UpdateAccountPassword :execrows
UPDATE users SET password_hash = ?2 WHERE id = ?1 AND email_verified_at IS NOT NULL;

