// Package cleanup runs the built-in kiosk file cleanup: once at startup and
// then on a cron schedule, on its own runner independent of the scheduled
// command service.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// runTimeout bounds a single cleanup run.
const runTimeout = 5 * time.Minute

// Purger deletes files older than maxAge and reports how many were deleted
// (consumer-defined interface, implemented by kiosk.Service).
type Purger interface {
	PurgeOld(ctx context.Context, maxAge time.Duration) (int, error)
}

// Config holds the cleanup configuration.
type Config struct {
	MaxAge   time.Duration // files older than this are deleted
	Schedule string        // standard 5-field cron expression
	Timezone string        // IANA timezone the schedule is evaluated in
}

// Service runs the purger at startup and on the configured schedule.
type Service struct {
	purger   Purger
	log      *slog.Logger
	tracer   trace.Tracer
	cfg      Config
	schedule cron.Schedule
	location *time.Location

	mu      sync.Mutex
	cron    *cron.Cron
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup // tracks the startup run
}

// New validates the configuration and creates the cleanup service.
func New(purger Purger, log *slog.Logger, tp trace.TracerProvider, cfg Config) (*Service, error) {
	if purger == nil {
		return nil, errors.New("cleanup purger cannot be nil")
	}
	if cfg.MaxAge <= 0 {
		return nil, fmt.Errorf("cleanup max age must be positive, got %s", cfg.MaxAge)
	}
	schedule, err := cron.ParseStandard(cfg.Schedule)
	if err != nil {
		return nil, fmt.Errorf("invalid cleanup schedule %q: %w", cfg.Schedule, err)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid cleanup timezone %q: %w", cfg.Timezone, err)
	}
	return &Service{
		purger:   purger,
		log:      log,
		tracer:   tp.Tracer("services/cleanup"),
		cfg:      cfg,
		schedule: schedule,
		location: loc,
	}, nil
}

// Start launches one cleanup run in the background, so a slow filesystem
// never delays startup, and registers the recurring schedule. Each run
// recovers from panics. Start can be called once.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("cleanup service already started")
	}
	s.started = true

	// Detach from the caller's cancellation; Stop cancels in-flight runs.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel

	s.cron = cron.New(cron.WithLocation(s.location))
	s.cron.Schedule(s.schedule, cron.FuncJob(func() { s.run(runCtx, "schedule") }))

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(runCtx, "startup")
	}()

	s.cron.Start()
	s.log.Info("Kiosk cleanup started",
		"schedule", s.cfg.Schedule, "timezone", s.cfg.Timezone, "max_age", s.cfg.MaxAge.String())
	return nil
}

// Stop cancels in-flight runs, stops the schedule and waits for any running
// cleanup to return. It is safe to call before Start and more than once.
func (s *Service) Stop() {
	s.mu.Lock()
	if !s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	c, cancel := s.cron, s.cancel
	s.mu.Unlock()

	cancel()
	<-c.Stop().Done()
	s.wg.Wait()
	s.log.Info("Kiosk cleanup stopped")
}

// run executes one bounded cleanup with its own span. Errors and panics are
// logged and never propagate: the next run retries.
func (s *Service) run(ctx context.Context, trigger string) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	ctx, span := s.tracer.Start(ctx, "cleanup.Run", trace.WithAttributes(
		attribute.String("trigger", trigger),
		attribute.String("max_age", s.cfg.MaxAge.String()),
	))
	defer span.End()

	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("kiosk cleanup panicked: %v", r)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			s.log.ErrorContext(ctx, "Kiosk cleanup panicked", "trigger", trigger, "panic", r)
		}
	}()

	deleted, err := s.purger.PurgeOld(ctx, s.cfg.MaxAge)
	span.SetAttributes(attribute.Int("deleted", deleted))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.log.WarnContext(ctx, "Kiosk cleanup finished with errors", "trigger", trigger, "deleted", deleted, "error", err)
		return
	}
	s.log.InfoContext(ctx, "Kiosk cleanup run completed", "trigger", trigger, "deleted", deleted)
}
