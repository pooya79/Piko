-- name: IncrementRateLimit :one
INSERT INTO rate_limits (key, count, expires_at)
VALUES (sqlc.arg(key), 1, sqlc.arg(expires_at))
ON CONFLICT (key) DO UPDATE SET
 count = CASE WHEN rate_limits.expires_at <= CAST(unixepoch('subsec') * 1000 AS INTEGER) THEN 1 ELSE rate_limits.count + 1 END,
 expires_at = CASE WHEN rate_limits.expires_at <= CAST(unixepoch('subsec') * 1000 AS INTEGER) THEN excluded.expires_at ELSE rate_limits.expires_at END
RETURNING count, expires_at;

-- name: DeleteExpiredRateLimits :execrows
DELETE FROM rate_limits WHERE expires_at <= CAST(unixepoch('subsec') * 1000 AS INTEGER);
