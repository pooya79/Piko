-- name: GetOwnerBotCredentials :one
SELECT id, owner_id, telegram_id, encrypted_token FROM bots WHERE owner_id = ?1 AND id = ?2;

-- name: GetBotDelivery :one
SELECT * FROM bot_delivery WHERE bot_id = ?1;

-- name: EnsureOwnerDelivery :execrows
INSERT INTO bot_delivery (bot_id, encrypted_secret)
SELECT id, ?3 FROM bots WHERE owner_id = ?1 AND id = ?2
ON CONFLICT (bot_id) DO NOTHING;

-- name: ObserveOwnerDelivery :exec
UPDATE bots SET has_webhook = ?3, pending_updates = ?4, webhook_is_piko = ?5, verified_at = unixepoch()
WHERE owner_id = ?1 AND id = ?2;

-- name: BeginOwnerActivation :execrows
UPDATE bot_delivery SET state = 'activating', mode = ?3, activation_nonce = ?4, activation_until = unixepoch()+30
WHERE bot_id = ?2 AND EXISTS (SELECT 1 FROM bots WHERE bots.id = ?2 AND bots.owner_id = ?1)
AND activation_until < unixepoch() AND worker_until < unixepoch()
AND encrypted_secret = sqlc.arg(expected_secret)
AND EXISTS (SELECT 1 FROM bots WHERE bots.id = ?2 AND bots.telegram_id IS NOT NULL AND length(bots.encrypted_token) > 0 AND bots.encrypted_token = sqlc.arg(expected_token));

-- name: FinishOwnerActivation :execrows
UPDATE bot_delivery SET state = ?3, activation_until = 0
WHERE bot_id = ?2 AND activation_nonce = ?4
AND EXISTS (SELECT 1 FROM bots WHERE bots.id = ?2 AND bots.owner_id = ?1);

-- name: GetIngressCredentials :one
SELECT b.owner_id, b.telegram_id, d.encrypted_secret
FROM bots b JOIN bot_delivery d ON d.bot_id = b.id
WHERE b.id = ?1 AND d.mode = 'webhook' AND d.state IN ('activating','active','error') AND b.telegram_id IS NOT NULL AND length(b.encrypted_token) > 0;

-- name: HasAcceptedUpdate :one
SELECT EXISTS (SELECT 1 FROM bot_updates WHERE bot_id = ?1 AND update_id = ?2);

-- name: AcceptUpdate :execrows
INSERT INTO bot_updates (bot_id, update_id, payload, participant_id, accepted_while_paused)
SELECT ?1, ?2, ?3, ?4, bots.paused FROM bots WHERE bots.id = ?1
AND bots.telegram_id IS NOT NULL AND length(bots.encrypted_token) > 0
AND EXISTS (SELECT 1 FROM bot_delivery WHERE bot_id = ?1 AND state IN ('activating','active','error'))
AND (SELECT COUNT(*) FROM bot_updates WHERE bot_id = ?1 AND complete = 0) < 1000
ON CONFLICT (bot_id, update_id) DO NOTHING;

-- name: ClaimDeliveryWork :one
UPDATE bot_delivery SET worker_nonce = ?1, worker_until = unixepoch()+60
WHERE bot_id = (
 SELECT d.bot_id FROM bot_delivery d
 WHERE d.state = 'active' AND d.mode = ?2 AND d.worker_until < unixepoch() AND d.retry_at <= unixepoch()
 AND (EXISTS (SELECT 1 FROM bot_ready_updates u WHERE u.bot_id = d.bot_id) OR d.mode = 'polling')
 ORDER BY d.retry_at, d.bot_id LIMIT 1
)
RETURNING *;

-- name: GetWorkerBotCredentials :one
SELECT b.owner_id, b.telegram_id, b.encrypted_token FROM bots b JOIN bot_delivery d ON d.bot_id = b.id
WHERE b.id = ?1 AND d.worker_nonce = ?2 AND d.worker_until >= unixepoch() AND d.state = 'active';

-- name: GetNextUpdate :one
SELECT u.* FROM bot_updates u WHERE u.id IN (SELECT ready.id FROM bot_ready_updates ready WHERE ready.bot_id = ?1) ORDER BY u.update_id LIMIT 1;

-- name: ReleaseDeliveryWork :execrows
UPDATE bot_delivery SET worker_until = 0, worker_nonce = '', retry_at = ?3, worker_error = ?4
WHERE bot_id = ?1 AND worker_nonce = ?2;

-- name: RenewDeliveryWork :execrows
UPDATE bot_delivery SET worker_until = unixepoch()+60 WHERE bot_id = ?1 AND worker_nonce = ?2 AND worker_until >= unixepoch() AND state = 'active';

-- name: GetParticipant :one
SELECT p.*, v.definition FROM bot_participants p JOIN bot_publications v ON v.id = p.publication_id
WHERE p.bot_id = ?1 AND p.participant_id = ?2 AND p.expires_at > sqlc.arg(now);

-- name: DeleteExpiredParticipant :one
DELETE FROM bot_participants
WHERE bot_id = ?1 AND participant_id = ?2 AND expires_at <= sqlc.arg(now)
RETURNING interaction;

-- name: SaveParticipant :exec
INSERT INTO bot_participants (bot_id, participant_id, chat_id, publication_id, step_token, interaction, attempt_id, expires_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, sqlc.arg(expires_at))
ON CONFLICT (bot_id, participant_id) DO UPDATE SET chat_id = excluded.chat_id,
publication_id = excluded.publication_id, step_token = excluded.step_token, interaction = excluded.interaction,
attempt_id = excluded.attempt_id, expires_at = excluded.expires_at;

-- name: StageUpdateOutput :execrows
UPDATE bot_updates SET output = ?3, payload = '{}' WHERE id = ?1 AND bot_id = ?2 AND output IS NULL;

-- name: AdvanceUpdateOutput :execrows
UPDATE bot_updates SET cursor = cursor+1, complete = ?4, attempts = 0 WHERE id = ?1 AND bot_id = ?2 AND cursor = ?3 AND complete = 0;

-- name: CompleteIgnoredUpdate :exec
UPDATE bot_updates SET complete = 1 WHERE id = ?1 AND bot_id = ?2;

-- name: RecordUpdateFailure :exec
UPDATE bot_updates SET attempts = MIN(attempts+1, 10), retry_at = ?3 WHERE id = ?1 AND bot_id = ?2;

-- name: FailUndeliverableUpdate :exec
UPDATE bot_updates SET complete = 1, terminal_failure = 1 WHERE id = ?1 AND bot_id = ?2;

-- name: AdvancePollingOffset :execrows
UPDATE bot_delivery SET polling_offset = ?3 WHERE bot_id = ?1 AND worker_nonce = ?2 AND worker_until >= unixepoch() AND state = 'active';

-- name: DeleteExpiredParticipants :execrows
DELETE FROM bot_participants WHERE expires_at <= sqlc.arg(now);

-- name: CompactCompletedUpdateOutput :execrows
UPDATE bot_updates SET output = NULL WHERE complete = 1 AND output IS NOT NULL AND received_at < unixepoch()-86400;

-- name: BeginOwnerLifecycle :execrows
UPDATE bot_delivery SET state = 'inactive', activation_nonce = ?3, activation_until = unixepoch()+90
WHERE bot_id = ?2 AND activation_until < unixepoch()
AND EXISTS (SELECT 1 FROM bots WHERE bots.id = ?2 AND bots.owner_id = ?1
AND (CAST(sqlc.arg(expected_disconnected) AS INTEGER) = -1 OR (length(bots.encrypted_token) = 0) = CAST(sqlc.arg(expected_disconnected) AS INTEGER)));

-- name: FinishOwnerLifecycle :execrows
UPDATE bot_delivery SET activation_until = 0, encrypted_secret = ?4, worker_error = 0, retry_at = 0
WHERE bot_id = ?2 AND activation_nonce = ?3
AND EXISTS (SELECT 1 FROM bots WHERE bots.id = ?2 AND bots.owner_id = ?1);

-- name: SetOwnerCredentials :execrows
UPDATE bots SET encrypted_token = ?3, name = ?4, username = ?5,
has_webhook = ?6, pending_updates = ?7, webhook_is_piko = ?8, verified_at = unixepoch()
WHERE id = ?2 AND owner_id = ?1;

-- name: DeleteOwnerBot :execrows
DELETE FROM bots WHERE owner_id = ?1 AND id = ?2;
