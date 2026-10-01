package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/robfig/cron/v3"
	"go.mau.fi/whatsmeow/types"
	"go.opentelemetry.io/otel/trace/noop"
)

// fakeStore is an in-memory scheduler store for tests.
type fakeStore struct {
	cmds   []model.ScheduledCommand
	nextID int64
}

func (f *fakeStore) CreateScheduledCommand(ctx context.Context, cmd model.ScheduledCommand) (int64, error) {
	f.nextID++
	cmd.ID = f.nextID
	f.cmds = append(f.cmds, cmd)
	return f.nextID, nil
}

func (f *fakeStore) ListScheduledCommands(ctx context.Context) ([]model.ScheduledCommand, error) {
	return append([]model.ScheduledCommand(nil), f.cmds...), nil
}

func (f *fakeStore) DeleteScheduledCommand(ctx context.Context, id int64) error {
	for i, c := range f.cmds {
		if c.ID == id {
			f.cmds = append(f.cmds[:i], f.cmds[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

// fakeExecutor matches commands and records executions.
type fakeExecutor struct {
	commands []string
	execs    []struct {
		Body string
		Chat types.JID
	}
}

func (f *fakeExecutor) ExecuteCommand(ctx context.Context, body string, chat, senderJID types.JID) error {
	if _, err := f.Match(body); err != nil {
		return err
	}
	f.execs = append(f.execs, struct {
		Body string
		Chat types.JID
	}{Body: body, Chat: chat})
	return nil
}

func (f *fakeExecutor) Match(body string) (string, error) {
	for _, cmd := range f.commands {
		if body == cmd {
			return cmd, nil
		}
	}
	return "", errors.New("no match")
}

// fakeChecker reports connected and a fixed own JID.
type fakeChecker struct {
	connected bool
	ownJID    types.JID
}

func (f *fakeChecker) IsConnected() bool { return f.connected }
func (f *fakeChecker) OwnJID() types.JID { return f.ownJID }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestCreateValidatesCron(t *testing.T) {
	svc, err := New(&fakeStore{}, &fakeExecutor{}, &fakeChecker{}, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	_, err = svc.Create(context.Background(), model.ScheduledCommand{
		Name:     "bad",
		Schedule: "not a cron",
		Command:  "x",
		GroupJID: "123@g.us",
	})
	if err == nil {
		t.Fatal("expected error for invalid cron expression")
	}
}

func TestCreateValidatesCommand(t *testing.T) {
	svc, err := New(&fakeStore{}, &fakeExecutor{commands: []string{"electricidad"}}, &fakeChecker{}, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	_, err = svc.Create(context.Background(), model.ScheduledCommand{
		Name:     "bad",
		Schedule: "0 8 * * *",
		Command:  "unknown",
		GroupJID: "123@g.us",
	})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestCreateValidatesGroupJID(t *testing.T) {
	svc, err := New(&fakeStore{}, &fakeExecutor{commands: []string{"electricidad"}}, &fakeChecker{}, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	_, err = svc.Create(context.Background(), model.ScheduledCommand{
		Name:     "bad",
		Schedule: "0 8 * * *",
		Command:  "electricidad",
		GroupJID: "123@c.us",
	})
	if err == nil {
		t.Fatal("expected error for non-group JID")
	}
}

func TestCreateStoresAndRegistersCronJob(t *testing.T) {
	store := &fakeStore{}
	exec := &fakeExecutor{commands: []string{"electricidad"}}
	checker := &fakeChecker{connected: true, ownJID: types.NewJID("bot", types.DefaultUserServer)}
	svc, err := New(store, exec, checker, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer svc.Stop()

	id, err := svc.Create(context.Background(), model.ScheduledCommand{
		Name:     "morning",
		Schedule: "0 8 * * *",
		Command:  "electricidad",
		GroupJID: "123456789@g.us",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	if _, ok := svc.entries[id]; !ok {
		t.Fatal("expected cron entry to be registered")
	}
}

func TestRunJobExecutesWhenConnected(t *testing.T) {
	exec := &fakeExecutor{commands: []string{"electricidad"}}
	checker := &fakeChecker{connected: true, ownJID: types.NewJID("bot", types.DefaultUserServer)}
	svc, err := New(&fakeStore{}, exec, checker, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	svc.runJob(1, "0 8 * * *", "electricidad", "123456789@g.us")

	if len(exec.execs) != 1 {
		t.Fatalf("expected 1 execution, got %d", len(exec.execs))
	}
	if exec.execs[0].Body != "electricidad" {
		t.Fatalf("unexpected command body: %s", exec.execs[0].Body)
	}
	if exec.execs[0].Chat.String() != "123456789@g.us" {
		t.Fatalf("unexpected chat: %s", exec.execs[0].Chat.String())
	}
}

func TestDeleteRemovesJob(t *testing.T) {
	store := &fakeStore{}
	exec := &fakeExecutor{commands: []string{"electricidad"}}
	checker := &fakeChecker{connected: true, ownJID: types.NewJID("bot", types.DefaultUserServer)}
	svc, err := New(store, exec, checker, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer svc.Stop()

	id, err := svc.Create(context.Background(), model.ScheduledCommand{
		Name:     "morning",
		Schedule: "0 8 * * *",
		Command:  "electricidad",
		GroupJID: "123456789@g.us",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, ok := svc.entries[id]; !ok {
		t.Fatal("expected cron entry to be registered before delete")
	}

	if err := svc.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, ok := svc.entries[id]; ok {
		t.Fatal("expected cron entry to be removed after delete")
	}
}

func TestRunJobSkipsWhenDisconnected(t *testing.T) {
	store := &fakeStore{}
	exec := &fakeExecutor{commands: []string{"electricidad"}}
	checker := &fakeChecker{connected: false}
	svc, err := New(store, exec, checker, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Europe/Madrid"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	svc.runJob(1, "* * * * *", "electricidad", "123456789@g.us")

	if len(exec.execs) != 0 {
		t.Fatal("expected no execution when disconnected")
	}
}

func TestInvalidTimezone(t *testing.T) {
	_, err := New(&fakeStore{}, &fakeExecutor{}, &fakeChecker{}, testLogger(), noop.NewTracerProvider(), Config{Timezone: "Mars/Olympus"})
	if err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

func TestParseCron(t *testing.T) {
	if _, err := cron.ParseStandard("0 8 * * *"); err != nil {
		t.Fatalf("standard cron parse failed: %v", err)
	}
}
