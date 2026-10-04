-- name: SaveOwnerDraft :one
INSERT INTO bot_drafts (bot_id, definition, updated_at, revision)
SELECT b.id, sqlc.arg(definition), unixepoch(), 1 FROM bots b
LEFT JOIN bot_drafts d ON d.bot_id = b.id
WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id) AND COALESCE(d.revision, 0) = sqlc.arg(expected_revision)
AND sqlc.arg(expected_revision) >= 0 AND sqlc.arg(expected_revision) < 9223372036854775807
ON CONFLICT (bot_id) DO UPDATE SET definition = excluded.definition,
updated_at = excluded.updated_at, revision = bot_drafts.revision + 1
RETURNING revision;

-- name: GetOwnerDraft :one
SELECT definition, revision FROM bot_drafts
JOIN bots ON bots.id = bot_drafts.bot_id
WHERE bots.owner_id = ?1 AND bot_drafts.bot_id = ?2;
