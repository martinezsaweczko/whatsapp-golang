package tts

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestSynthesize(t *testing.T) {
	audio := []byte("fake-mp3-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "hola mundo" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("tl") != "es" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write(audio)
	}))
	defer server.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := New(server.Client(), log, tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Point the service at the test server by rewriting the endpoint via transport
	svc.client = &http.Client{
		Transport: &rewriteTransport{target: server.URL, base: http.DefaultTransport},
	}

	got, err := svc.Synthesize(context.Background(), "hola mundo")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if string(got) != string(audio) {
		t.Errorf("unexpected audio: %q", got)
	}
}

func TestSynthesizeEmpty(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, _ := New(http.DefaultClient, log, tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if _, err := svc.Synthesize(context.Background(), "   "); err == nil {
		t.Fatal("expected error for empty text")
	}
}

// rewriteTransport rewrites all requests to the test server (endpoint is a const)
type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	targetURL, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	return t.base.RoundTrip(req)
}
