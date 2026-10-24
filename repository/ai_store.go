package repository

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
)

// SaveAIMessage stores one AI conversation turn. A zero CreatedAt means now.
func (d *DB) SaveAIMessage(ctx context.Context, msg model.AIMessage) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO ai_messages (id, chat_jid, sender_jid, role, content, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		d.uuidValue(newUUID()), msg.ChatJID, msg.SenderJID, msg.Role, msg.Content, msg.CreatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("failed to save ai message: %w", err)
	}
	return nil
}

// ListAIMessages returns the most recent limit messages of a sender in a chat
// created at or after since, ordered oldest to newest.
func (d *DB) ListAIMessages(ctx context.Context, chat, sender string, since time.Time, limit int) ([]model.AIMessage, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT chat_jid, sender_jid, role, content, created_at FROM ai_messages
		 WHERE chat_jid = ? AND sender_jid = ? AND created_at >= ?
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		chat, sender, since.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list ai messages: %w", err)
	}
	defer rows.Close()

	var msgs []model.AIMessage
	for rows.Next() {
		var m model.AIMessage
		var createdMs int64
		if err := rows.Scan(&m.ChatJID, &m.SenderJID, &m.Role, &m.Content, &createdMs); err != nil {
			return nil, fmt.Errorf("failed to scan ai message: %w", err)
		}
		m.CreatedAt = time.UnixMilli(createdMs).UTC()
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate ai messages: %w", err)
	}
	slices.Reverse(msgs)
	return msgs, nil
}

// PurgeAIMessages deletes the messages created before the given instant.
func (d *DB) PurgeAIMessages(ctx context.Context, before time.Time) error {
	if _, err := d.db.ExecContext(ctx, "DELETE FROM ai_messages WHERE created_at < ?", before.UnixMilli()); err != nil {
		return fmt.Errorf("failed to purge ai messages: %w", err)
	}
	return nil
}
