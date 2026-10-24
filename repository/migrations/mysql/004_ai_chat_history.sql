-- +goose Up
-- created_at holds Unix milliseconds (UTC) so range queries do not depend on
-- how the driver serializes time values.
CREATE TABLE IF NOT EXISTS ai_messages (
    id BINARY(16) PRIMARY KEY,
    chat_jid VARCHAR(255) NOT NULL,
    sender_jid VARCHAR(255) NOT NULL,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    INDEX idx_ai_messages_history (chat_jid, sender_jid, created_at)
);

-- +goose Down
DROP TABLE IF EXISTS ai_messages;
