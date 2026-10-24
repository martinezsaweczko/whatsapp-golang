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
	chat, sender string
	since        time.Time
	limit        int
}

// fakeHistory is an in-memory HistoryStore that records its calls.
type fakeHistory struct {
	stored    []model.AIMessage
	listCalls []listCall
	purges    []time.Time
	listErr   error
	saveErr   error
	purgeErr  error
}

func (f *fakeHistory) SaveAIMessage(_ context.Context, msg model.AIMessage) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.stored = append(f.stored, msg)
	return nil
}

func (f *fakeHistory) ListAIMessages(_ context.Context, chat, sender string, since time.Time, limit int) ([]model.AIMessage, error) {
	f.listCalls = append(f.listCalls, listCall{chat, sender, since, limit})
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []model.AIMessage
	for _, m := range f.stored {
		if m.ChatJID == chat && m.SenderJID == sender {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeHistory) PurgeAIMessages(_ context.Context, before time.Time) error {
	f.purges = append(f.purges, before)
	return f.purgeErr
}

func newTestService(t *testing.T, server *fakeOpenAI, trolls []string, store HistoryStore) *Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := New("test-key", "test-model", server.URL+"/v1", server.Client(), trolls, store, 3, log,
		tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return svc
}

var (
	alice = types.NewJID("34645568517", types.DefaultUserServer)
	group = types.NewJID("120363000000000000", types.GroupServer)
)

func mention(body string) whatsapp.IncomingMessage {
	return whatsapp.IncomingMessage{Body: body, Sender: alice, Chat: group}
}

func TestFallbackTextReply(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta de prueba")
	svc := newTestService(t, server, nil, &fakeHistory{})
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
	svc := newTestService(t, server, []string{alice.String()}, &fakeHistory{})

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

func TestFallbackSendsHistoryInOrder(t *testing.T) {
	server := newFakeOpenAI(t, "nueva")
	store := &fakeHistory{stored: []model.AIMessage{
		{ChatJID: group.String(), SenderJID: alice.String(), Role: model.AIRoleUser, Content: "q1"},
		{ChatJID: group.String(), SenderJID: alice.String(), Role: model.AIRoleAssistant, Content: "a1"},
		{ChatJID: "otro@g.us", SenderJID: alice.String(), Role: model.AIRoleUser, Content: "otro chat"},
	}}
	svc := newTestService(t, server, nil, store)

	if err := svc.Fallback()(context.Background(), &fakeSender{}, mention("q2")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	want := []chatMessage{{"system", systemPrompt}, {"user", "q1"}, {"assistant", "a1"}, {"user", "q2"}}
	got := server.requests[0]
	if len(got) != len(want) {
		t.Fatalf("messages = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("messages = %+v, want %+v", got, want)
		}
	}
}

func TestFallbackRequestsOnlySameDayHistory(t *testing.T) {
	server := newFakeOpenAI(t, "ok")
	store := &fakeHistory{}
	svc := newTestService(t, server, nil, store)
	// 2026-07-01 23:30 UTC is already 01:30 on July 2nd in Madrid (UTC+2)
	svc.now = func() time.Time { return time.Date(2026, 7, 1, 23, 30, 0, 0, time.UTC) }

	if err := svc.Fallback()(context.Background(), &fakeSender{}, mention("hola")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(store.listCalls) != 1 {
		t.Fatalf("expected one history load, got %d", len(store.listCalls))
	}
	call := store.listCalls[0]
	wantSince := time.Date(2026, 7, 1, 22, 0, 0, 0, time.UTC) // 00:00 Madrid
	if call.chat != group.String() || call.sender != alice.String() {
		t.Errorf("history requested for %s/%s, want %s/%s", call.chat, call.sender, group, alice)
	}
	if !call.since.Equal(wantSince) {
		t.Errorf("since = %v, want %v", call.since, wantSince)
	}
	if call.limit != 6 { // maxTurns 3 * 2 messages per turn
		t.Errorf("limit = %d, want 6", call.limit)
	}
}

func TestFallbackSavesExchangeAfterSuccess(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta")
	store := &fakeHistory{}
	svc := newTestService(t, server, nil, store)

	if err := svc.Fallback()(context.Background(), &fakeSender{}, mention("pregunta")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(store.stored) != 2 {
		t.Fatalf("stored %d messages, want 2", len(store.stored))
	}
	u, a := store.stored[0], store.stored[1]
	if u.Role != model.AIRoleUser || u.Content != "pregunta" || a.Role != model.AIRoleAssistant || a.Content != "respuesta" {
		t.Errorf("unexpected stored exchange: %+v", store.stored)
	}
	if u.ChatJID != group.String() || u.SenderJID != alice.String() {
		t.Errorf("exchange stored under %s/%s", u.ChatJID, u.SenderJID)
	}
}

func TestFallbackSavesNothingWhenCompletionFails(t *testing.T) {
	server := newFakeOpenAI(t, "x")
	server.status = http.StatusInternalServerError
	store := &fakeHistory{}
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
	store := &fakeHistory{listErr: errors.New("db down"), saveErr: errors.New("db down")}
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
	store := &fakeHistory{}
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
