-- name: CreateUser :one
INSERT INTO users (email, display_name, password_hash, preferred_language)
VALUES (?1, ?2, ?3, COALESCE(sqlc.narg(preferred_language), 'fa')) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?1;

-- name: UpdateUserLanguage :execrows
UPDATE users SET preferred_language = ?2 WHERE id = ?1;

