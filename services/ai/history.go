package ai

import (
	"context"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/sashabaranov/go-openai"
)

// systemPrompt frames non-troll conversations. The server behind the API may
// reject requests without a system message, so one is always sent.
const systemPrompt = "Eres un asistente útil en un chat de WhatsApp. Responde de forma concisa."

// historyTimezone defines the calendar day that bounds the conversation context.
const historyTimezone = "Europe/Madrid"

// purgeInterval is the minimum time between two history purges.
const purgeInterval = 24 * time.Hour

// historyRetention is how long stored messages are kept before being purged.
const historyRetention = 7 * 24 * time.Hour

// HistoryStore persists the AI conversation turns (consumer-defined interface)
type HistoryStore interface {
	SaveAIMessage(ctx context.Context, msg model.AIMessage) error
	ListAIMessages(ctx context.Context, chat, sender string, since time.Time, limit int) ([]model.AIMessage, error)
	PurgeAIMessages(ctx context.Context, before time.Time) error
}

// startOfDay returns 00:00 of now's calendar day in loc.
func startOfDay(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// buildMessages assembles system prompt, prior turns and the new user message.
func buildMessages(system string, history []model.AIMessage, question string) []openai.ChatCompletionMessage {
	msgs := make([]openai.ChatCompletionMessage, 0, len(history)+2)
	msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: system})
	for _, h := range history {
		role := openai.ChatMessageRoleUser
		if h.Role == model.AIRoleAssistant {
			role = openai.ChatMessageRoleAssistant
		}
		msgs = append(msgs, openai.ChatCompletionMessage{Role: role, Content: h.Content})
	}
	return append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: question})
}
