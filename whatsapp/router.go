package whatsapp

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/o11"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Sender is the interface consumed by command handlers to interact with WhatsApp.
// It is defined here (consumer side) and implemented by the whatsmeow adapter (Client).
type Sender interface {
	// ReplyText sends a text message quoting the original message
	ReplyText(ctx context.Context, msg IncomingMessage, text string) error
	// SendText sends a plain text message to a JID
	SendText(ctx context.Context, to types.JID, text string) error
	// ReplyMedia sends media (image, audio, document...) quoting the original message
	ReplyMedia(ctx context.Context, msg IncomingMessage, data []byte, mimeType, filename string) error
	// SendMedia sends media to a JID without quoting
	SendMedia(ctx context.Context, to types.JID, data []byte, mimeType, filename string) error
	// DeleteMessage deletes (revokes) a message
	DeleteMessage(ctx context.Context, msg IncomingMessage) error
}

// CommandHandler handles a matched incoming message
type CommandHandler func(ctx context.Context, s Sender, msg IncomingMessage) error

// Command binds a regex pattern to a handler. First matching command wins.
type Command struct {
	Name    string // Used in logs and metrics
	Pattern *regexp.Regexp
	Handler CommandHandler
}

// Router dispatches incoming WhatsApp messages to registered commands
type Router struct {
	log      *slog.Logger
	metrics  *o11.CommandMetrics
	tracer   trace.Tracer
	sender   Sender
	commands []Command
	fallback CommandHandler
	botJIDs  []string // JID forms that count as a bot mention (@lid, @c.us)
	timeout  time.Duration
	wg       sync.WaitGroup
}

// NewRouter creates a command router
func NewRouter(log *slog.Logger, metrics *o11.CommandMetrics, tp trace.TracerProvider, botJIDs []string, timeout time.Duration) *Router {
	return &Router{
		log:     log,
		metrics: metrics,
		tracer:  tp.Tracer("whatsapp/router"),
		botJIDs: botJIDs,
		timeout: timeout,
	}
}

// SetSender injects the WhatsApp sender used by command handlers.
// It is set at wiring time (after creating the client) to break the
// client <-> router construction cycle.
func (r *Router) SetSender(s Sender) {
	r.sender = s
}

// Register adds a command to the router. Commands are evaluated in registration order.
func (r *Router) Register(name string, pattern *regexp.Regexp, handler CommandHandler) {
	r.commands = append(r.commands, Command{Name: name, Pattern: pattern, Handler: handler})
	r.log.Debug("Registered command", "name", name, "pattern", pattern.String())
}

// SetFallback sets the handler invoked when no command matches and the bot is mentioned
func (r *Router) SetFallback(handler CommandHandler) {
	r.fallback = handler
}

// Wait blocks until all in-flight command handlers finish (used during shutdown)
func (r *Router) Wait() {
	r.wg.Wait()
}

// HandleEvent is the whatsmeow event handler entry point
func (r *Router) HandleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		r.dispatch(v)
	case *events.Connected:
		r.log.Info("WhatsApp client connected")
	case *events.LoggedOut:
		r.log.Error("WhatsApp client logged out, exiting to trigger restart")
		// Exit non-zero so the container/supervisor restarts the process
		// (a new login flow will be required)
		r.wg.Wait()
		r.onLoggedOut()
	}
}

// onLoggedOut is called on a LoggedOut event. It is a variable to allow testing.
var onLoggedOutFn = func() {}

func (r *Router) onLoggedOut() { onLoggedOutFn() }

// dispatch routes a message to the first matching command, or the fallback
func (r *Router) dispatch(evt *events.Message) {
	msg := NewIncomingMessage(evt)

	if msg.Body == "" {
		return
	}

	// Ignore messages sent by the bot itself
	if evt.Info.IsFromMe {
		return
	}

	for _, cmd := range r.commands {
		if cmd.Pattern.MatchString(msg.Body) {
			r.execute(cmd.Name, cmd.Handler, msg)
			return
		}
	}

	if r.fallback != nil && r.mentionsBot(msg) {
		r.execute("ai_fallback", r.fallback, msg)
	}
}

// mentionsBot reports whether the message mentions the bot in any of its known JID forms
func (r *Router) mentionsBot(msg IncomingMessage) bool {
	for _, mentioned := range msg.MentionedJIDs {
		for _, botJID := range r.botJIDs {
			if botJID != "" && mentioned == botJID {
				return true
			}
		}
	}
	return false
}

// execute runs a handler in its own goroutine with timeout, panic recovery,
// metrics and tracing
func (r *Router) execute(name string, handler CommandHandler, msg IncomingMessage) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				r.log.Error("Command handler panicked",
					"command", name, "panic", fmt.Sprint(rec), "chat", msg.Chat.String())
				r.recordMetrics(context.Background(), name, "panic", 0)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
		defer cancel()

		ctx, span := r.tracer.Start(ctx, "whatsapp.command."+name,
			trace.WithAttributes(
				attribute.String("command", name),
				attribute.String("chat", msg.Chat.String()),
				attribute.String("sender", msg.Sender.String()),
			))
		defer span.End()

		log := r.log.With("command", name, "push_name", msg.PushName, "chat", msg.Chat.String())
		log.Info("Executing command", "body", msg.Body)

		start := time.Now()
		err := handler(ctx, r.sender, msg)
		duration := time.Since(start).Seconds()

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			log.Error("Command failed", "error", err)
			r.recordMetrics(ctx, name, "error", duration)
			return
		}

		r.recordMetrics(ctx, name, "ok", duration)
	}()
}

func (r *Router) recordMetrics(ctx context.Context, name, result string, duration float64) {
	attrs := metric.WithAttributes(
		attribute.String("command", name),
		attribute.String("result", result),
	)
	r.metrics.CommandsTotal.Add(ctx, 1, attrs)
	if duration > 0 {
		r.metrics.CommandDuration.Record(ctx, duration,
			metric.WithAttributes(attribute.String("command", name)))
	}
}
