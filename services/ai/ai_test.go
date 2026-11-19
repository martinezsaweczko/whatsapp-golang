package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
	"go.mau.fi/whatsmeow/types"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type fakeSender struct {
	replies  []string
	mediaOut [][]byte
}

func (f *fakeSender) ReplyText(_ context.Context, _ whatsapp.IncomingMessage, text string) error {
	f.replies = append(f.replies, text)
	return nil
}
func (f *fakeSender) SendText(_ context.Context, _ types.JID, _ string) error { return nil }
func (f *fakeSender) ReplyMedia(_ context.Context, _ whatsapp.IncomingMessage, data []byte, _, _ string) error {
	f.mediaOut = append(f.mediaOut, data)
	return nil
}
func (f *fakeSender) SendMedia(_ context.Context, _ types.JID, _ []byte, _, _ string) error {
	return nil
}
func (f *fakeSender) DeleteMessage(_ context.Context, _ whatsapp.IncomingMessage) error { return nil }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// fakeOpenAI records the messages of every request and answers with the given
// replies in order (cycling). When status is not 200 it answers with an error.
type fakeOpenAI struct {
	*httptest.Server
	requests [][]chatMessage
	paths    []string
	status   int
	replies  []string
}

func newFakeOpenAI(t *testing.T, replies ...string) *fakeOpenAI {
	t.Helper()
	f := &fakeOpenAI{status: http.StatusOK, replies: replies}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []chatMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.requests = append(f.requests, req.Messages)
		f.paths = append(f.paths, r.URL.Path)

		if f.status != http.StatusOK {
			http.Error(w, `{"error":{"message":"boom"}}`, f.status)
			return
		}
		content := f.replies[(len(f.requests)-1)%len(f.replies)]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
		})
	}))
	t.Cleanup(f.Close)
	return f
}

type listCall struct {
	chat     string
	since    time.Time
	maxChars int
}

// fakeStore is an in-memory ChatStore that records its calls. List returns
// every stored row of the chat, ignoring since and the budget.
type fakeStore struct {
	mu        sync.Mutex
	stored    []model.ChatMessage
	listCalls []listCall
	purges    []time.Time
	listErr   error
	saveErr   error
	purgeErr  error
}

func (f *fakeStore) SaveChatMessage(_ context.Context, msg model.ChatMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.stored = append(f.stored, msg)
	return nil
}

func (f *fakeStore) ListChatContext(_ context.Context, chat string, since time.Time, maxChars int) ([]model.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, listCall{chat, since, maxChars})
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []model.ChatMessage
	for _, m := range f.stored {
		if m.ChatJID == chat {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) PurgeChatMessages(_ context.Context, before time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges = append(f.purges, before)
	return f.purgeErr
}

const botJID = "34936674253@s.whatsapp.net"

func newTestService(t *testing.T, server *fakeOpenAI, trolls []string, store ChatStore) *Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := New("test-key", "test-model", server.URL+"/v1", server.Client(), trolls, store,
		ContextConfig{Days: 180, MaxChars: 1000, BotJID: botJID}, log,
		tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return svc
}

var (
	alice      = types.NewJID("34645568517", types.DefaultUserServer)
	aliceLID   = types.NewJID("111222333444555", types.HiddenUserServer)
	bob        = types.NewJID("34600111222", types.DefaultUserServer)
	group      = types.NewJID("120363000000000000", types.GroupServer)
	privateJID = alice
)

func mention(body string) whatsapp.IncomingMessage {
	return whatsapp.IncomingMessage{ID: "Q1", Body: body, Sender: alice, Chat: group, IsGroup: true, PushName: "Alice"}
}

func TestFallbackTextReply(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta de prueba")
	svc := newTestService(t, server, nil, &fakeStore{})
	sender := &fakeSender{}

	if err := svc.Fallback()(context.Background(), sender, mention("@bot cuéntame algo")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(sender.replies) != 1 || sender.replies[0] != "respuesta de prueba" {
		t.Errorf("unexpected replies: %+v", sender.replies)
	}
	if len(server.requests) != 1 {
		t.Fatalf("expected a single completion call, got %d", len(server.requests))
	}
	if server.paths[0] != "/v1/chat/completions" {
		t.Errorf("base URL override not honored, path = %s", server.paths[0])
	}
	msgs := server.requests[0]
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[0].Content != systemPrompt ||
		msgs[1] != (chatMessage{"user", "@bot cuéntame algo"}) {
		t.Errorf("unexpected messages: %+v", msgs)
	}
}

func TestFallbackTrollSystemPrompt(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta troll")
	svc := newTestService(t, server, []string{alice.String()}, &fakeStore{})

	if err := svc.Fallback()(context.Background(), &fakeSender{}, mention("@bot pregunta seria")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	msgs := server.requests[0]
	if msgs[0].Role != "system" || msgs[0].Content != trollPrompt {
		t.Errorf("troll system prompt not applied: %+v", msgs[0])
	}
	if msgs[len(msgs)-1].Content != "@bot pregunta seria" {
		t.Errorf("question must stay untouched: %+v", msgs)
	}
}

func TestFallbackSavesNothingWhenCompletionFails(t *testing.T) {
	server := newFakeOpenAI(t, "x")
	server.status = http.StatusInternalServerError
	store := &fakeStore{}
	svc := newTestService(t, server, nil, store)
	sender := &fakeSender{}

	if err := svc.Fallback()(context.Background(), sender, mention("pregunta")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(store.stored) != 0 {
		t.Errorf("nothing must be stored after a failed completion, got %+v", store.stored)
	}
	if len(sender.replies) != 1 || !strings.Contains(sender.replies[0], "no puedo responder") {
		t.Errorf("expected the apology reply, got %+v", sender.replies)
	}
}

func TestFallbackHistoryFailuresDoNotBlockReply(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta")
	store := &fakeStore{listErr: errors.New("db down"), saveErr: errors.New("db down")}
	svc := newTestService(t, server, nil, store)
	sender := &fakeSender{}

	if err := svc.Fallback()(context.Background(), sender, mention("pregunta")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(sender.replies) != 1 || sender.replies[0] != "respuesta" {
		t.Errorf("reply must be delivered despite history failures: %+v", sender.replies)
	}
	if msgs := server.requests[0]; len(msgs) != 2 {
		t.Errorf("without history the request is system + question, got %+v", msgs)
	}
}

func TestCompleteStaysHistoryFree(t *testing.T) {
	server := newFakeOpenAI(t, "ok")
	store := &fakeStore{}
	svc := newTestService(t, server, nil, store)

	if got, err := svc.Complete(context.Background(), "prompt"); err != nil || got != "ok" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if len(store.listCalls) != 0 || len(store.stored) != 0 {
		t.Errorf("Complete must not touch history: %+v %+v", store.listCalls, store.stored)
	}
	if msgs := server.requests[0]; len(msgs) != 1 || msgs[0] != (chatMessage{"user", "prompt"}) {
		t.Errorf("unexpected messages: %+v", msgs)
	}
}

func TestFallbackPurgesOncePer24h(t *testing.T) {
	server := newFakeOpenAI(t, "ok")
	store := &fakeStore{}
	svc := newTestService(t, server, nil, store)
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	handler := svc.Fallback()

	run := func() {
		t.Helper()
		if err := handler(context.Background(), &fakeSender{}, mention("hola")); err != nil {
			t.Fatalf("handler failed: %v", err)
		}
	}

	run()
	now = now.Add(time.Hour)
	run()
	if len(store.purges) != 1 {
		t.Fatalf("purges = %d within 24h, want 1", len(store.purges))
	}
	if want := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Add(-180 * 24 * time.Hour); !store.purges[0].Equal(want) {
		t.Errorf("purge cutoff = %v, want %v (180 days back)", store.purges[0], want)
	}

	now = now.Add(24 * time.Hour)
	run()
	if len(store.purges) != 2 {
		t.Errorf("purges = %d after 24h, want 2", len(store.purges))
	}
}

func TestFallbackPurgeFailureIsHarmlessAndRetried(t *testing.T) {
	server := newFakeOpenAI(t, "ok")
	store := &fakeStore{purgeErr: errors.New("db down")}
	svc := newTestService(t, server, nil, store)
	sender := &fakeSender{}

	for i := 0; i < 2; i++ {
		if err := svc.Fallback()(context.Background(), sender, mention("hola")); err != nil {
			t.Fatalf("handler failed: %v", err)
		}
	}

	if len(sender.replies) != 2 {
		t.Errorf("replies = %d, want 2 despite purge failures", len(sender.replies))
	}
	if len(store.purges) != 2 {
		t.Errorf("failed purge must be retried on the next fallback, purges = %d", len(store.purges))
	}
}
