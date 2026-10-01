// Package scheduler runs recurring WhatsApp commands on a cron schedule.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/robfig/cron/v3"
	"go.mau.fi/whatsmeow/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Store persists scheduled commands.
type Store interface {
	CreateScheduledCommand(ctx context.Context, cmd model.ScheduledCommand) (uuid.UUID, error)
	ListScheduledCommands(ctx context.Context) ([]model.ScheduledCommand, error)
	DeleteScheduledCommand(ctx context.Context, id uuid.UUID) error
}

// CommandExecutor runs a command by matching its text against registered handlers.
type CommandExecutor interface {
	ExecuteCommand(ctx context.Context, body string, chat, senderJID types.JID) error
	Match(body string) (string, error)
}

// ConnectionChecker reports whether the WhatsApp client is connected.
type ConnectionChecker interface {
	IsConnected() bool
	OwnJID() types.JID
}

// Config holds the scheduler configuration.
type Config struct {
	Timezone string
	Enabled  bool
}

// Service manages recurring scheduled commands.
type Service struct {
	store    Store
	executor CommandExecutor
	checker  ConnectionChecker
	log      *slog.Logger
	tracer   trace.Tracer
	cfg      Config
	cron      *cron.Cron
	entries   map[uuid.UUID]cron.EntryID
	location  *time.Location
}

// New creates a scheduler service. It does not start the cron runner; call Start after creation.
func New(store Store, executor CommandExecutor, checker ConnectionChecker, log *slog.Logger, tp trace.TracerProvider, cfg Config) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("scheduler store is required")
	}
	if executor == nil {
		return nil, fmt.Errorf("command executor is required")
	}
	if checker == nil {
		return nil, fmt.Errorf("connection checker is required")
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid scheduler timezone %q: %w", cfg.Timezone, err)
	}

	return &Service{
		store:    store,
		executor: executor,
		checker:  checker,
		log:      log,
		tracer:   tp.Tracer("services/scheduler"),
		cfg:      cfg,
		entries:  make(map[uuid.UUID]cron.EntryID),
		location: loc,
	}, nil
}

// Start loads existing schedules from the database and starts the cron runner.
func (s *Service) Start(ctx context.Context) error {
	s.cron = cron.New(cron.WithLocation(s.location))

	cmds, err := s.store.ListScheduledCommands(ctx)
	if err != nil {
		return fmt.Errorf("failed to load scheduled commands: %w", err)
	}

	for _, cmd := range cmds {
		if !cmd.Enabled {
			continue
		}
		if err := s.addCronJob(cmd); err != nil {
			s.log.Error("Failed to schedule command on startup", "id", cmd.ID, "error", err)
		}
	}

	s.cron.Start()
	s.log.Info("Scheduler started", "timezone", s.cfg.Timezone, "schedules", len(s.entries))
	return nil
}

// Stop halts the cron runner.
func (s *Service) Stop() {
	if s.cron != nil {
		ctx := s.cron.Stop()
		<-ctx.Done()
		s.log.Info("Scheduler stopped")
	}
}

// Create validates and persists a new scheduled command, then adds it to the cron runner.
func (s *Service) Create(ctx context.Context, cmd model.ScheduledCommand) (uuid.UUID, error) {
	ctx, span := s.tracer.Start(ctx, "scheduler.Create")
	defer span.End()

	if err := s.validate(cmd); err != nil {
		span.SetAttributes(attribute.String("validation_error", err.Error()))
		return uuid.Nil, err
	}

	cmd.Enabled = true
	id, err := s.store.CreateScheduledCommand(ctx, cmd)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to store scheduled command: %w", err)
	}
	cmd.ID = id

	if s.cron != nil {
		if err := s.addCronJob(cmd); err != nil {
			s.log.Error("Failed to add cron job after create", "id", id.String(), "error", err)
		}
	}

	s.log.Info("Created scheduled command", "id", id.String(), "schedule", cmd.Schedule, "command", cmd.Command)
	return id, nil
}

// List returns all scheduled commands.
func (s *Service) List(ctx context.Context) ([]model.ScheduledCommand, error) {
	return s.store.ListScheduledCommands(ctx)
}

// Delete removes a scheduled command from the cron runner and the database.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if s.cron != nil {
		if entryID, ok := s.entries[id]; ok {
			s.cron.Remove(entryID)
			delete(s.entries, id)
		}
	}

	if err := s.store.DeleteScheduledCommand(ctx, id); err != nil {
		return fmt.Errorf("failed to delete scheduled command: %w", err)
	}

	s.log.Info("Deleted scheduled command", "id", id.String())
	return nil
}

// validate checks that the schedule, group JID and command are valid.
func (s *Service) validate(cmd model.ScheduledCommand) error {
	if cmd.Name == "" {
		return fmt.Errorf("name is required")
	}
	if cmd.Schedule == "" {
		return fmt.Errorf("schedule is required")
	}
	if _, err := cron.ParseStandard(cmd.Schedule); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	if cmd.Command == "" {
		return fmt.Errorf("command is required")
	}
	if _, err := s.executor.Match(cmd.Command); err != nil {
		return fmt.Errorf("command does not match any registered handler: %w", err)
	}
	if cmd.GroupJID == "" {
		return fmt.Errorf("group_jid is required")
	}
	groupJID, err := types.ParseJID(cmd.GroupJID)
	if err != nil || groupJID.User == "" || groupJID.Server != types.GroupServer {
		return fmt.Errorf("invalid group JID: %s", cmd.GroupJID)
	}
	return nil
}

// addCronJob registers a cron job for an existing scheduled command.
func (s *Service) addCronJob(cmd model.ScheduledCommand) error {
	id := cmd.ID
	entryID, err := s.cron.AddFunc(cmd.Schedule, func() {
		s.runJob(id, cmd.Schedule, cmd.Command, cmd.GroupJID)
	})
	if err != nil {
		return fmt.Errorf("failed to add cron job for schedule %q: %w", cmd.Schedule, err)
	}
	s.entries[id] = entryID
	return nil
}

// runJob executes a single scheduled command.
func (s *Service) runJob(id uuid.UUID, schedule, command, groupJIDStr string) {
	ctx, span := s.tracer.Start(context.Background(), "scheduler.runJob",
		trace.WithAttributes(
			attribute.String("id", id.String()),
			attribute.String("schedule", schedule),
			attribute.String("command", command),
			attribute.String("group_jid", groupJIDStr),
		))
	defer span.End()

	log := s.log.With("id", id, "schedule", schedule, "command", command, "group_jid", groupJIDStr)

	if !s.checker.IsConnected() {
		log.Warn("Skipping scheduled command: WhatsApp not connected")
		return
	}

	groupJID, err := types.ParseJID(groupJIDStr)
	if err != nil {
		log.Error("Invalid group JID in scheduled command", "error", err)
		return
	}

	ownJID := s.checker.OwnJID()
	log.Info("Executing scheduled command")

	if err := s.executor.ExecuteCommand(ctx, command, groupJID, ownJID); err != nil {
		span.RecordError(err)
		log.Error("Scheduled command failed", "error", err)
		return
	}
	log.Info("Scheduled command executed successfully")
}
