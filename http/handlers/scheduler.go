package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/martinezsaweczko/whatsappBot-golang/http/middleware"
	"github.com/martinezsaweczko/whatsappBot-golang/model"
)

// SchedulerService is the interface consumed by the scheduler HTTP handler.
type SchedulerService interface {
	Create(ctx context.Context, cmd model.ScheduledCommand) (uuid.UUID, error)
	List(ctx context.Context) ([]model.ScheduledCommand, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// SchedulerHandlerConfig holds dependencies for the scheduler handler.
type SchedulerHandlerConfig struct {
	Log      *slog.Logger
	Service  SchedulerService
	BasePath string
}

// SchedulerHandler exposes the scheduled commands REST API.
type SchedulerHandler struct {
	log      *slog.Logger
	service  SchedulerService
	basePath string
}

// createScheduleRequest is the body of POST /scheduler.
type createScheduleRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	GroupJID string `json:"group_jid"`
}

// scheduledCommandResponse is the JSON representation of a scheduled command.
type scheduledCommandResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Command   string `json:"command"`
	GroupJID  string `json:"group_jid"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// NewSchedulerHandler creates a new scheduler handler.
func (c SchedulerHandlerConfig) NewSchedulerHandler() (*SchedulerHandler, error) {
	if c.Log == nil {
		return nil, fmt.Errorf("scheduler handler logger is required")
	}
	if c.Service == nil {
		return nil, fmt.Errorf("scheduler service is required")
	}
	return &SchedulerHandler{
		log:      c.Log,
		service:  c.Service,
		basePath: c.BasePath,
	}, nil
}

// RegisterRoutes registers POST/GET/DELETE {basePath}/scheduler.
func (h *SchedulerHandler) RegisterRoutes(router Router, middlewares ...middleware.Middleware) {
	path := h.basePath + "/scheduler"
	idPath := path + "/{id}"

	register := func(method, pattern string, handler func(http.ResponseWriter, *http.Request)) {
		if len(middlewares) > 0 {
			router.HandleFuncWithMiddleware(method+" "+pattern, handler, middlewares...)
		} else {
			router.HandleFunc(method+" "+pattern, handler)
		}
	}

	register("POST", path, h.create)
	register("GET", path, h.list)
	register("DELETE", idPath, h.delete)
}

// Create a scheduled command.
//
//	@Summary      Create a scheduled command
//	@Description  Schedules a WhatsApp command to run automatically in a group using a cron expression.
//	@Tags         Scheduler
//	@Accept       json
//	@Produce      json
//	@Param        request body createScheduleRequest true "Scheduled command"
//	@Success      201 {object} scheduledCommandResponse
//	@Failure      400 {object} errorResponse
//	@Router       /api/v1/scheduler [post]
func (h *SchedulerHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createScheduleRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Schedule = strings.TrimSpace(req.Schedule)
	req.Command = strings.TrimSpace(req.Command)
	req.GroupJID = strings.TrimSpace(req.GroupJID)

	cmd := model.ScheduledCommand{
		Name:     req.Name,
		Schedule: req.Schedule,
		Command:  req.Command,
		GroupJID: req.GroupJID,
		Enabled:  true,
	}

	id, err := h.service.Create(r.Context(), cmd)
	if err != nil {
		h.log.Error("Failed to create scheduled command", "error", err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, scheduledCommandResponse{
		ID:        id.String(),
		Name:      cmd.Name,
		Schedule:  cmd.Schedule,
		Command:   cmd.Command,
		GroupJID:  cmd.GroupJID,
		Enabled:   true,
		CreatedAt: "",
		UpdatedAt: "",
	})
}

// List scheduled commands.
//
//	@Summary      List scheduled commands
//	@Description  Returns all scheduled commands.
//	@Tags         Scheduler
//	@Produce      json
//	@Success      200 {array} scheduledCommandResponse
//	@Failure      502 {object} errorResponse
//	@Router       /api/v1/scheduler [get]
func (h *SchedulerHandler) list(w http.ResponseWriter, r *http.Request) {
	cmds, err := h.service.List(r.Context())
	if err != nil {
		h.log.Error("Failed to list scheduled commands", "error", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "failed to list scheduled commands"})
		return
	}

	resp := make([]scheduledCommandResponse, 0, len(cmds))
	for _, c := range cmds {
		resp = append(resp, scheduledCommandResponse{
			ID:        c.ID.String(),
			Name:      c.Name,
			Schedule:  c.Schedule,
			Command:   c.Command,
			GroupJID:  c.GroupJID,
			Enabled:   c.Enabled,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
			UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// Delete a scheduled command.
//
//	@Summary      Delete a scheduled command
//	@Description  Removes a scheduled command by ID.
//	@Tags         Scheduler
//	@Param        id path string true "Schedule UUID"
//	@Success      204
//	@Failure      400 {object} errorResponse
//	@Failure      404 {object} errorResponse
//	@Router       /api/v1/scheduler/{id} [delete]
func (h *SchedulerHandler) delete(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSpace(r.PathValue("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid schedule id"})
		return
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		status := http.StatusInternalServerError
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		h.log.Error("Failed to delete scheduled command", "id", id.String(), "error", err)
		writeJSON(w, status, errorResponse{Error: "failed to delete scheduled command"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
