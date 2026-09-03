package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionCookie = "devpilot_session"
	csrfCookie    = "devpilot_csrf"
	maxBodyBytes  = 64 << 10
)

type contextKey string

const requestIDKey contextKey = "request_id"

type Handler struct {
	logger        *slog.Logger
	db            *pgxpool.Pool
	platform      *platform.Service
	cookieSecure  bool
	allowedOrigin string
	loginLimiter  *rateLimiter
}

func New(logger *slog.Logger, db *pgxpool.Pool, service *platform.Service, cookieSecure bool, allowedOrigin string) http.Handler {
	h := &Handler{logger: logger, db: db, platform: service, cookieSecure: cookieSecure, allowedOrigin: allowedOrigin, loginLimiter: newRateLimiter(10, time.Minute)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.ready)
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/me", h.me)
	mux.HandleFunc("GET /api/v1/organization", h.organization)
	mux.HandleFunc("PATCH /api/v1/organization", h.updateOrganization)
	mux.HandleFunc("GET /api/v1/organization/members", h.members)
	return h.requestContext(mux)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "PostgreSQL is unavailable", requestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

type registrationRequest struct {
	Email            string `json:"email"`
	DisplayName      string `json:"display_name"`
	Password         string `json:"password"`
	OrganizationName string `json:"organization_name"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if !h.validOrigin(r) {
		writeError(w, http.StatusForbidden, "csrf_rejected", "Request origin is not allowed", requestID(r))
		return
	}
	var body registrationRequest
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	if !validEmail(body.Email) || len(strings.TrimSpace(body.DisplayName)) < 1 || len(strings.TrimSpace(body.OrganizationName)) < 1 || len(body.Password) < 12 || len(body.Password) > 128 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "Valid account fields and a 12–128 character password are required", requestID(r))
		return
	}
	session, err := h.platform.Register(r.Context(), platform.Registration{Email: body.Email, DisplayName: body.DisplayName, Password: body.Password, OrganizationName: body.OrganizationName, RequestID: requestID(r), UserAgent: r.UserAgent()})
	if errors.Is(err, platform.ErrConflict) {
		writeError(w, http.StatusConflict, "registration_failed", "Unable to create account with the supplied details", requestID(r))
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	h.setSessionCookies(w, session)
	writeJSON(w, http.StatusCreated, session.Actor)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.validOrigin(r) {
		writeError(w, http.StatusForbidden, "csrf_rejected", "Request origin is not allowed", requestID(r))
		return
	}
	if !h.loginLimiter.Allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts; try again later", requestID(r))
		return
	}
	var body loginRequest
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	session, err := h.platform.Login(r.Context(), body.Email, body.Password, requestID(r), r.UserAgent())
	if errors.Is(err, platform.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect", requestID(r))
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	h.setSessionCookies(w, session)
	writeJSON(w, http.StatusOK, session.Actor)
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !h.validCSRF(r) {
		writeError(w, http.StatusForbidden, "csrf_rejected", "CSRF validation failed", requestID(r))
		return
	}
	if err := h.platform.Logout(r.Context(), actor, requestID(r)); err != nil {
		h.internalError(w, r, err)
		return
	}
	h.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if ok {
		writeJSON(w, http.StatusOK, actor)
	}
}
func (h *Handler) organization(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if ok {
		writeJSON(w, http.StatusOK, actor.Organization)
	}
}

type updateOrganizationRequest struct {
	Name string `json:"name"`
}

func (h *Handler) updateOrganization(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !h.validCSRF(r) {
		writeError(w, http.StatusForbidden, "csrf_rejected", "CSRF validation failed", requestID(r))
		return
	}
	var body updateOrganizationRequest
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	if len(strings.TrimSpace(body.Name)) < 1 || len(strings.TrimSpace(body.Name)) > 120 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "Organization name must be between 1 and 120 characters", requestID(r))
		return
	}
	org, err := h.platform.UpdateOrganization(r.Context(), actor, body.Name, requestID(r))
	if errors.Is(err, platform.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "Insufficient organization permission", requestID(r))
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}
func (h *Handler) members(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	members, err := h.platform.Members(r.Context(), actor)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (h *Handler) requireActor(w http.ResponseWriter, r *http.Request) (platform.Actor, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", requestID(r))
		return platform.Actor{}, false
	}
	actor, err := h.platform.Authenticate(r.Context(), cookie.Value, r.Header.Get("X-Organization-ID"))
	if errors.Is(err, platform.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "Organization access denied", requestID(r))
		return platform.Actor{}, false
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", requestID(r))
		return platform.Actor{}, false
	}
	return actor, true
}
func (h *Handler) setSessionCookies(w http.ResponseWriter, session platform.Session) {
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: session.Token, Path: "/", Expires: session.ExpiresAt, MaxAge: maxAge, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: randomToken(), Path: "/", Expires: session.ExpiresAt, MaxAge: maxAge, HttpOnly: false, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode})
}
func (h *Handler) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: name == sessionCookie, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode})
	}
}
func (h *Handler) validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == h.allowedOrigin
}
func (h *Handler) validCSRF(r *http.Request) bool {
	if !h.validOrigin(r) {
		return false
	}
	cookie, err := r.Cookie(csrfCookie)
	return err == nil && cookie.Value != "" && cookie.Value == r.Header.Get("X-CSRF-Token")
}
func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", requestID(r))
		return errors.New("content type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		status := http.StatusBadRequest
		code := "invalid_json"
		if strings.Contains(err.Error(), "request body too large") {
			status = http.StatusRequestEntityTooLarge
			code = "request_too_large"
		}
		writeError(w, status, code, "Request body is invalid", requestID(r))
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON object", requestID(r))
		return errors.New("multiple JSON values")
	}
	return nil
}
func (h *Handler) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 128 {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
		next.ServeHTTP(w, r)
		h.logger.Info("http request", "request_id", id, "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}
func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("request failed", "request_id", requestID(r), "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred", requestID(r))
}
func requestID(r *http.Request) string {
	value, _ := r.Context().Value(requestIDKey).(string)
	return value
}
func newRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(bytes)
}
func randomToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}
func validEmail(value string) bool {
	value = strings.TrimSpace(value)
	at := strings.LastIndex(value, "@")
	return at > 0 && at < len(value)-3 && len(value) <= 320
}
func clientIP(r *http.Request) string {
	if address, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return address.Addr().String()
	}
	return r.RemoteAddr
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message, id string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": id}})
}

type rateEntry struct {
	count int
	reset time.Time
}
type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]rateEntry
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, entries: map[string]rateEntry{}}
}
func (l *rateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry := l.entries[key]
	if now.After(entry.reset) {
		entry = rateEntry{reset: now.Add(l.window)}
	}
	entry.count++
	l.entries[key] = entry
	return entry.count <= l.limit
}
