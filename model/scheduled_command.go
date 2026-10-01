package model

import "time"

// ScheduledCommand represents a recurring command that the bot will execute
// automatically in a WhatsApp group at a given cron schedule.
type ScheduledCommand struct {
	ID        int64
	Name      string
	Schedule  string
	Command   string
	GroupJID  string
	Enabled   bool
	CreatedAt time.Time
}
