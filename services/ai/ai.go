// Package ai implements the OpenAI fallback: messages that mention the bot
// and match no command are answered by an LLM as text.
package ai

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/o11"
	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// trollPrompt is the system prompt for troll-listed users
const trollPrompt = "Eres un bufon, responde lo que te preguntan pero intenta ser gracioso y troll, no des nunca una respuesta seria. Por favor no indiques que la respuesta forma parte de una broma, haz que no se note. Siempre intenta reirte de quien te pregunta."

// Service provides AI completions
type Service struct {
	client     *openai.Client
	model      string
	trollJIDs  map[string]bool
	store      ChatStore
	contextCfg ContextConfig
	loc        *time.Location
	now        func() time.Time

	purgeMu       sync.Mutex
	lastPurge     time.Time
	log           *slog.Logger
	tracer        trace.Tracer
	repliesTotal  metric.Int64Counter
	recordedTotal metric.Int64Counter
}

// New creates the AI service. httpClient is the instrumented external client;
// store keeps the group conversations used as context.
func New(apiKey, model, baseURL string, httpClient *http.Client, trollJIDs []string, store ChatStore, contextCfg ContextConfig, log *slog.Logger, tp trace.TracerProvider, mp metric.MeterProvider) (*Service, error) {
	repliesTotal, err := o11.NewBusinessCounter(mp, "ai_replies_total", "AI replies per mode (text)")
	if err != nil {
		return nil, fmt.Errorf("failed to create ai counter: %w", err)
	}
	recordedTotal, err := o11.NewBusinessCounter(mp, "ai_chat_messages_recorded_total", "Group messages recorded as AI context, per result")
	if err != nil {
		return nil, fmt.Errorf("failed to create ai recorder counter: %w", err)
	}

	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	cfg.HTTPClient = httpClient

	trolls := make(map[string]bool, len(trollJIDs))
	for _, j := range trollJIDs {
		trolls[j] = true
	}

	loc, err := time.LoadLocation(historyTimezone)
	if err != nil {
		log.Warn("Cannot load history timezone, using UTC", "timezone", historyTimezone, "error", err)
		loc = time.UTC
	}

	return &Service{
		client:        openai.NewClientWithConfig(cfg),
		model:         model,
		trollJIDs:     trolls,
		store:         store,
		contextCfg:    contextCfg,
		loc:           loc,
		now:           time.Now,
		log:           log,
		tracer:        tp.Tracer("services/ai"),
		repliesTotal:  repliesTotal,
		recordedTotal: recordedTotal,
	}, nil
}

// Complete returns the chat completion for a prompt (shared with the weather service)
func (s *Service) Complete(ctx context.Context, prompt string) (string, error) {
	ctx, span := s.tracer.Start(ctx, "ai.Complete")
	defer span.End()

	resp, err := s.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: s.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		err = fmt.Errorf("OpenAI completion failed: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	if len(resp.Choices) == 0 {
		err := fmt.Errorf("OpenAI returned no choices")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}

// Fallback returns the handler for unmatched messages that mention the bot
func (s *Service) Fallback() whatsapp.CommandHandler {
	return func(ctx context.Context, sender whatsapp.Sender, msg whatsapp.IncomingMessage) error {
		s.log.Info("Processing AI fallback", "user", msg.PushName, "chat", msg.Chat.String())
		// Purge after the reply has been sent so it never delays the answer
		defer s.maybePurge(ctx)
		return s.replyText(ctx, sender, msg)
	}
}

// maybePurge deletes chat messages older than the context window, at most
// once per purgeInterval. Failures are logged and retried on the next fallback.
func (s *Service) maybePurge(ctx context.Context) {
	if s.store == nil {
		return
	}
	now := s.now()
	s.purgeMu.Lock()
	if !s.lastPurge.IsZero() && now.Sub(s.lastPurge) < purgeInterval {
		s.purgeMu.Unlock()
		return
	}
	s.lastPurge = now
	s.purgeMu.Unlock()

	if err := s.store.PurgeChatMessages(ctx, now.Add(-s.contextCfg.window())); err != nil {
		s.log.Warn("Cannot purge chat history", "error", err)
		s.purgeMu.Lock()
		s.lastPurge = time.Time{}
		s.purgeMu.Unlock()
	}
}

// replyText answers with a completion. In groups it includes the recent group
// conversation and stores the reply as a bot message after a successful
// completion; history failures never prevent the reply. Private chats are
// answered without history.
func (s *Service) replyText(ctx context.Context, sender whatsapp.Sender, msg whatsapp.IncomingMessage) error {
	var transcript string
	if msg.IsGroup {
		transcript = s.loadTranscript(ctx, msg)
	}

	system := systemPrompt
	if s.isTroll(msg) {
		system = trollPrompt
	}

	reply, err := s.completeMessages(ctx, buildMessages(system, transcript, msg.Body))
	if err != nil {
		return sender.ReplyText(ctx, msg, "Ahora mismo no puedo responder, inténtalo más tarde")
	}

	if msg.IsGroup {
		s.saveReply(ctx, msg, reply)
	}
	s.repliesTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", "text")))
	return sender.ReplyText(ctx, msg, reply)
}

// completeMessages sends a multi-message conversation and returns the reply.
func (s *Service) completeMessages(ctx context.Context, messages []openai.ChatCompletionMessage) (string, error) {
	ctx, span := s.tracer.Start(ctx, "ai.CompleteWithHistory")
	defer span.End()
	span.SetAttributes(attribute.Int("ai.messages", len(messages)))

	resp, err := s.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    s.model,
		Messages: messages,
	})
	if err != nil {
		err = fmt.Errorf("OpenAI completion failed: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	if len(resp.Choices) == 0 {
		err := fmt.Errorf("OpenAI returned no choices")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

// isTroll reports whether the message author (or the group) is troll-listed
func (s *Service) isTroll(msg whatsapp.IncomingMessage) bool {
	return s.trollJIDs[msg.Sender.String()] || s.trollJIDs[msg.Chat.String()]
}
