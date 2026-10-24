package model

import (
	"time"

	"github.com/google/uuid"
)

// AI message roles stored in AIMessage.Role.
const (
	AIRoleUser      = "user"
	AIRoleAssistant = "assistant"
)

// AIMessage is one turn of the AI fallback conversation of a sender in a chat.
type AIMessage struct {
	ID        uuid.UUID
	ChatJID   string
	SenderJID string
	Role      string // AIRoleUser or AIRoleAssistant
	Content   string
	CreatedAt time.Time
}
