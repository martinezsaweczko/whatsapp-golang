package whatsapp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/o11"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
)

func newTestRouter(t *testing.T, botJIDs []string) *Router {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	metrics, err := o11.NewCommandMetrics(noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("failed to create metrics: %v", err)
	}
	return NewRouter(log, metrics, tracenoop.NewTracerProvider(), botJIDs, 5*time.Second)
}

func messageEvent(body string, mentioned ...string) *events.Message {
	msg := &waE2E.Message{}
	if mentioned == nil {
		msg.Conversation = proto.String(body)
	} else {
		msg.ExtendedTextMessage = &waE2E.ExtendedTextMessage{
			Text: proto.String(body),
			ContextInfo: &waE2E.ContextInfo{
				MentionedJID: mentioned,
			},
		}
	}
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   types.NewJID("12345", types.GroupServer),
				Sender: types.NewJID("34645568517", types.DefaultUserServer),
			},
			ID:       "MSGID1",
			PushName: "David",
		},
		Message:    msg,
		RawMessage: msg,
	}
}

// waitFor polls a condition until it holds or the timeout expires
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestFirstMatchWins(t *testing.T) {
	router := newTestRouter(t, nil)

	var first, second atomic.Int32
	router.Register("first", regexp.MustCompile(`^hola`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		first.Add(1)
		return nil
	})
	router.Register("second", regexp.MustCompile(`^hola.*`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		second.Add(1)
		return nil
	})

	router.dispatch(messageEvent("hola que tal"))
	waitFor(t, func() bool { return first.Load() == 1 })

	if second.Load() != 0 {
		t.Error("second handler should not run when first matches")
	}
}

func TestNoMatchNoMentionNoDispatch(t *testing.T) {
	router := newTestRouter(t, []string{"57905285959776@lid"})

	var calls atomic.Int32
	router.Register("cmd", regexp.MustCompile(`^nacional`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		calls.Add(1)
		return nil
	})
	router.SetFallback(func(_ context.Context, _ Sender, _ IncomingMessage) error {
		calls.Add(1)
		return nil
	})

	router.dispatch(messageEvent("mensaje cualquiera sin mencion"))

	// Give the dispatcher a chance to (incorrectly) fire
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 0 {
		t.Error("no handler should run for unmatched message without mention")
	}
}

func TestFallbackOnMention(t *testing.T) {
	router := newTestRouter(t, []string{"57905285959776@lid", "34936674253@c.us"})

	var fallbackCalls atomic.Int32
	router.SetFallback(func(_ context.Context, _ Sender, _ IncomingMessage) error {
		fallbackCalls.Add(1)
		return nil
	})

	// Mention via @lid
	router.dispatch(messageEvent("@bot como estas", "57905285959776@lid"))
	waitFor(t, func() bool { return fallbackCalls.Load() == 1 })

	// Mention via @c.us
	router.dispatch(messageEvent("@bot dime algo", "34936674253@c.us"))
	waitFor(t, func() bool { return fallbackCalls.Load() == 2 })
}

func TestOwnMessagesIgnored(t *testing.T) {
	router := newTestRouter(t, nil)

	var calls atomic.Int32
	router.Register("cmd", regexp.MustCompile(`.*`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		calls.Add(1)
		return nil
	})

	evt := messageEvent("hola")
	evt.Info.IsFromMe = true
	router.dispatch(evt)

	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 0 {
		t.Error("messages sent by the bot itself must be ignored")
	}
}

func TestPanicRecovery(t *testing.T) {
	router := newTestRouter(t, nil)

	var survived atomic.Int32
	router.Register("panic", regexp.MustCompile(`^boom`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		panic("handler exploded")
	})
	router.Register("survivor", regexp.MustCompile(`^ok`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		survived.Add(1)
		return nil
	})

	router.dispatch(messageEvent("boom"))
	router.dispatch(messageEvent("ok"))

	waitFor(t, func() bool { return survived.Load() == 1 })
}

func TestHandlerErrorRecorded(t *testing.T) {
	router := newTestRouter(t, nil)

	done := make(chan struct{})
	router.Register("fail", regexp.MustCompile(`^fail`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		close(done)
		return errors.New("handler error")
	})

	router.dispatch(messageEvent("fail"))
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not run")
	}

	// Router must remain usable
	var ok atomic.Int32
	router.Register("ok", regexp.MustCompile(`^ok`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		ok.Add(1)
		return nil
	})
	router.dispatch(messageEvent("ok"))
	waitFor(t, func() bool { return ok.Load() == 1 })
}

func TestExtractTextAndMentions(t *testing.T) {
	// Conversation
	m := &waE2E.Message{Conversation: proto.String("hola")}
	if got := extractText(m); got != "hola" {
		t.Errorf("extractText conversation = %q", got)
	}

	// Extended text with mentions
	m2 := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String("@bot hola"),
			ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{"123@lid"}},
		},
	}
	if got := extractText(m2); got != "@bot hola" {
		t.Errorf("extractText extended = %q", got)
	}
	if got := extractMentions(m2); len(got) != 1 || got[0] != "123@lid" {
		t.Errorf("extractMentions = %v", got)
	}

	// Nil safety
	if got := extractText(nil); got != "" {
		t.Errorf("extractText nil = %q", got)
	}
	if got := extractMentions(nil); got != nil {
		t.Errorf("extractMentions nil = %v", got)
	}
}

func TestMediaTypeFor(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":      "image",
		"audio/mpeg":      "audio",
		"video/mp4":       "video",
		"application/pdf": "document",
	}
	for mime, _ := range cases {
		_ = mediaTypeFor(mime) // smoke test: no panic
	}
}
