-- name: CreateSubmission :exec
INSERT INTO bot_submissions (bot_id, participant_id, publication_id, attempt_id, answers)
VALUES (?1, ?2, ?3, ?4, ?5)
ON CONFLICT (bot_id, attempt_id) DO NOTHING;

-- name: ListOwnerSubmissions :many
SELECT s.id, s.participant_id, s.created_at, s.answers, v.version FROM bot_submissions s
JOIN bots b ON b.id = s.bot_id JOIN bot_publications v ON v.id = s.publication_id
WHERE b.owner_id = ?1 AND s.bot_id = ?2 AND s.id < sqlc.arg(before_id)
ORDER BY s.id DESC LIMIT 51;

-- name: GetOwnerSubmission :one
SELECT s.*, v.version FROM bot_submissions s
JOIN bots b ON b.id = s.bot_id JOIN bot_publications v ON v.id = s.publication_id
WHERE b.owner_id = ?1 AND s.bot_id = ?2 AND s.id = ?3;

-- name: DeleteOwnerSubmission :execrows
DELETE FROM bot_submissions
WHERE bot_submissions.bot_id = sqlc.arg(bot_id) AND bot_submissions.id = sqlc.arg(id)
AND EXISTS (SELECT 1 FROM bots WHERE bots.id = bot_submissions.bot_id AND bots.owner_id = sqlc.arg(owner_id));
