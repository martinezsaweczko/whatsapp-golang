package cleanup

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// fakePurger records every call and signals it on calls.
type fakePurger struct {
	mu     sync.Mutex
	ages   []time.Duration
	calls  chan struct{}
	err    error
	panics bool
}

func newFakePurger() *fakePurger {
	return &fakePurger{calls: make(chan struct{}, 10)}
}

func (f *fakePurger) PurgeOld(ctx context.Context, maxAge time.Duration) (int, error) {
	f.mu.Lock()
	f.ages = append(f.ages, maxAge)
	f.mu.Unlock()
	f.calls <- struct{}{}
	if f.panics {
		panic("boom")
	}
	if _, ok := ctx.Deadline(); !ok {
		return 0, errors.New("run context has no deadline")
	}
	return 3, f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func validConfig() Config {
	return Config{MaxAge: 720 * time.Hour, Schedule: "0 4 * * *", Timezone: "Europe/Madrid"}
}

func waitCall(t *testing.T, f *fakePurger) {
	t.Helper()
	select {
	case <-f.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("purger was not called")
	}
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"bad schedule", func(c *Config) { c.Schedule = "every day" }, true},
		{"empty schedule", func(c *Config) { c.Schedule = "" }, true},
		{"bad timezone", func(c *Config) { c.Timezone = "Mars/Olympus" }, true},
		{"non-positive max age", func(c *Config) { c.MaxAge = 0 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			_, err := New(newFakePurger(), testLogger(), tracenoop.NewTracerProvider(), cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewRejectsNilPurger(t *testing.T) {
	if _, err := New(nil, testLogger(), tracenoop.NewTracerProvider(), validConfig()); err == nil {
		t.Fatal("expected an error for a nil purger")
	}
}

func TestStartRunsOnceWithConfiguredMaxAge(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"successful purge", nil},
		{"failed purge is only logged", errors.New("disk on fire")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newFakePurger()
			p.err = tt.err
			svc, err := New(p, testLogger(), tracenoop.NewTracerProvider(), validConfig())
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Start(context.Background()); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			waitCall(t, p)
			svc.Stop()

			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.ages) != 1 {
				t.Fatalf("purger called %d times, want 1", len(p.ages))
			}
			if p.ages[0] != 720*time.Hour {
				t.Errorf("max age = %v, want 720h", p.ages[0])
			}
		})
	}
}

func TestStartRecoversFromPanic(t *testing.T) {
	p := newFakePurger()
	p.panics = true
	svc, err := New(p, testLogger(), tracenoop.NewTracerProvider(), validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCall(t, p)
	svc.Stop() // waits for the startup run; the panic must not crash the test binary
}

func TestStartTwiceFails(t *testing.T) {
	p := newFakePurger()
	svc, err := New(p, testLogger(), tracenoop.NewTracerProvider(), validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()
	if err := svc.Start(context.Background()); err == nil {
		t.Fatal("expected an error on the second Start")
	}
}

func TestStopIsSafe(t *testing.T) {
	svc, err := New(newFakePurger(), testLogger(), tracenoop.NewTracerProvider(), validConfig())
	if err != nil {
		t.Fatal(err)
	}
	svc.Stop() // before Start
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.Stop()
	svc.Stop() // twice
}
