ALTER TABLE bots ADD COLUMN action_revision INTEGER NOT NULL DEFAULT 0;
CREATE TABLE bot_action_proposals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    chat_id INTEGER NOT NULL REFERENCES builder_chats(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL UNIQUE REFERENCES builder_runs(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK(action IN ('deploy','pause','resume')),
    action_revision INTEGER NOT NULL,
    draft_revision INTEGER NOT NULL,
    bot_name TEXT NOT NULL,
    delivery_state TEXT NOT NULL,
    paused INTEGER NOT NULL CHECK(paused IN (0,1)),
    published_version INTEGER NOT NULL,
    receiver TEXT NOT NULL,
    result TEXT NOT NULL DEFAULT 'pending',
    version INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX bot_action_chat ON bot_action_proposals(chat_id,id);
-- Track owner-relevant changes, including ABA transitions. Worker leases,
-- polling offsets and health observations must not expire confirmation cards.
CREATE TRIGGER bot_action_identity AFTER UPDATE OF name, telegram_id, encrypted_token, paused ON bots
WHEN NEW.name IS NOT OLD.name OR NEW.telegram_id IS NOT OLD.telegram_id OR NEW.encrypted_token IS NOT OLD.encrypted_token OR NEW.paused IS NOT OLD.paused
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=NEW.id; END;
CREATE TRIGGER bot_action_draft_insert AFTER INSERT ON bot_drafts
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=NEW.bot_id; END;
CREATE TRIGGER bot_action_draft_update AFTER UPDATE ON bot_drafts
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=NEW.bot_id; END;
CREATE TRIGGER bot_action_draft_delete AFTER DELETE ON bot_drafts
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=OLD.bot_id; END;
CREATE TRIGGER bot_action_publication AFTER INSERT ON bot_publications
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=NEW.bot_id; END;
CREATE TRIGGER bot_action_delivery_insert AFTER INSERT ON bot_delivery
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=NEW.bot_id; END;
CREATE TRIGGER bot_action_delivery_update AFTER UPDATE OF state, mode, encrypted_secret, activation_nonce ON bot_delivery
WHEN NEW.state IS NOT OLD.state OR NEW.mode IS NOT OLD.mode OR NEW.encrypted_secret IS NOT OLD.encrypted_secret OR NEW.activation_nonce IS NOT OLD.activation_nonce
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=NEW.bot_id; END;
CREATE TRIGGER bot_action_delivery_delete AFTER DELETE ON bot_delivery
BEGIN UPDATE bots SET action_revision=action_revision+1 WHERE id=OLD.bot_id; END;
