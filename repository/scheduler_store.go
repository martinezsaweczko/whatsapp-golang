package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/martinezsaweczko/whatsappBot-golang/model"
)

// CreateScheduledCommand stores a new scheduled command and assigns it a UUID v7 ID.
func (d *DB) CreateScheduledCommand(ctx context.Context, cmd model.ScheduledCommand) (uuid.UUID, error) {
	if cmd.ID == uuid.Nil {
		cmd.ID = newUUID()
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO scheduled_commands (id, name, schedule, command, group_jid, enabled)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		cmd.ID.String(), cmd.Name, cmd.Schedule, cmd.Command, cmd.GroupJID, cmd.Enabled)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create scheduled command: %w", err)
	}
	return cmd.ID, nil
}

// ListScheduledCommands returns all scheduled commands.
func (d *DB) ListScheduledCommands(ctx context.Context) ([]model.ScheduledCommand, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, name, schedule, command, group_jid, enabled, created_date, updated_date
		 FROM scheduled_commands
		 ORDER BY created_date DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to list scheduled commands: %w", err)
	}
	defer rows.Close()

	var cmds []model.ScheduledCommand
	for rows.Next() {
		var c model.ScheduledCommand
		var idStr string
		if err := rows.Scan(&idStr, &c.Name, &c.Schedule, &c.Command, &c.GroupJID, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan scheduled command: %w", err)
		}
		c.ID, err = uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse scheduled command id: %w", err)
		}
		cmds = append(cmds, c)
	}
	return cmds, rows.Err()
}

// DeleteScheduledCommand removes a scheduled command by ID.
func (d *DB) DeleteScheduledCommand(ctx context.Context, id uuid.UUID) error {
	res, err := d.db.ExecContext(ctx, "DELETE FROM scheduled_commands WHERE id = ?", id.String())
	if err != nil {
		return fmt.Errorf("failed to delete scheduled command: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
