-- name: PublishOwnerDraft :one
INSERT INTO bot_publications (bot_id, version, definition)
SELECT b.id, COALESCE((SELECT MAX(version) FROM bot_publications WHERE bot_id = b.id), 0) + 1, d.definition
FROM bots b JOIN bot_drafts d ON d.bot_id = b.id
WHERE b.owner_id = ?1 AND b.id = ?2 AND d.definition = ?3
RETURNING version;

-- name: GetLatestPublication :one
SELECT id, version, definition FROM bot_publications WHERE bot_id = ?1 ORDER BY version DESC LIMIT 1;
