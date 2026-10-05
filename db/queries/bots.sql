-- name: CreateBot :one
INSERT INTO bots (owner_id, telegram_id, name, username, encrypted_token, has_webhook, pending_updates, verified_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
RETURNING id, telegram_id, name, username, has_webhook, pending_updates, verified_at;

-- name: ListOwnerBots :many
SELECT bots.id, bots.telegram_id, bots.name, bots.username, bots.has_webhook, bots.pending_updates, bots.verified_at, bots.webhook_is_piko, bots.paused,
CAST(bots.telegram_id IS NULL AS INTEGER) AS unconnected,
CAST(bots.telegram_id IS NOT NULL AND length(bots.encrypted_token) = 0 AS INTEGER) AS disconnected,
CAST(COALESCE((SELECT MAX(version) FROM bot_publications WHERE bot_id = bots.id), 0) AS INTEGER) AS published_version,
CAST(COALESCE((SELECT state FROM bot_delivery WHERE bot_id = bots.id), 'inactive') AS TEXT) AS delivery_state,
CAST(COALESCE((SELECT mode FROM bot_delivery WHERE bot_id = bots.id), '') AS TEXT) AS delivery_mode,
CAST(COALESCE((SELECT worker_error FROM bot_delivery WHERE bot_id = bots.id), 0) OR EXISTS(SELECT 1 FROM bot_updates WHERE bot_id = bots.id AND ((complete = 0 AND attempts > 0) OR terminal_failure = 1)) AS INTEGER) AS delivery_error
FROM bots WHERE bots.owner_id = ?1 ORDER BY bots.id DESC;

-- name: GetOwnerBot :one
SELECT bots.id, bots.telegram_id, bots.name, bots.username, bots.has_webhook, bots.pending_updates, bots.verified_at, bots.webhook_is_piko, bots.paused,
CAST(bots.telegram_id IS NULL AS INTEGER) AS unconnected,
CAST(bots.telegram_id IS NOT NULL AND length(bots.encrypted_token) = 0 AS INTEGER) AS disconnected,
CAST(COALESCE((SELECT MAX(version) FROM bot_publications WHERE bot_id = bots.id), 0) AS INTEGER) AS published_version,
CAST(COALESCE((SELECT state FROM bot_delivery WHERE bot_id = bots.id), 'inactive') AS TEXT) AS delivery_state,
CAST(COALESCE((SELECT mode FROM bot_delivery WHERE bot_id = bots.id), '') AS TEXT) AS delivery_mode,
CAST(COALESCE((SELECT worker_error FROM bot_delivery WHERE bot_id = bots.id), 0) OR EXISTS(SELECT 1 FROM bot_updates WHERE bot_id = bots.id AND ((complete = 0 AND attempts > 0) OR terminal_failure = 1)) AS INTEGER) AS delivery_error
FROM bots WHERE bots.owner_id = ?1 AND bots.id = ?2;

-- name: SetOwnerBotPaused :execrows
UPDATE bots SET paused = ?3 WHERE bots.owner_id = ?1 AND bots.id = ?2
AND bots.telegram_id IS NOT NULL AND length(bots.encrypted_token) > 0
AND EXISTS (SELECT 1 FROM bot_delivery WHERE bot_id = ?2 AND state = 'active' AND mode = ?4)
AND EXISTS (SELECT 1 FROM bot_publications WHERE bot_id = ?2);

-- name: GetBotPaused :one
SELECT paused FROM bots WHERE id = ?1;

-- name: CreateUnconnectedBot :one
INSERT INTO bots (owner_id, name, username, encrypted_token, has_webhook, pending_updates, verified_at)
VALUES (?1, ?2, '', X'', 0, 0, 0)
RETURNING id;

-- name: DeleteOwnerUnconnectedBot :execrows
DELETE FROM bots WHERE owner_id = ?1 AND id = ?2 AND telegram_id IS NULL;

-- name: ConnectOwnerUnconnectedBot :execrows
UPDATE bots SET telegram_id = ?3, name = ?4, username = ?5, encrypted_token = ?6,
has_webhook = ?7, pending_updates = ?8, verified_at = ?9
WHERE owner_id = ?1 AND id = ?2 AND telegram_id IS NULL;

-- name: GetOwnerBotByTelegramID :one
SELECT id FROM bots WHERE owner_id = ?1 AND telegram_id = ?2;

-- name: RenameOwnerBot :execrows
UPDATE bots SET name = sqlc.arg(name) WHERE owner_id = sqlc.arg(owner_id) AND id = sqlc.arg(bot_id);
