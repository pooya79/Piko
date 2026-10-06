-- Bot scope 0 denotes an unassociated Piko chat; storage keeps bot_id NULL.
-- name: CreateOwnerBuilderChat :one
INSERT INTO builder_chats (owner_id, bot_id, title, created_at, updated_at)
SELECT b.owner_id, b.id, sqlc.arg(title), sqlc.arg(created_at), sqlc.arg(created_at)
FROM bots b WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id)
RETURNING *;

-- name: ReserveOwnerBuilderChat :one
INSERT INTO builder_chats (owner_id, bot_id, title, created_at, updated_at, start_key)
SELECT b.owner_id, b.id, sqlc.arg(title), sqlc.arg(created_at), sqlc.arg(created_at), sqlc.arg(start_key)
FROM bots b WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id)
ON CONFLICT(owner_id, start_key) WHERE start_key IS NOT NULL DO UPDATE SET start_key = excluded.start_key
RETURNING *;

-- name: ResolveOwnerChat :one
SELECT * FROM builder_chats WHERE owner_id = sqlc.arg(owner_id) AND id = sqlc.arg(chat_id);

-- name: AssociateOwnerChat :execrows
UPDATE builder_chats SET bot_id = sqlc.arg(bot_id)
WHERE builder_chats.owner_id = sqlc.arg(owner_id) AND builder_chats.id = sqlc.arg(chat_id) AND builder_chats.bot_id IS NULL
AND EXISTS (SELECT 1 FROM bots WHERE id = sqlc.arg(bot_id) AND owner_id = sqlc.arg(owner_id));

-- name: AssociateOwnerChatRuns :exec
UPDATE builder_runs SET bot_id = sqlc.arg(bot_id)
WHERE builder_runs.owner_id = sqlc.arg(owner_id) AND builder_runs.chat_id = sqlc.arg(chat_id) AND builder_runs.bot_id IS NULL
AND EXISTS (SELECT 1 FROM builder_chats c WHERE c.id = builder_runs.chat_id AND c.owner_id = builder_runs.owner_id AND c.bot_id = sqlc.arg(bot_id));

-- name: ListOwnerPikoChats :many
SELECT c.*, COALESCE(b.name, '') AS bot_name FROM builder_chats c
LEFT JOIN bots b ON b.id = c.bot_id AND b.owner_id = c.owner_id
WHERE c.owner_id = sqlc.arg(owner_id)
ORDER BY c.updated_at DESC, c.id DESC;

-- name: GetOwnerBuilderChat :one
SELECT c.* FROM builder_chats c
WHERE c.owner_id = sqlc.arg(owner_id) AND COALESCE(c.bot_id, 0) = CAST(sqlc.arg(bot_id) AS INTEGER) AND c.id = sqlc.arg(chat_id);


-- name: ListOwnerBuilderMessages :many
SELECT m.* FROM builder_messages m
JOIN builder_chats c ON c.id = m.chat_id
WHERE c.owner_id = sqlc.arg(owner_id) AND COALESCE(c.bot_id, 0) = CAST(sqlc.arg(bot_id) AS INTEGER) AND c.id = sqlc.arg(chat_id)
ORDER BY m.sequence;

-- name: AppendOwnerBuilderMessage :one
INSERT INTO builder_messages (chat_id, sequence, role, content, created_at)
SELECT c.id, COALESCE((SELECT MAX(m.sequence) FROM builder_messages m WHERE m.chat_id = c.id), 0) + 1,
sqlc.arg(role), sqlc.arg(content), sqlc.arg(created_at)
FROM builder_chats c
WHERE c.owner_id = sqlc.arg(owner_id) AND COALESCE(c.bot_id, 0) = CAST(sqlc.arg(bot_id) AS INTEGER) AND c.id = sqlc.arg(chat_id)
RETURNING *;

-- name: TouchOwnerBuilderChat :exec
UPDATE builder_chats SET updated_at = sqlc.arg(updated_at)
WHERE builder_chats.id = sqlc.arg(chat_id) AND COALESCE(builder_chats.bot_id, 0) = CAST(sqlc.arg(bot_id) AS INTEGER)
AND builder_chats.owner_id = sqlc.arg(owner_id);

-- name: CreateOwnerPikoChat :one
INSERT INTO builder_chats (owner_id, title, created_at, updated_at)
VALUES (sqlc.arg(owner_id), sqlc.arg(title), sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: ReserveOwnerPikoChat :one
INSERT INTO builder_chats (owner_id, title, created_at, updated_at, start_key)
VALUES (sqlc.arg(owner_id), sqlc.arg(title), sqlc.arg(created_at), sqlc.arg(created_at), sqlc.arg(start_key))
ON CONFLICT(owner_id, start_key) WHERE start_key IS NOT NULL DO UPDATE SET start_key = excluded.start_key
RETURNING *;
