-- Local development only: alice@example.test / DevPassword123!
-- Re-running the seed never changes an existing account's password.
INSERT INTO users (email, display_name, password_hash, email_verified_at) VALUES
 ('alice@example.test', 'Alice Demo', '$argon2id$v=19$m=65536,t=3,p=2$bsr3Iztsm6gJKRgLEE0bsQ$m9ZbPOxJidUMyliJneuy37MYeLsY5Hxfe+R10Y1LuNk', CAST(unixepoch('subsec') * 1000 AS INTEGER))
ON CONFLICT (email) DO NOTHING;
