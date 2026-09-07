package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/devpilot/devpilot/services/api/internal/platform"
)

type createTaskRequest struct {
	Title     string `json:"title"`
	Objective string `json:"objective"`
}
type createRunRequest struct {
	Ref string `json:"ref"`
}
type decisionRequest struct {
	Comment string `json:"comment"`
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	var body createTaskRequest
	if decodeJSON(w, r, &body) != nil {
		return
	}
	value, err := h.platform.CreateTask(r.Context(), actor, r.PathValue("repositoryID"), body.Title, body.Objective, requestID(r))
	if err != nil {
		h.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}
func (h *Handler) tasks(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	items, err := h.platform.Tasks(r.Context(), actor)
	if err != nil {
		h.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": items})
}
func (h *Handler) task(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	value, runs, err := h.platform.Task(r.Context(), actor, r.PathValue("taskID"))
	if err != nil {
		h.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": value, "runs": runs})
}
func (h *Handler) createTaskRun(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	var body createRunRequest
	if decodeJSON(w, r, &body) != nil {
		return
	}
	value, err := h.platform.CreateTaskRun(r.Context(), actor, r.PathValue("taskID"), strings.TrimSpace(body.Ref), requestID(r))
	if err != nil {
		h.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, value)
}
func (h *Handler) taskRun(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	value, err := h.platform.TaskRun(r.Context(), actor, r.PathValue("runID"))
	if err != nil {
		h.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (h *Handler) cancelTaskRun(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	if err := h.platform.CancelTaskRun(r.Context(), actor, r.PathValue("runID"), requestID(r)); err != nil {
		h.taskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) cancelTask(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	if err := h.platform.CancelTask(r.Context(), actor, r.PathValue("taskID"), requestID(r)); err != nil {
		h.taskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) approvals(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	items, err := h.platform.Approvals(r.Context(), actor, r.PathValue("runID"))
	if err != nil {
		h.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": items})
}
func (h *Handler) approve(w http.ResponseWriter, r *http.Request) { h.decide(w, r, "approved") }
func (h *Handler) reject(w http.ResponseWriter, r *http.Request)  { h.decide(w, r, "rejected") }
func (h *Handler) decide(w http.ResponseWriter, r *http.Request, decision string) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	var body decisionRequest
	if decodeJSON(w, r, &body) != nil {
		return
	}
	value, err := h.platform.DecideApproval(r.Context(), actor, r.PathValue("approvalID"), decision, body.Comment, requestID(r))
	if err != nil {
		h.taskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (h *Handler) taskError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, platform.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "Task input is invalid", requestID(r))
	case errors.Is(err, platform.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Task resource was not found", requestID(r))
	case errors.Is(err, platform.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "Insufficient task permission", requestID(r))
	case errors.Is(err, platform.ErrConflict):
		writeError(w, http.StatusConflict, "invalid_state", "The requested transition is no longer valid", requestID(r))
	case errors.Is(err, platform.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "workflow_unavailable", "Workflow processing is temporarily unavailable", requestID(r))
	case errors.Is(err, githubapp.ErrForbidden):
		writeError(w, http.StatusConflict, "github_contents_permission_required", "Read-only repository contents access is required", requestID(r))
	default:
		h.platformError(w, r, err)
	}
}
