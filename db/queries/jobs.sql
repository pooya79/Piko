-- name: EnqueueAccountEmail :exec
INSERT INTO email_jobs (nonce) VALUES (?) ON CONFLICT (nonce) DO NOTHING;

-- name: ClaimAccountEmail :one
UPDATE email_jobs
SET attempts = attempts + 1,
    lease_token = sqlc.arg(lease_token), lease_until = sqlc.arg(lease_until)
WHERE id = (
 SELECT queued.id FROM email_jobs AS queued
 WHERE queued.failed_at IS NULL AND queued.available_at <= sqlc.arg(now)
 AND (queued.lease_until IS NULL OR queued.lease_until <= sqlc.arg(now))
 ORDER BY queued.available_at, queued.id LIMIT 1
)
RETURNING *;

-- name: CompleteAccountEmail :execrows
DELETE FROM email_jobs WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: RetryAccountEmail :execrows
UPDATE email_jobs SET available_at = sqlc.arg(available_at), failed_at = sqlc.narg(failed_at), lease_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token);

-- name: DeleteOldFailedEmails :execrows
DELETE FROM email_jobs WHERE failed_at <= sqlc.arg(before);
