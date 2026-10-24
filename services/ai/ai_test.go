package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
	"github.com/sashabaranov/go-openai"
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

// fakeOpenAI serves classification + completion responses in order
func fakeOpenAI(t *testing.T, prompts *[]string, responses []string) *httptest.Server {
	t.Helper()
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages) > 0 {
			*prompts = append(*prompts, req.Messages[0].Content)
		}

		content := responses[i%len(responses)]
		i++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
		})
	}))
}

func newTestService(t *testing.T, server *httptest.Server, trolls []string) *Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := New("test-key", "test-model", "", server.Client(), trolls, log,
		tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	// Point the OpenAI client at the test server
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = server.URL + "/v1"
	cfg.HTTPClient = server.Client()
	svc.client = openai.NewClientWithConfig(cfg)
	return svc
}

func TestFallbackTextReply(t *testing.T) {
	var prompts []string
	server := fakeOpenAI(t, &prompts, []string{"respuesta de prueba"})
	defer server.Close()

	svc := newTestService(t, server, nil)
	sender := &fakeSender{}

	msg := whatsapp.IncomingMessage{
		Body:   "@bot cuéntame algo",
		Sender: types.NewJID("34645568517", types.DefaultUserServer),
		Chat:   types.NewJID("34645568517", types.DefaultUserServer),
	}

	handler := svc.Fallback()
	if err := handler(context.Background(), sender, msg); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(sender.replies) != 1 || sender.replies[0] != "respuesta de prueba" {
		t.Errorf("unexpected replies: %+v", sender.replies)
	}
	if len(prompts) != 1 {
		t.Errorf("expected a single completion call, got %d", len(prompts))
	}
}

func TestFallbackTrollPrompt(t *testing.T) {
	var prompts []string
	server := fakeOpenAI(t, &prompts, []string{"respuesta troll"})
	defer server.Close()

	trollJID := types.NewJID("34645568517", types.DefaultUserServer).String()
	svc := newTestService(t, server, []string{trollJID})
	sender := &fakeSender{}

	msg := whatsapp.IncomingMessage{
		Body:   "@bot pregunta seria",
		Sender: types.NewJID("34645568517", types.DefaultUserServer),
		Chat:   types.NewJID("34645568517", types.DefaultUserServer),
	}

	handler := svc.Fallback()
	if err := handler(context.Background(), sender, msg); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(prompts) != 1 || !strings.Contains(prompts[0], "Eres un bufon") {
		t.Errorf("troll prompt not applied: %+v", prompts)
	}
}

func TestNewHonorsBaseURL(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer server.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := New("k", "m", server.URL+"/v1", server.Client(), nil, log,
		tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if got, err := svc.Complete(context.Background(), "hola"); err != nil || got != "ok" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	if len(paths) != 1 || paths[0] != "/v1/chat/completions" {
		t.Errorf("unexpected request paths: %v", paths)
	}
}
