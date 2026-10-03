-- name: SaveOwnerDraft :execrows
INSERT INTO bot_drafts (bot_id, definition, updated_at)
SELECT id, ?3, unixepoch() FROM bots WHERE owner_id = ?1 AND id = ?2
ON CONFLICT (bot_id) DO UPDATE SET definition = excluded.definition, updated_at = excluded.updated_at;

-- name: GetOwnerDraft :one
SELECT definition FROM bot_drafts
JOIN bots ON bots.id = bot_drafts.bot_id
WHERE bots.owner_id = ?1 AND bot_drafts.bot_id = ?2;
