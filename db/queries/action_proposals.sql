-- name: GetOwnerActionRevision :one
SELECT action_revision FROM bots WHERE owner_id=sqlc.arg(owner_id) AND id=sqlc.arg(bot_id);

-- name: CreateOwnerActionProposal :execrows
INSERT INTO bot_action_proposals (bot_id,chat_id,run_id,action,action_revision,draft_revision,bot_name,delivery_state,paused,published_version,receiver)
SELECT b.id,c.id,sqlc.arg(run_id),sqlc.arg(action),b.action_revision,sqlc.arg(draft_revision),b.name,sqlc.arg(delivery_state),b.paused,sqlc.arg(published_version),sqlc.arg(receiver)
FROM bots b JOIN builder_chats c ON c.bot_id=b.id AND c.owner_id=b.owner_id
JOIN builder_runs r ON r.id=sqlc.arg(run_id) AND r.owner_id=b.owner_id AND r.bot_id=b.id AND r.chat_id=c.id AND r.status='running'
WHERE b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND c.id=sqlc.arg(chat_id);

-- name: ListOwnerActionProposals :many
SELECT p.*, b.action_revision AS current_revision FROM bot_action_proposals p
JOIN bots b ON b.id=p.bot_id JOIN builder_chats c ON c.id=p.chat_id AND c.owner_id=b.owner_id AND c.bot_id=b.id
WHERE b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND c.id=sqlc.arg(chat_id) ORDER BY p.id;

-- name: GetOwnerActionProposal :one
SELECT p.*, b.action_revision AS current_revision FROM bot_action_proposals p
JOIN bots b ON b.id=p.bot_id JOIN builder_chats c ON c.id=p.chat_id AND c.owner_id=b.owner_id AND c.bot_id=b.id
WHERE b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND p.id=sqlc.arg(proposal_id);

-- name: SaveOwnerActionOutcome :exec
UPDATE bot_action_proposals SET result=sqlc.arg(result), version=sqlc.arg(version)
WHERE bot_action_proposals.id=sqlc.arg(proposal_id) AND bot_action_proposals.bot_id IN (SELECT bots.id FROM bots WHERE bots.owner_id=sqlc.arg(owner_id) AND bots.id=sqlc.arg(bot_id));
