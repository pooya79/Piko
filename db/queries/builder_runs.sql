-- name: CountOwnerBuilderDay :one
SELECT COUNT(*) FROM builder_runs WHERE owner_id = sqlc.arg(owner_id) AND day = sqlc.arg(day);

-- name: ActiveOwnerBuilderBot :one
SELECT COUNT(*) FROM builder_runs WHERE owner_id = sqlc.arg(owner_id) AND bot_id = sqlc.arg(bot_id) AND status = 'running';

-- name: ActiveOwnerBuilderChat :one
SELECT COUNT(*) FROM builder_runs WHERE owner_id = sqlc.arg(owner_id) AND chat_id = sqlc.arg(chat_id) AND status = 'running';

-- name: AdmitOwnerBuilderRun :one
INSERT INTO builder_runs (owner_id,bot_id,chat_id,day,model,draft_revision,status,created_at,lease_until,request_sequence)
SELECT b.owner_id,b.id,c.id,sqlc.arg(day),sqlc.arg(model),sqlc.arg(draft_revision),'running',sqlc.arg(created_at),sqlc.arg(lease_until),sqlc.arg(request_sequence)
FROM builder_chats c JOIN bots b ON b.id=c.bot_id
WHERE b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND c.id=sqlc.arg(chat_id)
RETURNING *;

-- name: GetOwnerInterruptedBuilderRequest :one
SELECT m.content FROM builder_runs r
JOIN builder_chats c ON c.id=r.chat_id
JOIN bots b ON b.id=c.bot_id AND b.id=r.bot_id
JOIN builder_messages m ON m.chat_id=c.id AND m.sequence=r.request_sequence AND m.role='owner'
WHERE r.id=sqlc.arg(run_id) AND r.owner_id=sqlc.arg(owner_id) AND b.owner_id=sqlc.arg(owner_id)
AND b.id=sqlc.arg(bot_id) AND c.id=sqlc.arg(chat_id) AND r.status='interrupted'
AND r.id=(SELECT MAX(latest.id) FROM builder_runs latest WHERE latest.chat_id=c.id);

-- name: FinishOwnerBuilderRun :execrows
UPDATE builder_runs SET status=sqlc.arg(status),finished_at=sqlc.arg(finished_at)
WHERE builder_runs.id=sqlc.arg(run_id) AND builder_runs.owner_id=sqlc.arg(owner_id) AND status='running'
AND lease_until > sqlc.arg(now)
AND EXISTS (SELECT 1 FROM builder_chats c JOIN bots b ON b.id=c.bot_id
    WHERE c.id=builder_runs.chat_id AND b.id=builder_runs.bot_id AND b.owner_id=builder_runs.owner_id);

-- name: StartOwnerBuilderCall :one
UPDATE builder_runs SET model_calls=model_calls+1
WHERE builder_runs.id=sqlc.arg(run_id) AND builder_runs.owner_id=sqlc.arg(owner_id) AND status='running'
AND model_calls < sqlc.arg(max_calls)
AND lease_until > sqlc.arg(now)
AND EXISTS (SELECT 1 FROM builder_chats c JOIN bots b ON b.id=c.bot_id
    WHERE c.id=builder_runs.chat_id AND b.id=builder_runs.bot_id AND b.owner_id=builder_runs.owner_id)
RETURNING model_calls;

-- name: InsertOwnerBuilderCall :exec
INSERT INTO builder_calls (run_id,sequence)
SELECT id,sqlc.arg(sequence) FROM builder_runs WHERE id=sqlc.arg(run_id) AND owner_id=sqlc.arg(owner_id);

-- name: AccountOwnerBuilderCall :exec
UPDATE builder_calls SET input_tokens=sqlc.narg(input_tokens),output_tokens=sqlc.narg(output_tokens),
total_tokens=sqlc.narg(total_tokens),cost=sqlc.narg(cost)
WHERE run_id=sqlc.arg(run_id) AND sequence=sqlc.arg(sequence)
AND EXISTS (SELECT 1 FROM builder_runs r WHERE r.id=builder_calls.run_id AND r.owner_id=sqlc.arg(owner_id));

-- name: ListOwnerBuilderRuns :many
SELECT r.* FROM builder_runs r JOIN bots b ON b.id=r.bot_id
WHERE r.owner_id=sqlc.arg(owner_id) AND b.owner_id=sqlc.arg(owner_id) AND b.id=sqlc.arg(bot_id) AND r.chat_id=sqlc.arg(chat_id)
ORDER BY r.id;

-- name: GetLatestOwnerBuilderRunStatus :one
SELECT r.id,r.status FROM builder_runs r JOIN bots b ON b.id=r.bot_id
WHERE r.owner_id=sqlc.arg(owner_id) AND b.owner_id=sqlc.arg(owner_id)
AND b.id=sqlc.arg(bot_id) AND r.chat_id=sqlc.arg(chat_id)
ORDER BY r.id DESC LIMIT 1;

-- name: ListOwnerBuilderCalls :many
SELECT c.* FROM builder_calls c JOIN builder_runs r ON r.id=c.run_id
WHERE r.owner_id=sqlc.arg(owner_id) AND r.id=sqlc.arg(run_id) ORDER BY c.sequence;

-- name: ListOwnerBuilderDayCalls :many
SELECT c.* FROM builder_calls c JOIN builder_runs r ON r.id=c.run_id
WHERE r.owner_id=sqlc.arg(owner_id) AND r.day=sqlc.arg(day) ORDER BY r.id,c.sequence;

-- name: InterruptBuilderRuns :exec
UPDATE builder_runs SET status='interrupted',finished_at=sqlc.arg(finished_at) WHERE status='running' AND lease_until <= sqlc.arg(now);

-- name: RenewOwnerBuilderRun :execrows
UPDATE builder_runs SET lease_until=sqlc.arg(lease_until)
WHERE builder_runs.id=sqlc.arg(run_id) AND builder_runs.owner_id=sqlc.arg(owner_id) AND status='running' AND lease_until > sqlc.arg(now);
