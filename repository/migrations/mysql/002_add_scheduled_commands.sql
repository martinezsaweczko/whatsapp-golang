-- +goose Up
CREATE TABLE IF NOT EXISTS scheduled_commands (
    id INT PRIMARY KEY AUTO_INCREMENT,
    name VARCHAR(255) NOT NULL,
    schedule VARCHAR(255) NOT NULL,
    command VARCHAR(255) NOT NULL,
    group_jid VARCHAR(255) NOT NULL,
    enabled BOOLEAN DEFAULT TRUE,
    created_date DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_scheduled_commands_enabled (enabled)
);

-- +goose Down
DROP TABLE IF EXISTS scheduled_commands;
