-- name: CreateBot :one
INSERT INTO bots (owner_id, telegram_id, name, username, encrypted_token, has_webhook, pending_updates, verified_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
RETURNING id, telegram_id, name, username, has_webhook, pending_updates, verified_at;

-- name: ListOwnerBots :many
SELECT id, telegram_id, name, username, has_webhook, pending_updates, verified_at
FROM bots WHERE owner_id = ?1 ORDER BY id DESC;

-- name: GetOwnerBot :one
SELECT id, telegram_id, name, username, has_webhook, pending_updates, verified_at
FROM bots WHERE owner_id = ?1 AND id = ?2;
