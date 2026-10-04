-- name: GetOwnerBuilderSummary :one
SELECT s.* FROM builder_summaries s
JOIN builder_chats c ON c.id=s.chat_id JOIN bots b ON b.id=c.bot_id
WHERE b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND c.id=sqlc.arg(chat_id);

-- name: SaveOwnerBuilderSummary :execrows
INSERT INTO builder_summaries (chat_id, through_sequence, content)
SELECT c.id,sqlc.arg(through_sequence),sqlc.arg(content)
FROM builder_chats c JOIN bots b ON b.id=c.bot_id
WHERE b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND c.id=sqlc.arg(chat_id)
ON CONFLICT(chat_id) DO UPDATE SET through_sequence=excluded.through_sequence,content=excluded.content
WHERE builder_summaries.through_sequence < excluded.through_sequence;

-- name: ListOwnerBuilderUnsummarizedMessages :many
SELECT m.* FROM builder_messages m
JOIN builder_chats c ON c.id=m.chat_id JOIN bots b ON b.id=c.bot_id
WHERE b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND c.id=sqlc.arg(chat_id)
AND m.sequence > sqlc.arg(through_sequence)
ORDER BY m.sequence;
