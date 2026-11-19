-- +goose Up
-- Every text message of the groups the bot is in, used as AI context. It
-- replaces the per-sender ai_messages history. created_at holds Unix
-- milliseconds (UTC); the unique key ignores duplicate deliveries of an event.
-- body is MEDIUMTEXT because a WhatsApp text can exceed TEXT's 64 KiB in utf8mb4.
CREATE TABLE IF NOT EXISTS chat_messages (
    id BINARY(16) PRIMARY KEY,
    chat_jid VARCHAR(255) NOT NULL,
    message_id VARCHAR(128) NOT NULL,
    sender_jid VARCHAR(255) NOT NULL,
    sender_alt_jid VARCHAR(255) NOT NULL DEFAULT '',
    push_name VARCHAR(255) NOT NULL DEFAULT '',
    body MEDIUMTEXT NOT NULL,
    is_bot BOOLEAN NOT NULL DEFAULT FALSE,
    created_at BIGINT NOT NULL,
    INDEX idx_chat_messages_context (chat_jid, created_at),
    UNIQUE INDEX idx_chat_messages_message (chat_jid, message_id)
);

DROP TABLE IF EXISTS ai_messages;

-- +goose Down
CREATE TABLE IF NOT EXISTS ai_messages (
    id BINARY(16) PRIMARY KEY,
    chat_jid VARCHAR(255) NOT NULL,
    sender_jid VARCHAR(255) NOT NULL,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    INDEX idx_ai_messages_history (chat_jid, sender_jid, created_at)
);

DROP TABLE IF EXISTS chat_messages;
