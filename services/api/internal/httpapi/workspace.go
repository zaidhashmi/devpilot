package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/devpilot/devpilot/services/api/internal/platform"
)

type createWorkspaceRequest struct {
	Ref string `json:"ref"`
}

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	var body createWorkspaceRequest
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	body.Ref = strings.TrimSpace(body.Ref)
	if body.Ref == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "A repository ref is required", requestID(r))
		return
	}
	value, err := h.platform.CreateWorkspace(r.Context(), actor, r.PathValue("repositoryID"), body.Ref, requestID(r))
	if err != nil {
		h.workspaceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, value)
}

func (h *Handler) workspace(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	value, err := h.platform.Workspace(r.Context(), actor, r.PathValue("workspaceID"))
	if err != nil {
		h.workspaceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) cancelWorkspace(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	if err := h.platform.CancelWorkspace(r.Context(), actor, r.PathValue("workspaceID"), requestID(r)); err != nil {
		h.workspaceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) workspaceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, platform.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "Repository ref is invalid", requestID(r))
	case errors.Is(err, platform.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Repository or workspace was not found", requestID(r))
	case errors.Is(err, platform.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "Insufficient workspace permission", requestID(r))
	case errors.Is(err, platform.ErrConflict):
		writeError(w, http.StatusConflict, "workspace_not_active", "Workspace is no longer active", requestID(r))
	case errors.Is(err, platform.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "workspace_runner_unavailable", "Repository inspection is unavailable", requestID(r))
	case errors.Is(err, githubapp.ErrForbidden):
		writeError(w, http.StatusConflict, "github_contents_permission_required", "The GitHub App installation must grant read-only repository contents access", requestID(r))
	default:
		h.platformError(w, r, err)
	}
}
