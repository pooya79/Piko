-- name: CreateOwnerBuilderChat :one
INSERT INTO builder_chats (bot_id, title, created_at, updated_at)
SELECT b.id, sqlc.arg(title), sqlc.arg(created_at), sqlc.arg(created_at)
FROM bots b WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id)
RETURNING *;

-- name: ListOwnerBuilderChats :many
SELECT c.* FROM builder_chats c JOIN bots b ON b.id = c.bot_id
WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id)
ORDER BY c.id DESC;

-- name: GetOwnerBuilderChat :one
SELECT c.* FROM builder_chats c JOIN bots b ON b.id = c.bot_id
WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id) AND c.id = sqlc.arg(chat_id);

-- name: DeleteOwnerBuilderChat :execrows
DELETE FROM builder_chats WHERE builder_chats.id = sqlc.arg(chat_id) AND builder_chats.bot_id = sqlc.arg(bot_id)
AND EXISTS (SELECT 1 FROM bots b WHERE b.id = builder_chats.bot_id AND b.owner_id = sqlc.arg(owner_id));

-- name: ListOwnerBuilderMessages :many
SELECT m.* FROM builder_messages m
JOIN builder_chats c ON c.id = m.chat_id JOIN bots b ON b.id = c.bot_id
WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id) AND c.id = sqlc.arg(chat_id)
ORDER BY m.sequence;

-- name: AppendOwnerBuilderMessage :one
INSERT INTO builder_messages (chat_id, sequence, role, content, created_at)
SELECT c.id, COALESCE((SELECT MAX(m.sequence) FROM builder_messages m WHERE m.chat_id = c.id), 0) + 1,
sqlc.arg(role), sqlc.arg(content), sqlc.arg(created_at)
FROM builder_chats c JOIN bots b ON b.id = c.bot_id
WHERE b.owner_id = sqlc.arg(owner_id) AND b.id = sqlc.arg(bot_id) AND c.id = sqlc.arg(chat_id)
RETURNING *;

-- name: TouchOwnerBuilderChat :exec
UPDATE builder_chats SET updated_at = sqlc.arg(updated_at)
WHERE builder_chats.id = sqlc.arg(chat_id) AND builder_chats.bot_id = sqlc.arg(bot_id)
AND EXISTS (SELECT 1 FROM bots b WHERE b.id = builder_chats.bot_id AND b.owner_id = sqlc.arg(owner_id));
