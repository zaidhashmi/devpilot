package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/devpilot/devpilot/services/api/internal/platform"
)

const maxWebhookBytes = 1 << 20

func (h *Handler) githubIntegration(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	value, err := h.platform.GitHubInstallation(r.Context(), actor)
	if err != nil {
		h.platformError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": h.githubEnabled, "installation": value})
}

func (h *Handler) githubInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !h.githubEnabled {
		writeError(w, http.StatusServiceUnavailable, "github_not_configured", "GitHub integration is not configured", requestID(r))
		return
	}
	state, err := h.platform.BeginGitHubInstallation(r.Context(), actor)
	if err != nil {
		h.platformError(w, r, err)
		return
	}
	location := "https://github.com/apps/" + url.PathEscape(h.githubAppSlug) + "/installations/new?state=" + url.QueryEscape(state)
	http.Redirect(w, r, location, http.StatusFound)
}

func (h *Handler) githubCallback(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil || installationID <= 0 || r.URL.Query().Get("state") == "" {
		writeError(w, http.StatusBadRequest, "invalid_callback", "GitHub callback parameters are invalid", requestID(r))
		return
	}
	_, err = h.platform.CompleteGitHubInstallation(r.Context(), actor, r.URL.Query().Get("state"), installationID, requestID(r))
	if err != nil {
		h.platformError(w, r, err)
		return
	}
	http.Redirect(w, r, h.allowedOrigin+"/app/settings/integrations/github?connected=1", http.StatusFound)
}

func (h *Handler) githubSync(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	count, err := h.platform.SyncGitHub(r.Context(), actor, requestID(r))
	if err != nil {
		h.platformError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"repository_count": count})
}
func (h *Handler) githubDisconnect(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.authenticatedMutation(w, r)
	if !ok {
		return
	}
	if err := h.platform.DisconnectGitHub(r.Context(), actor, requestID(r)); err != nil {
		h.platformError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) repositories(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	repos, err := h.platform.Repositories(r.Context(), actor)
	if err != nil {
		h.platformError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
}

func (h *Handler) authenticatedMutation(w http.ResponseWriter, r *http.Request) (platform.Actor, bool) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return platform.Actor{}, false
	}
	if !h.validCSRF(r) {
		writeError(w, http.StatusForbidden, "csrf_rejected", "CSRF validation failed", requestID(r))
		return platform.Actor{}, false
	}
	return actor, true
}

func (h *Handler) githubWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.githubEnabled {
		writeError(w, http.StatusServiceUnavailable, "github_not_configured", "GitHub integration is not configured", requestID(r))
		return
	}
	signature := r.Header.Get("X-Hub-Signature-256")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	eventType := r.Header.Get("X-GitHub-Event")
	if signature == "" || deliveryID == "" || eventType == "" || len(deliveryID) > 128 || len(eventType) > 128 {
		writeError(w, http.StatusUnauthorized, "invalid_webhook", "Webhook authentication failed", requestID(r))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Webhook payload is too large", requestID(r))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_webhook", "Webhook payload is invalid", requestID(r))
		return
	}
	if !verifyWebhookSignature([]byte(h.githubWebhookSecret), payload, signature) {
		writeError(w, http.StatusUnauthorized, "invalid_webhook", "Webhook authentication failed", requestID(r))
		return
	}
	duplicate, err := h.platform.ProcessGitHubWebhook(r.Context(), deliveryID, eventType, payload)
	if err != nil {
		h.platformError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"duplicate": duplicate})
}

func verifyWebhookSignature(secret, payload []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return hmac.Equal(provided, mac.Sum(nil))
}

func (h *Handler) platformError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, platform.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "Insufficient organization permission", requestID(r))
	case errors.Is(err, platform.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "GitHub installation was not found", requestID(r))
	case errors.Is(err, platform.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "GitHub integration state does not allow this operation", requestID(r))
	case errors.Is(err, platform.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "github_unavailable", "GitHub integration is unavailable", requestID(r))
	case errors.Is(err, platform.ErrInvalidWebhook):
		writeError(w, http.StatusBadRequest, "invalid_webhook", "Webhook payload is invalid", requestID(r))
	case errors.Is(err, githubapp.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "github_rate_limited", "GitHub rate limit reached; try again later", requestID(r))
	case errors.Is(err, githubapp.ErrUnauthorized), errors.Is(err, githubapp.ErrForbidden), errors.Is(err, githubapp.ErrNotFound):
		writeError(w, http.StatusBadGateway, "github_access_failed", "GitHub installation access is unavailable", requestID(r))
	case errors.Is(err, githubapp.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "github_unavailable", "GitHub is temporarily unavailable", requestID(r))
	default:
		h.internalError(w, r, err)
	}
}
