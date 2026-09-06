package platform

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	runnerclient "github.com/devpilot/devpilot/services/api/internal/runner"
)

type fakeRunner struct {
	mu        sync.Mutex
	requests  []runnerclient.Request
	block     chan struct{}
	cancelled chan string
}

func (f *fakeRunner) Inspect(ctx context.Context, r runnerclient.Request) (runnerclient.Result, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return runnerclient.Result{Status: "cancelled"}, ctx.Err()
		}
	}
	return runnerclient.Result{WorkspaceID: r.WorkspaceID, Status: "succeeded", Artifact: &runnerclient.Artifact{Repository: r.Repository, CommitSHA: r.CommitSHA, RegularFiles: 2, SourceTrust: "untrusted_repository_data"}}, nil
}
func (f *fakeRunner) Cancel(_ context.Context, id string) error {
	if f.cancelled != nil {
		f.cancelled <- id
	}
	return nil
}

func connectedRepository(t *testing.T, s *Service, actor Actor, fake *fakeGitHub) Repository {
	t.Helper()
	state, err := s.BeginGitHubInstallation(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteGitHubInstallation(context.Background(), actor, state, "code", fake.installation.ID, "connect"); err != nil {
		t.Fatal(err)
	}
	repos, err := s.Repositories(context.Background(), actor)
	if err != nil || len(repos) != 1 {
		t.Fatalf("repos=%+v err=%v", repos, err)
	}
	return repos[0]
}
func waitWorkspace(t *testing.T, s *Service, actor Actor, id string) Workspace {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w, err := s.Workspace(context.Background(), actor, id)
		if err == nil && (w.Status == "succeeded" || w.Status == "failed" || w.Status == "cancelled" || w.Status == "timed_out") {
			return w
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workspace did not finish")
	return Workspace{}
}

func TestWorkspaceImmutableTenantSafeAndCapabilitiesNotPersisted(t *testing.T) {
	s, pool := integrationService(t)
	ctx := context.Background()
	owner, _ := s.Register(ctx, registration("workspace-owner@example.com", "Workspace Org"))
	other, _ := s.Register(ctx, registration("workspace-other@example.com", "Other Org"))
	capability := "https://archives.example/temporary-sensitive-capability"
	gh := &fakeGitHub{installation: githubapp.Installation{ID: 991, AccountID: 77, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected"}, repositories: []githubapp.Repository{{ID: 881, Owner: "octo", Name: "private", FullName: "octo/private", DefaultBranch: "main", Private: true}}, resolvedSHA: strings.Repeat("a", 40), archiveURL: capability}
	run := &fakeRunner{}
	s.SetGitHubClient(gh)
	s.SetRunnerClient(run)
	repo := connectedRepository(t, s, owner.Actor, gh)
	w, err := s.CreateWorkspace(ctx, owner.Actor, repo.ID, "main", "create")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitWorkspace(t, s, owner.Actor, w.ID)
	if finished.Status != "succeeded" || finished.ResolvedCommitSHA != strings.Repeat("a", 40) {
		t.Fatalf("workspace=%+v", finished)
	}
	if _, err = s.Workspace(ctx, other.Actor, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant read=%v", err)
	}
	if _, err = s.CreateWorkspace(ctx, other.Actor, repo.ID, "main", "attack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant create=%v", err)
	}
	if err = s.CancelWorkspace(ctx, other.Actor, w.ID, "attack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant cancel=%v", err)
	}
	gh.resolvedSHA = strings.Repeat("b", 40)
	second, err := s.CreateWorkspace(ctx, owner.Actor, repo.ID, "main", "create-2")
	if err != nil {
		t.Fatal(err)
	}
	_ = waitWorkspace(t, s, owner.Actor, second.ID)
	original, _ := s.Workspace(ctx, owner.Actor, w.ID)
	if original.ResolvedCommitSHA != strings.Repeat("a", 40) || second.ID == w.ID {
		t.Fatal("immutable identity changed")
	}
	var leaked bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE inspection_artifact::text LIKE '%'||$1||'%' UNION ALL SELECT 1 FROM audit_events WHERE metadata::text LIKE '%'||$1||'%')`, "temporary-sensitive-capability").Scan(&leaked); err != nil || leaked {
		t.Fatalf("capability persisted=%v err=%v", leaked, err)
	}
}

func TestWorkspaceCancellationIsDurableAndIdempotent(t *testing.T) {
	s, _ := integrationService(t)
	owner, _ := s.Register(context.Background(), registration("cancel-owner@example.com", "Cancel Org"))
	gh := &fakeGitHub{installation: githubapp.Installation{ID: 992, AccountID: 78, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected"}, repositories: []githubapp.Repository{{ID: 882, Owner: "octo", Name: "repo", FullName: "octo/repo", DefaultBranch: "main"}}}
	run := &fakeRunner{block: make(chan struct{}), cancelled: make(chan string, 1)}
	s.SetGitHubClient(gh)
	s.SetRunnerClient(run)
	repo := connectedRepository(t, s, owner.Actor, gh)
	w, err := s.CreateWorkspace(context.Background(), owner.Actor, repo.ID, "main", "create")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if err = s.CancelWorkspace(context.Background(), owner.Actor, w.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-run.cancelled:
	case <-time.After(time.Second):
		t.Fatal("runner not cancelled")
	}
	if err = s.CancelWorkspace(context.Background(), owner.Actor, w.ID, "cancel-again"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second cancel=%v", err)
	}
	value, _ := s.Workspace(context.Background(), owner.Actor, w.ID)
	if value.Status != "cancelled" {
		t.Fatalf("status=%s", value.Status)
	}
}
