package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/database"
	"github.com/devpilot/devpilot/services/api/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func githubHandler(t *testing.T) http.Handler {
	t.Helper()
	databaseURL := os.Getenv("DEVPILOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DEVPILOT_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(ctx, connection); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close(ctx)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(99887767)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = lock.Exec(context.Background(), `SELECT pg_advisory_unlock(99887767)`); lock.Release() })
	if _, err = pool.Exec(ctx, `TRUNCATE github_webhook_deliveries,audit_events,sessions,organization_memberships,organizations,users CASCADE`); err != nil {
		t.Fatal(err)
	}
	return NewWithGitHub(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), pool, platform.New(pool, time.Hour), false, "http://localhost:3000", true, "devpilot-test", "webhook-secret")
}

func signature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGitHubWebhookVerification(t *testing.T) {
	h := githubHandler(t)
	payload := []byte(`{"action":"created","installation":{"id":999}}`)
	tests := []struct {
		name, signature string
		body            []byte
		want            int
	}{{"missing", "", payload, http.StatusUnauthorized}, {"invalid", "sha256=00", payload, http.StatusUnauthorized}, {"mutated", signature("webhook-secret", payload), append(append([]byte{}, payload...), ' '), http.StatusUnauthorized}, {"malformed", signature("webhook-secret", []byte(`{`)), []byte(`{`), http.StatusBadRequest}, {"valid", signature("webhook-secret", payload), payload, http.StatusAccepted}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/webhook", bytes.NewReader(tt.body))
			request.Header.Set("X-GitHub-Delivery", "delivery-"+tt.name)
			request.Header.Set("X-GitHub-Event", "installation")
			if tt.signature != "" {
				request.Header.Set("X-Hub-Signature-256", tt.signature)
			}
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			if recorder.Code != tt.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	large := []byte(strings.Repeat("x", maxWebhookBytes+1))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/webhook", bytes.NewReader(large))
	request.Header.Set("X-GitHub-Delivery", "delivery-large")
	request.Header.Set("X-GitHub-Event", "installation")
	request.Header.Set("X-Hub-Signature-256", signature("webhook-secret", large))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status=%d", recorder.Code)
	}
}
