-- +goose Up
-- Every text message of the groups the bot is in, used as AI context. It
-- replaces the per-sender ai_messages history. created_at holds Unix
-- milliseconds (UTC); the unique key ignores duplicate deliveries of an event.
CREATE TABLE IF NOT EXISTS chat_messages (
    id TEXT PRIMARY KEY,
    chat_jid TEXT NOT NULL,
    message_id TEXT NOT NULL,
    sender_jid TEXT NOT NULL,
    sender_alt_jid TEXT NOT NULL DEFAULT '',
    push_name TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    is_bot INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_context ON chat_messages (chat_jid, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_messages_message ON chat_messages (chat_jid, message_id);

DROP TABLE IF EXISTS ai_messages;

-- +goose Down
CREATE TABLE IF NOT EXISTS ai_messages (
    id TEXT PRIMARY KEY,
    chat_jid TEXT NOT NULL,
    sender_jid TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ai_messages_history ON ai_messages (chat_jid, sender_jid, created_at);

DROP TABLE IF EXISTS chat_messages;
