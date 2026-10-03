-- name: CreateOwnerPreview :execrows
INSERT INTO bot_previews (id, bot_id, definition, conversation, expires_at)
SELECT sqlc.arg(preview_id), bots.id, sqlc.arg(definition), sqlc.arg(conversation), unixepoch() + 86400
FROM bots WHERE bots.owner_id = sqlc.arg(owner_id) AND bots.id = sqlc.arg(bot_id);

-- name: GetOwnerPreview :one
SELECT bot_previews.id, definition, conversation, revision FROM bot_previews
JOIN bots ON bots.id = bot_previews.bot_id
WHERE bots.owner_id = ?1 AND bot_previews.bot_id = ?2 AND bot_previews.id = ?3 AND expires_at > unixepoch();

-- name: UpdateOwnerPreview :execrows
UPDATE bot_previews SET conversation = sqlc.arg(conversation), revision = revision + 1
WHERE bot_previews.bot_id = sqlc.arg(bot_id) AND bot_previews.id = sqlc.arg(preview_id)
AND revision = sqlc.arg(revision) AND expires_at > unixepoch()
AND EXISTS (SELECT 1 FROM bots WHERE bots.id = bot_previews.bot_id AND bots.owner_id = sqlc.arg(owner_id));

-- name: DeleteExpiredPreviews :execrows
DELETE FROM bot_previews WHERE expires_at <= unixepoch();
