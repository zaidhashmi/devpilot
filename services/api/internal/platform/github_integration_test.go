package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/githubapp"
)

type fakeGitHub struct {
	installation     githubapp.Installation
	userInstallation githubapp.Installation
	repositories     []githubapp.Repository
	exchangeErr      error
	userErr          error
	getErr           error
	listErr          error
	deleteErr        error
	userToken        string
	deleted          bool
}

func (f *fakeGitHub) ExchangeUserCode(context.Context, string) (string, error) {
	if f.exchangeErr != nil {
		return "", f.exchangeErr
	}
	if f.userToken == "" {
		return "github-user-token-sensitive", nil
	}
	return f.userToken, nil
}
func (f *fakeGitHub) GetUserInstallation(_ context.Context, token string, _ int64) (githubapp.Installation, error) {
	if f.userErr != nil {
		return githubapp.Installation{}, f.userErr
	}
	if token == "" {
		return githubapp.Installation{}, githubapp.ErrUnauthorized
	}
	if f.userInstallation.ID != 0 {
		return f.userInstallation, nil
	}
	return f.installation, nil
}
func (f *fakeGitHub) GetInstallation(context.Context, int64) (githubapp.Installation, error) {
	return f.installation, f.getErr
}
func (f *fakeGitHub) ListRepositories(context.Context, int64) ([]githubapp.Repository, error) {
	return f.repositories, f.listErr
}
func (f *fakeGitHub) DeleteInstallation(context.Context, int64) error {
	f.deleted = true
	return f.deleteErr
}

func TestGitHubIntegrationTenantSafetyAndReconciliation(t *testing.T) {
	s, pool := integrationService(t)
	ctx := context.Background()
	fake := &fakeGitHub{installation: githubapp.Installation{ID: 101, AccountID: 202, AccountLogin: "octo-org", AccountType: "Organization", RepositorySelection: "selected"}, repositories: []githubapp.Repository{{ID: 301, Owner: "octo-org", Name: "private-one", FullName: "octo-org/private-one", DefaultBranch: "main", Private: true, HTMLURL: "https://github.com/octo-org/private-one"}}}
	s.SetGitHubClient(fake)
	a, err := s.Register(ctx, registration("owner-a@example.com", "Organization A"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Register(ctx, registration("owner-b@example.com", "Organization B"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.BeginGitHubInstallation(ctx, a.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteGitHubInstallation(ctx, b.Actor, state, "code", 101, "req-attack"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant callback error=%v", err)
	}
	installation, err := s.CompleteGitHubInstallation(ctx, a.Actor, state, "code", 101, "req-connect")
	if err != nil {
		t.Fatal(err)
	}
	if installation.RepositoryCount != 1 {
		t.Fatalf("repository count=%d", installation.RepositoryCount)
	}
	if _, err = s.CompleteGitHubInstallation(ctx, a.Actor, state, "code", 101, "req-replay"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("state replay error=%v", err)
	}
	repos, err := s.Repositories(ctx, a.Actor)
	if err != nil || len(repos) != 1 || !repos[0].Private {
		t.Fatalf("repositories=%+v error=%v", repos, err)
	}
	other, err := s.Repositories(ctx, b.Actor)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross tenant repositories=%+v error=%v", other, err)
	}
	if err = s.DisconnectGitHub(ctx, b.Actor, "req-cross-tenant"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant disconnect error=%v", err)
	}

	fake.repositories = []githubapp.Repository{{ID: 302, Owner: "octo-org", Name: "new", FullName: "octo-org/new", DefaultBranch: "trunk", HTMLURL: "https://github.com/octo-org/new"}}
	count, err := s.SyncGitHub(ctx, a.Actor, "req-sync")
	if err != nil || count != 1 {
		t.Fatalf("sync count=%d err=%v", count, err)
	}
	repos, err = s.Repositories(ctx, a.Actor)
	if err != nil || len(repos) != 1 || repos[0].GitHubRepositoryID != 302 {
		t.Fatalf("reconciled=%+v err=%v", repos, err)
	}
	if _, err = s.SyncGitHub(ctx, a.Actor, "req-sync-2"); err != nil {
		t.Fatal(err)
	}
	var total int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM repositories WHERE organization_id=$1`, a.Actor.Organization.ID).Scan(&total); err != nil || total != 2 {
		t.Fatalf("total=%d err=%v", total, err)
	}

	a.Actor.Membership.Role = "member"
	if err = s.DisconnectGitHub(ctx, a.Actor, "req-member"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member disconnect=%v", err)
	}
	a.Actor.Membership.Role = "admin"
	if err = s.DisconnectGitHub(ctx, a.Actor, "req-disconnect"); err != nil {
		t.Fatal(err)
	}
	if !fake.deleted {
		t.Fatal("disconnect did not uninstall the GitHub App")
	}
	var available int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM repositories WHERE organization_id=$1 AND available`, a.Actor.Organization.ID).Scan(&available); err != nil || available != 0 {
		t.Fatalf("available=%d err=%v", available, err)
	}
	var leaked bool
	if err = pool.QueryRow(ctx, `SELECT coalesce(bool_or(metadata::text LIKE '%test-token-value%' OR metadata::text LIKE '%private-one%'),false) FROM audit_events`).Scan(&leaked); err != nil || leaked {
		t.Fatalf("audit leaked=%v err=%v", leaked, err)
	}
	var tokenColumns int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name IN ('github_installations','repositories','github_installation_states') AND column_name LIKE '%token%' AND column_name <> 'token_hash'`).Scan(&tokenColumns); err != nil || tokenColumns != 0 {
		t.Fatalf("persisted token columns=%d err=%v", tokenColumns, err)
	}
}

func TestGitHubInstallationBindingRequiresUserAuthorization(t *testing.T) {
	s, pool := integrationService(t)
	ctx := context.Background()
	actor, err := s.Register(ctx, registration("binding@example.com", "Binding Org"))
	if err != nil {
		t.Fatal(err)
	}
	base := githubapp.Installation{ID: 701, AccountID: 702, AccountLogin: "binding-org", AccountType: "Organization", RepositorySelection: "selected"}
	fake := &fakeGitHub{installation: base, userInstallation: base, userToken: "github-user-token-sensitive"}
	s.SetGitHubClient(fake)

	state, _ := s.BeginGitHubInstallation(ctx, actor.Actor)
	fake.exchangeErr = githubapp.ErrUnauthorized
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "bad-code", 701, "req-exchange-fail"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("exchange failure=%v", err)
	}
	fake.exchangeErr = nil
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "good-code", 701, "req-consumed"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("definitive failure state replay=%v", err)
	}

	state, _ = s.BeginGitHubInstallation(ctx, actor.Actor)
	fake.userErr = githubapp.ErrForbidden
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "code", 701, "req-user-denied"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized installation=%v", err)
	}
	fake.userErr = nil

	state, _ = s.BeginGitHubInstallation(ctx, actor.Actor)
	fake.userInstallation = githubapp.Installation{ID: 999, AccountID: 998, AccountLogin: "other-account", AccountType: "Organization", RepositorySelection: "all"}
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "code", 701, "req-spoof"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("spoofed installation=%v", err)
	}
	fake.userInstallation = base

	state, _ = s.BeginGitHubInstallation(ctx, actor.Actor)
	fake.userErr = githubapp.ErrUnavailable
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "code", 701, "req-transient"); !errors.Is(err, githubapp.ErrUnavailable) {
		t.Fatalf("transient verification=%v", err)
	}
	fake.userErr = nil
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "code", 701, "req-retry"); err != nil {
		t.Fatalf("retry after transient=%v", err)
	}
	bound, err := s.GitHubInstallation(ctx, actor.Actor)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(bound)
	if err != nil || strings.Contains(string(encoded), "github-user-token-sensitive") {
		t.Fatalf("user token exposed in response: %s err=%v", encoded, err)
	}

	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM github_installations WHERE organization_id=$1`, actor.Actor.Organization.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("bound installations=%d err=%v", count, err)
	}
	var leaked bool
	if err = pool.QueryRow(ctx, `SELECT coalesce(bool_or(metadata::text LIKE '%github-user-token-sensitive%'),false) FROM audit_events`).Scan(&leaked); err != nil || leaked {
		t.Fatalf("user token audited=%v err=%v", leaked, err)
	}
}

func TestGitHubDisconnectRemoteFailureAndNotFound(t *testing.T) {
	s, pool := integrationService(t)
	ctx := context.Background()
	actor, err := s.Register(ctx, registration("disconnect@example.com", "Disconnect Org"))
	if err != nil {
		t.Fatal(err)
	}
	installation := githubapp.Installation{ID: 801, AccountID: 802, AccountLogin: "disconnect-org", AccountType: "Organization", RepositorySelection: "all"}
	fake := &fakeGitHub{installation: installation, userInstallation: installation}
	s.SetGitHubClient(fake)
	state, _ := s.BeginGitHubInstallation(ctx, actor.Actor)
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "code", 801, "req-connect"); err != nil {
		t.Fatal(err)
	}
	fake.deleteErr = githubapp.ErrUnavailable
	if err = s.DisconnectGitHub(ctx, actor.Actor, "req-failed"); !errors.Is(err, githubapp.ErrUnavailable) {
		t.Fatalf("remote failure=%v", err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT status FROM github_installations WHERE github_installation_id=801`).Scan(&status); err != nil || status != "active" {
		t.Fatalf("false local disconnect status=%s err=%v", status, err)
	}
	fake.deleteErr = githubapp.ErrNotFound
	if err = s.DisconnectGitHub(ctx, actor.Actor, "req-not-found"); err != nil {
		t.Fatalf("already uninstalled=%v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM github_installations WHERE github_installation_id=801`).Scan(&status); err != nil || status != "removed" {
		t.Fatalf("removed status=%s err=%v", status, err)
	}
}

func TestGitHubWebhookLifecycleAndIdempotency(t *testing.T) {
	s, pool := integrationService(t)
	ctx := context.Background()
	actor, err := s.Register(ctx, registration("webhook@example.com", "Webhook Org"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeGitHub{installation: githubapp.Installation{ID: 501, AccountID: 502, AccountLogin: "webhook-org", AccountType: "Organization", RepositorySelection: "selected"}}
	s.SetGitHubClient(fake)
	state, _ := s.BeginGitHubInstallation(ctx, actor.Actor)
	if _, err = s.CompleteGitHubInstallation(ctx, actor.Actor, state, "code", 501, "req-connect"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProcessGitHubWebhook(ctx, "delivery-retry", "installation", []byte(`{`)); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("malformed error=%v", err)
	}
	if duplicate, err := s.ProcessGitHubWebhook(ctx, "delivery-retry", "installation", []byte(`{"action":"created","installation":{"id":501}}`)); err != nil || duplicate {
		t.Fatalf("failed delivery retry duplicate=%v err=%v", duplicate, err)
	}
	payload := []byte(`{"action":"added","installation":{"id":501},"repositories_added":[{"id":601,"name":"repo","full_name":"webhook-org/repo","private":true,"default_branch":"main","html_url":"https://github.com/webhook-org/repo","owner":{"login":"webhook-org"}}]}`)
	duplicate, err := s.ProcessGitHubWebhook(ctx, "delivery-1", "installation_repositories", payload)
	if err != nil || duplicate {
		t.Fatalf("first duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = s.ProcessGitHubWebhook(ctx, "delivery-1", "installation_repositories", payload)
	if err != nil || !duplicate {
		t.Fatalf("replay duplicate=%v err=%v", duplicate, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM repositories WHERE github_repository_id=601`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	removed := []byte(`{"action":"removed","installation":{"id":501},"repositories_removed":[{"id":601,"full_name":"webhook-org/repo"}]}`)
	if _, err = s.ProcessGitHubWebhook(ctx, "delivery-remove", "installation_repositories", removed); err != nil {
		t.Fatal(err)
	}
	var available bool
	if err = pool.QueryRow(ctx, `SELECT available FROM repositories WHERE github_repository_id=601`).Scan(&available); err != nil || available {
		t.Fatalf("removed available=%v err=%v", available, err)
	}
	suspend := []byte(`{"action":"suspended","installation":{"id":501}}`)
	if _, err = s.ProcessGitHubWebhook(ctx, "delivery-2", "installation", suspend); err != nil {
		t.Fatal(err)
	}
	var status string
	var suspended *time.Time
	if err = pool.QueryRow(ctx, `SELECT status,suspended_at FROM github_installations WHERE github_installation_id=501`).Scan(&status, &suspended); err != nil || status != "suspended" || suspended == nil {
		t.Fatalf("status=%s suspended=%v err=%v", status, suspended, err)
	}
	unsuspend := []byte(`{"action":"unsuspended","installation":{"id":501}}`)
	if _, err = s.ProcessGitHubWebhook(ctx, "delivery-3", "installation", unsuspend); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM github_installations WHERE github_installation_id=501`).Scan(&status); err != nil || status != "active" {
		t.Fatalf("unsuspended status=%s err=%v", status, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE repositories SET available=true WHERE github_repository_id=601`); err != nil {
		t.Fatal(err)
	}
	deleted := []byte(`{"action":"deleted","installation":{"id":501}}`)
	if _, err = s.ProcessGitHubWebhook(ctx, "delivery-4", "installation", deleted); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM github_installations WHERE github_installation_id=501`).Scan(&status); err != nil || status != "removed" {
		t.Fatalf("removed status=%s err=%v", status, err)
	}
	if err = pool.QueryRow(ctx, `SELECT available FROM repositories WHERE github_repository_id=601`).Scan(&available); err != nil || available {
		t.Fatalf("deleted repository available=%v err=%v", available, err)
	}
	var systemActor *string
	if err = pool.QueryRow(ctx, `SELECT actor_user_id::text FROM audit_events WHERE event_type='github.installation_removed'`).Scan(&systemActor); err != nil || systemActor != nil {
		t.Fatalf("system actor=%v err=%v", systemActor, err)
	}
}
