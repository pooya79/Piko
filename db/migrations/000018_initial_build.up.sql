-- A form submission is durable across conversion and lost HTTP responses.
ALTER TABLE builder_runs ADD COLUMN request_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX builder_request_key ON builder_runs(owner_id, chat_id, request_key) WHERE request_key <> '';
