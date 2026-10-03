-- name: CreateBot :one
INSERT INTO bots (owner_id, telegram_id, name, username, encrypted_token, has_webhook, pending_updates, verified_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
RETURNING id, telegram_id, name, username, has_webhook, pending_updates, verified_at;

-- name: ListOwnerBots :many
SELECT bots.id, bots.telegram_id, bots.name, bots.username, bots.has_webhook, bots.pending_updates, bots.verified_at, bots.webhook_is_piko,
CAST(COALESCE((SELECT MAX(version) FROM bot_publications WHERE bot_id = bots.id), 0) AS INTEGER) AS published_version,
CAST(COALESCE((SELECT state FROM bot_delivery WHERE bot_id = bots.id), 'inactive') AS TEXT) AS delivery_state,
CAST(COALESCE((SELECT worker_error FROM bot_delivery WHERE bot_id = bots.id), 0) OR EXISTS(SELECT 1 FROM bot_updates WHERE bot_id = bots.id AND ((complete = 0 AND attempts > 0) OR terminal_failure = 1)) AS INTEGER) AS delivery_error
FROM bots WHERE bots.owner_id = ?1 ORDER BY bots.id DESC;

-- name: GetOwnerBot :one
SELECT bots.id, bots.telegram_id, bots.name, bots.username, bots.has_webhook, bots.pending_updates, bots.verified_at, bots.webhook_is_piko,
CAST(COALESCE((SELECT MAX(version) FROM bot_publications WHERE bot_id = bots.id), 0) AS INTEGER) AS published_version,
CAST(COALESCE((SELECT state FROM bot_delivery WHERE bot_id = bots.id), 'inactive') AS TEXT) AS delivery_state,
CAST(COALESCE((SELECT worker_error FROM bot_delivery WHERE bot_id = bots.id), 0) OR EXISTS(SELECT 1 FROM bot_updates WHERE bot_id = bots.id AND ((complete = 0 AND attempts > 0) OR terminal_failure = 1)) AS INTEGER) AS delivery_error
FROM bots WHERE bots.owner_id = ?1 AND bots.id = ?2;
