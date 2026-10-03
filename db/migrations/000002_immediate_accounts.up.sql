DROP TABLE email_jobs;
DROP TABLE signup_receipts;
DROP TABLE account_email_requests;
DROP TABLE account_challenges;
ALTER TABLE users DROP COLUMN email_verified_at;
DELETE FROM rate_limits WHERE key LIKE 'rate:account-email:%' OR key LIKE 'rate:account-action:%';
