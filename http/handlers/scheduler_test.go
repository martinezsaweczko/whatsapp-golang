package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
)

// fakeSchedulerService is an in-memory scheduler service for tests.
type fakeSchedulerService struct {
	cmds []model.ScheduledCommand
}

func (f *fakeSchedulerService) Create(ctx context.Context, cmd model.ScheduledCommand) (int64, error) {
	if cmd.Schedule == "bad" {
		return 0, errors.New("invalid schedule")
	}
	cmd.ID = int64(len(f.cmds) + 1)
	f.cmds = append(f.cmds, cmd)
	return cmd.ID, nil
}

func (f *fakeSchedulerService) List(ctx context.Context) ([]model.ScheduledCommand, error) {
	return f.cmds, nil
}

func (f *fakeSchedulerService) Delete(ctx context.Context, id int64) error {
	for i, c := range f.cmds {
		if c.ID == id {
			f.cmds = append(f.cmds[:i], f.cmds[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

func newSchedulerHandler(t *testing.T) (*SchedulerHandler, *fakeSchedulerService) {
	svc := &fakeSchedulerService{}
	h, err := SchedulerHandlerConfig{
		Log:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Service:  svc,
		BasePath: "/api/v1",
	}.NewSchedulerHandler()
	if err != nil {
		t.Fatalf("NewSchedulerHandler failed: %v", err)
	}
	return h, svc
}

func TestSchedulerCreate(t *testing.T) {
	h, svc := newSchedulerHandler(t)

	body, _ := json.Marshal(createScheduleRequest{
		Name:     "morning",
		Schedule: "0 8 * * *",
		Command:  "electricidad",
		GroupJID: "123456789@g.us",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scheduler", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(svc.cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(svc.cmds))
	}
}

func TestSchedulerCreateValidation(t *testing.T) {
	h, _ := newSchedulerHandler(t)

	body, _ := json.Marshal(createScheduleRequest{
		Name:     "bad",
		Schedule: "bad",
		Command:  "x",
		GroupJID: "123@g.us",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scheduler", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSchedulerList(t *testing.T) {
	h, svc := newSchedulerHandler(t)
	svc.cmds = []model.ScheduledCommand{{ID: 1, Name: "morning", Schedule: "0 8 * * *", Command: "electricidad", GroupJID: "123@g.us"}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scheduler", nil)
	rec := httptest.NewRecorder()

	h.list(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp []scheduledCommandResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resp))
	}
}

func TestSchedulerDelete(t *testing.T) {
	h, svc := newSchedulerHandler(t)
	svc.cmds = []model.ScheduledCommand{{ID: 1, Name: "morning", Schedule: "0 8 * * *", Command: "electricidad", GroupJID: "123@g.us"}}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scheduler/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	h.delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if len(svc.cmds) != 0 {
		t.Fatalf("expected command to be deleted")
	}
}

func TestSchedulerDeleteNotFound(t *testing.T) {
	h, _ := newSchedulerHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scheduler/99", nil)
	req.SetPathValue("id", "99")
	rec := httptest.NewRecorder()

	h.delete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestSchedulerDeleteInvalidID(t *testing.T) {
	h, _ := newSchedulerHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/scheduler/abc", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()

	h.delete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
