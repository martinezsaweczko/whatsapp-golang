package repository

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
)

// SaveChatMessage stores one group message. A zero CreatedAt means now and an
// empty MessageID gets a generated unique one (bot replies). A message already
// stored for the same chat and message ID is silently ignored, so duplicate
// event deliveries are harmless.
func (d *DB) SaveChatMessage(ctx context.Context, msg model.ChatMessage) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	id := newUUID()
	if msg.MessageID == "" {
		msg.MessageID = id.String()
	}
	insert := "INSERT OR IGNORE"
	if d.dialect == "mysql" {
		insert = "INSERT IGNORE"
	}
	_, err := d.db.ExecContext(ctx,
		insert+` INTO chat_messages (id, chat_jid, message_id, sender_jid, sender_alt_jid, push_name, body, is_bot, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.uuidValue(id), msg.ChatJID, msg.MessageID, msg.SenderJID, msg.SenderAltJID, msg.PushName, msg.Body, msg.IsBot, msg.CreatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("failed to save chat message: %w", err)
	}
	return nil
}

// ListChatContext returns the messages of a chat created at or after since,
// ordered oldest to newest. Rows are read newest first and reading stops at the
// first message whose body would make the total exceed maxChars characters, so
// the most recent conversation that fits the budget is returned (possibly
// nothing). maxChars <= 0 disables the budget.
func (d *DB) ListChatContext(ctx context.Context, chat string, since time.Time, maxChars int) ([]model.ChatMessage, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, chat_jid, message_id, sender_jid, sender_alt_jid, push_name, body, is_bot, created_at
		 FROM chat_messages WHERE chat_jid = ? AND created_at >= ?
		 ORDER BY created_at DESC, id DESC`,
		chat, since.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("failed to list chat messages: %w", err)
	}
	defer rows.Close()

	var msgs []model.ChatMessage
	total := 0
	for rows.Next() {
		var m model.ChatMessage
		var rawID interface{}
		var createdMs int64
		if err := rows.Scan(&rawID, &m.ChatJID, &m.MessageID, &m.SenderJID, &m.SenderAltJID, &m.PushName, &m.Body, &m.IsBot, &createdMs); err != nil {
			return nil, fmt.Errorf("failed to scan chat message: %w", err)
		}
		if maxChars > 0 {
			total += utf8.RuneCountInString(m.Body)
			if total > maxChars {
				break
			}
		}
		if m.ID, err = d.scanUUID(rawID); err != nil {
			return nil, fmt.Errorf("failed to parse chat message id: %w", err)
		}
		m.CreatedAt = time.UnixMilli(createdMs).UTC()
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate chat messages: %w", err)
	}
	slices.Reverse(msgs)
	return msgs, nil
}

// PurgeChatMessages deletes the messages created before the given instant.
func (d *DB) PurgeChatMessages(ctx context.Context, before time.Time) error {
	if _, err := d.db.ExecContext(ctx, "DELETE FROM chat_messages WHERE created_at < ?", before.UnixMilli()); err != nil {
		return fmt.Errorf("failed to purge chat messages: %w", err)
	}
	return nil
}
