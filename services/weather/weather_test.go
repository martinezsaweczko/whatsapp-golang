package weather

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type fakeCompleter struct {
	lastPrompt string
	reply      string
	err        error
}

func (f *fakeCompleter) Complete(_ context.Context, prompt string) (string, error) {
	f.lastPrompt = prompt
	return f.reply, f.err
}

func TestReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("appid") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("q") != "Madrid" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"name":"Madrid","main":{"temp":25.5},"sys":{"country":"ES"}}`))
	}))
	defer server.Close()

	ai := &fakeCompleter{reply: "Día soleado en Madrid ☀️"}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := New(server.Client(), server.URL, "test-key", 200, ai, log, tracenoop.NewTracerProvider())

	note, err := svc.Report(context.Background(), "Madrid")
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}
	if note != "Día soleado en Madrid ☀️" {
		t.Errorf("unexpected note: %s", note)
	}
	// The AI prompt must contain the raw weather JSON
	if !strings.Contains(ai.lastPrompt, "Madrid") || !strings.Contains(ai.lastPrompt, "25.5") {
		t.Errorf("prompt missing weather data: %s", ai.lastPrompt)
	}
	if !strings.Contains(ai.lastPrompt, "200 palabras") {
		t.Errorf("prompt missing word count: %s", ai.lastPrompt)
	}
}

func TestReportAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ai := &fakeCompleter{}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := New(server.Client(), server.URL, "key", 200, ai, log, tracenoop.NewTracerProvider())

	if _, err := svc.Report(context.Background(), "Nowhere"); err == nil {
		t.Fatal("expected error for API failure")
	}
}
