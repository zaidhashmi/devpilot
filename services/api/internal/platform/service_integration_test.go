package platform

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("DEVPILOT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("DEVPILOT_TEST_DATABASE_URL is not set")
	}
	connection, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close(context.Background())
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	lock, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(context.Background(), `SELECT pg_advisory_lock(99887766)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = lock.Exec(context.Background(), `SELECT pg_advisory_unlock(99887766)`); lock.Release() })
	_, err = pool.Exec(context.Background(), `TRUNCATE audit_events,sessions,organization_memberships,organizations,users CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	return New(pool, time.Hour), pool
}

func registration(email, org string) Registration {
	return Registration{Email: email, DisplayName: "Test User", Password: "correct horse battery staple", OrganizationName: org, RequestID: "req-test", UserAgent: "test"}
}

func TestPlatformCoreIntegration(t *testing.T) {
	s, pool := integrationService(t)
	ctx := context.Background()
	first, err := s.Register(ctx, registration("User@Example.com", "Organization A"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Actor.Membership.Role != "owner" {
		t.Fatalf("role=%s", first.Actor.Membership.Role)
	}
	var normalized string
	if err := pool.QueryRow(ctx, `SELECT normalized_email FROM users WHERE id=$1`, first.Actor.User.ID).Scan(&normalized); err != nil {
		t.Fatal(err)
	}
	if normalized != "user@example.com" {
		t.Fatalf("normalized=%s", normalized)
	}
	_, err = pool.Exec(ctx, `INSERT INTO users(id,email,normalized_email,display_name,password_hash) VALUES($1,'Case@Example.com','wrong@example.com','Invalid','hash')`, uuid.Must(uuid.NewV7()))
	if err == nil {
		t.Fatal("normalized email invariant should reject inconsistent values")
	}
	if _, err := s.Register(ctx, registration(" user@example.COM ", "Duplicate")); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate error=%v", err)
	}

	before := rowCount(t, pool, "users")
	bad := registration("rollback@example.com", strings.Repeat("x", 121))
	if _, err := s.Register(ctx, bad); err == nil {
		t.Fatal("expected failed registration")
	}
	if got := rowCount(t, pool, "users"); got != before {
		t.Fatalf("partial registration: users %d -> %d", before, got)
	}

	login, err := s.Login(ctx, "USER@example.com", "correct horse battery staple", "req-login", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "user@example.com", "wrong password", "req-bad", "test"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid login error=%v", err)
	}
	actor, err := s.Authenticate(ctx, login.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Register(ctx, registration("second@example.com", "Organization B"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, login.Token, second.Actor.Organization.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant error=%v", err)
	}
	updated, err := s.UpdateOrganization(ctx, actor, "Organization A Updated", "req-update")
	if err != nil || updated.Name != "Organization A Updated" {
		t.Fatalf("owner update: organization=%+v error=%v", updated, err)
	}
	members, err := s.Members(ctx, actor)
	if err != nil || len(members) != 1 {
		t.Fatalf("members=%d error=%v", len(members), err)
	}

	_, err = pool.Exec(ctx, `UPDATE organization_memberships SET role='member' WHERE id=$1`, actor.Membership.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor.Membership.Role = "member"
	if _, err := s.UpdateOrganization(ctx, actor, "Forbidden update", "req-forbidden"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member update error=%v", err)
	}

	if err := s.Logout(ctx, login.Actor, "req-logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, login.Token, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session error=%v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, first.Actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, first.Token, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session error=%v", err)
	}

	var events int
	var leaked bool
	if err := pool.QueryRow(ctx, `SELECT count(*),bool_or(metadata::text LIKE '%correct horse%' OR metadata::text LIKE '%' || $1 || '%') FROM audit_events`, login.Token).Scan(&events, &leaked); err != nil {
		t.Fatal(err)
	}
	if events < 7 || leaked {
		t.Fatalf("audit events=%d leaked=%v", events, leaked)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_events SET event_type='tampered'`); err == nil {
		t.Fatal("audit update should be rejected")
	}

	_, err = pool.Exec(ctx, `INSERT INTO organization_memberships(id,organization_id,user_id,role,status) VALUES($1,$2,$3,'member','active')`, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), first.Actor.User.ID)
	if err == nil {
		t.Fatal("foreign key should reject unknown organization")
	}
}

func rowCount(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
