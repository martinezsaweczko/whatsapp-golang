package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
	"github.com/sashabaranov/go-openai"
	"go.mau.fi/whatsmeow/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// systemPrompt frames non-troll conversations. The server behind the API may
// reject requests without a system message, so one is always sent.
const systemPrompt = "Eres un asistente útil en un chat de WhatsApp. Responde de forma concisa."

// transcriptPrompt is appended to the system prompt when the group
// conversation is sent, explaining the transcript format to the model.
const transcriptPrompt = "Antes de la pregunta recibirás la conversación reciente del grupo de WhatsApp, " +
	"en orden cronológico, con una línea por mensaje con el formato \"[AAAA-MM-DD HH:MM] Nombre: texto\" " +
	"(hora de Madrid). Las líneas marcadas con \"(QUIEN PREGUNTA)\" son de la persona que te pregunta ahora; " +
	"el resto son de otros miembros del grupo y las líneas \"Bot\" son tus respuestas anteriores. " +
	"Usa esa conversación como contexto y responde solo a la última pregunta."

// askerMarker tags the transcript lines written by the person asking now.
const askerMarker = " (QUIEN PREGUNTA)"

// botName is the push name stored for, and shown on, the bot's own replies.
const botName = "Bot"

// historyTimezone is the timezone of the transcript timestamps.
const historyTimezone = "Europe/Madrid"

// purgeInterval is the minimum time between two history purges.
const purgeInterval = 24 * time.Hour

// ChatStore persists group conversations (consumer-defined interface)
type ChatStore interface {
	SaveChatMessage(ctx context.Context, msg model.ChatMessage) error
	ListChatContext(ctx context.Context, chat string, since time.Time, maxChars int) ([]model.ChatMessage, error)
	PurgeChatMessages(ctx context.Context, before time.Time) error
}

// ContextConfig controls the group conversation sent to the AI.
type ContextConfig struct {
	Days     int    // Retention and context window, in days
	MaxChars int    // Character budget of the transcript (newest messages win)
	BotJID   string // Sender JID stored for the bot's own replies
}

// window returns the context and retention window as a duration.
func (c ContextConfig) window() time.Duration {
	return time.Duration(c.Days) * 24 * time.Hour
}

// RecordMessage stores an incoming group message (whatsapp.MessageRecorder).
// Failures are logged and never returned to the router.
func (s *Service) RecordMessage(ctx context.Context, msg whatsapp.IncomingMessage) {
	if s.store == nil {
		return
	}
	ctx, span := s.tracer.Start(ctx, "ai.RecordMessage")
	defer span.End()

	at := msg.Timestamp
	if at.IsZero() {
		at = s.now()
	}
	var alt string
	if !msg.SenderAlt.IsEmpty() {
		alt = msg.SenderAlt.ToNonAD().String()
	}
	// Device suffixes are dropped so one person matches across devices
	err := s.store.SaveChatMessage(ctx, model.ChatMessage{
		ChatJID:      msg.Chat.String(),
		MessageID:    string(msg.ID),
		SenderJID:    msg.Sender.ToNonAD().String(),
		SenderAltJID: alt,
		PushName:     msg.PushName,
		Body:         msg.Body,
		CreatedAt:    at,
	})
	result := "ok"
	if err != nil {
		result = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.log.Warn("Cannot record chat message", "chat", msg.Chat.String(), "error", err)
	}
	s.recordedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

// loadTranscript returns the group conversation of the context window as one
// transcript block, without the message being answered. Failures are logged
// and yield an empty transcript.
func (s *Service) loadTranscript(ctx context.Context, msg whatsapp.IncomingMessage) string {
	if s.store == nil {
		return ""
	}
	ctx, span := s.tracer.Start(ctx, "ai.LoadChatContext")
	defer span.End()

	chat := msg.Chat.String()
	rows, err := s.store.ListChatContext(ctx, chat, s.now().Add(-s.contextCfg.window()), s.contextCfg.MaxChars)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.log.Warn("Cannot load chat context, answering without it", "chat", chat, "error", err)
		return ""
	}

	asker := map[string]bool{}
	for _, jid := range []types.JID{msg.Sender, msg.SenderAlt} {
		if !jid.IsEmpty() {
			asker[jid.ToNonAD().String()] = true
		}
	}

	var b strings.Builder
	lines := 0
	for _, r := range rows {
		if msg.ID != "" && r.MessageID == string(msg.ID) {
			continue
		}
		if lines > 0 {
			b.WriteByte('\n')
		}
		lines++
		fmt.Fprintf(&b, "[%s] %s", r.CreatedAt.In(s.loc).Format("2006-01-02 15:04"), displayName(r))
		if !r.IsBot && (asker[r.SenderJID] || (r.SenderAltJID != "" && asker[r.SenderAltJID])) {
			b.WriteString(askerMarker)
		}
		b.WriteString(": ")
		b.WriteString(r.Body)
	}
	span.SetAttributes(attribute.Int("ai.context_messages", lines))
	return b.String()
}

// displayName is the transcript name of a row: "Bot" for the bot's replies,
// else the push name, falling back to the user part of the sender JID.
func displayName(r model.ChatMessage) string {
	if r.IsBot {
		return botName
	}
	if r.PushName != "" {
		return r.PushName
	}
	if jid, err := types.ParseJID(r.SenderJID); err == nil && jid.User != "" {
		return jid.User
	}
	user, _, _ := strings.Cut(r.SenderJID, "@")
	return user
}

// saveReply stores the AI reply as a bot message of the group. Failures are
// only logged.
func (s *Service) saveReply(ctx context.Context, msg whatsapp.IncomingMessage, reply string) {
	if s.store == nil {
		return
	}
	err := s.store.SaveChatMessage(ctx, model.ChatMessage{
		ChatJID:   msg.Chat.String(),
		SenderJID: s.contextCfg.BotJID,
		PushName:  botName,
		Body:      reply,
		IsBot:     true,
		CreatedAt: s.now(),
	})
	if err != nil {
		s.log.Warn("Cannot save AI reply to chat history", "chat", msg.Chat.String(), "error", err)
	}
}

// buildMessages assembles the system prompt, the optional group transcript
// (explained in the system prompt) and the new question as the last message.
func buildMessages(system, transcript, question string) []openai.ChatCompletionMessage {
	if transcript == "" {
		return []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: system},
			{Role: openai.ChatMessageRoleUser, Content: question},
		}
	}
	return []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: system + "\n\n" + transcriptPrompt},
		{Role: openai.ChatMessageRoleUser, Content: transcript},
		{Role: openai.ChatMessageRoleUser, Content: question},
	}
}
