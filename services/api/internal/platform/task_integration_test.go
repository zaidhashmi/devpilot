package platform

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/config"
	"github.com/devpilot/devpilot/services/api/internal/githubapp"
)

func orchestrationFixture(t *testing.T) (*Service, Actor, Repository, *fakeRunner) {
	t.Helper()
	s, _ := integrationService(t)
	session, err := s.Register(context.Background(), registration("tasks@example.com", "Task Org"))
	if err != nil {
		t.Fatal(err)
	}
	gh := &fakeGitHub{installation: githubapp.Installation{ID: 4001, AccountID: 99, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected"}, repositories: []githubapp.Repository{{ID: 5001, Owner: "octo", Name: "private", FullName: "octo/private", DefaultBranch: "main", Private: true}}, resolvedSHA: strings.Repeat("c", 40), archiveURL: "https://codeload.github.com/ephemeral-not-persisted"}
	runner := &fakeRunner{}
	s.SetGitHubClient(gh)
	s.SetRunnerClient(runner)
	repo := connectedRepository(t, s, session.Actor, gh)
	return s, session.Actor, repo, runner
}

func TestTaskRunOutboxRecoveryAndApproval(t *testing.T) {
	s, actor, repo, _ := orchestrationFixture(t)
	ctx := context.Background()
	task, err := s.CreateTask(ctx, actor, repo.ID, "Improve safety", "Review repository structure before analysis", "req")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateTaskRun(ctx, actor, task.ID, "main", "req")
	if err != nil {
		t.Fatal(err)
	}
	// No worker has run: state and dispatch intent survive the HTTP/API lifetime.
	value, err := s.TaskRun(ctx, actor, run.ID)
	if err != nil || value.Status != "pending" {
		t.Fatalf("run=%+v err=%v", value, err)
	}
	var count int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND completed_at IS NULL`, run.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outbox=%d err=%v", count, err)
	}
	event, ok, err := s.ClaimOutbox(ctx, "worker-restarted", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	cfg := config.WorkerConfig{MaxAttempts: 5, BaseRetry: time.Millisecond, MaxRetry: time.Second}
	if err = s.ProcessOutbox(ctx, event, cfg); err != nil {
		t.Fatal(err)
	}
	value, err = s.TaskRun(ctx, actor, run.ID)
	if err != nil || value.Status != "awaiting_approval" || value.Plan == nil || value.Approval == nil || value.Workspace == nil {
		t.Fatalf("run=%+v err=%v", value, err)
	}
	if value.Plan.Source != "deterministic" || value.Plan.Payload["recommended_next_stage"] != "repository_analysis_agent" {
		t.Fatalf("plan=%+v", value.Plan)
	}
	// Redelivery after side effects is idempotent: no duplicate workspace, plan, or approval.
	if err = s.processTaskRun(ctx, run.ID, actor.Organization.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"workspaces", "task_plan_revisions", "approvals"} {
		query := `SELECT count(*) FROM ` + table
		if table == "workspaces" {
			query += ` WHERE id=(SELECT workspace_id FROM task_runs WHERE id=$1)`
		} else {
			query += ` WHERE task_run_id=$1`
		}
		if err = s.db.QueryRow(ctx, query, run.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	approved, err := s.DecideApproval(ctx, actor, value.Approval.ID, "approved", "Proceed to future read-only analysis", "approve")
	if err != nil || approved.Status != "approved" {
		t.Fatalf("approval=%+v err=%v", approved, err)
	}
	if _, err = s.DecideApproval(ctx, actor, value.Approval.ID, "rejected", "", "second"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second decision=%v", err)
	}
	var leaked bool
	if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_events WHERE payload::text ILIKE '%codeload%' OR payload::text ILIKE '%token%' UNION ALL SELECT 1 FROM approvals WHERE decision_comment ILIKE '%codeload%')`).Scan(&leaked); err != nil || leaked {
		t.Fatalf("secret capability persisted=%v err=%v", leaked, err)
	}
}

func TestOutboxClaimConcurrencyAndStaleRecovery(t *testing.T) {
	s, actor, repo, _ := orchestrationFixture(t)
	task, _ := s.CreateTask(context.Background(), actor, repo.ID, "Concurrent", "Objective", "req")
	run, _ := s.CreateTaskRun(context.Background(), actor, task.ID, "main", "req")
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, _ := s.ClaimOutbox(context.Background(), "worker", time.Minute)
			results <- ok
		}()
	}
	wg.Wait()
	close(results)
	claims := 0
	for ok := range results {
		if ok {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("claims=%d", claims)
	}
	_, err := s.db.Exec(context.Background(), `UPDATE outbox_events SET claimed_at=now()-interval '10 minutes' WHERE aggregate_id=$1`, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := s.ClaimOutbox(context.Background(), "recovery", time.Second)
	if err != nil || !ok {
		t.Fatalf("stale recovery ok=%v err=%v", ok, err)
	}
}

func TestTaskTenantIsolationCancellationAndApprovalRace(t *testing.T) {
	s, actor, repo, runner := orchestrationFixture(t)
	other, err := s.Register(context.Background(), registration("other-task@example.com", "Other Task Org"))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.CreateTask(context.Background(), actor, repo.ID, "Tenant safe", "Objective", "req")
	run, _ := s.CreateTaskRun(context.Background(), actor, task.ID, "main", "req")
	if _, _, err = s.Task(context.Background(), other.Actor, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross task=%v", err)
	}
	if _, err = s.TaskRun(context.Background(), other.Actor, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross run=%v", err)
	}
	if err = s.CancelTaskRun(context.Background(), other.Actor, run.ID, "attack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross cancel=%v", err)
	}
	event, _, _ := s.ClaimOutbox(context.Background(), "worker", time.Minute)
	_ = s.ProcessOutbox(context.Background(), event, config.WorkerConfig{MaxAttempts: 5, BaseRetry: time.Millisecond, MaxRetry: time.Second})
	current, _ := s.TaskRun(context.Background(), actor, run.ID)
	if _, err = s.DecideApproval(context.Background(), other.Actor, current.Approval.ID, "approved", "", "attack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross approval=%v", err)
	}
	var decisions sync.WaitGroup
	wins := make(chan error, 2)
	for _, decision := range []string{"approved", "rejected"} {
		decisions.Add(1)
		go func(d string) {
			defer decisions.Done()
			_, e := s.DecideApproval(context.Background(), actor, current.Approval.ID, d, "", "race")
			wins <- e
		}(decision)
	}
	decisions.Wait()
	close(wins)
	successes := 0
	for e := range wins {
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatalf("race error=%v", e)
		}
	}
	if successes != 1 {
		t.Fatalf("decision successes=%d", successes)
	}
	_ = runner
}

func TestQueuedCancellationPreventsDispatch(t *testing.T) {
	s, actor, repo, _ := orchestrationFixture(t)
	task, _ := s.CreateTask(context.Background(), actor, repo.ID, "Cancel", "Objective", "req")
	run, _ := s.CreateTaskRun(context.Background(), actor, task.ID, "main", "req")
	if err := s.CancelTaskRun(context.Background(), actor, run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimOutbox(context.Background(), "worker", time.Minute); err != nil || ok {
		t.Fatalf("cancelled dispatch claimed=%v err=%v", ok, err)
	}
	current, _ := s.TaskRun(context.Background(), actor, run.ID)
	if current.Status != "cancelled" || current.WorkspaceID != nil {
		t.Fatalf("run=%+v", current)
	}
}

func TestRetryBackoffAndMaximumAttempts(t *testing.T) {
	s, actor, repo, runner := orchestrationFixture(t)
	runner.returnErr = errors.New("temporary runner transport")
	task, _ := s.CreateTask(context.Background(), actor, repo.ID, "Retry", "Objective", "req")
	run, _ := s.CreateTaskRun(context.Background(), actor, task.ID, "main", "req")
	event, ok, err := s.ClaimOutbox(context.Background(), "worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim=%v err=%v", ok, err)
	}
	cfg := config.WorkerConfig{MaxAttempts: 2, BaseRetry: time.Millisecond, MaxRetry: time.Millisecond}
	if err = s.ProcessOutbox(context.Background(), event, cfg); err != nil {
		t.Fatal(err)
	}
	var completed *time.Time
	var attempts int
	var available time.Time
	if err = s.db.QueryRow(context.Background(), `SELECT completed_at,attempt_count,available_at FROM outbox_events WHERE id=$1`, event.ID).Scan(&completed, &attempts, &available); err != nil {
		t.Fatal(err)
	}
	if completed != nil || attempts != 1 || !available.After(time.Now().Add(-time.Second)) {
		t.Fatalf("completed=%v attempts=%d available=%v", completed, attempts, available)
	}
	time.Sleep(3 * time.Millisecond)
	event, ok, err = s.ClaimOutbox(context.Background(), "worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("retry claim=%v err=%v", ok, err)
	}
	if err = s.ProcessOutbox(context.Background(), event, cfg); err != nil {
		t.Fatal(err)
	}
	current, _ := s.TaskRun(context.Background(), actor, run.ID)
	if current.Status != "failed" || current.FailureCode == nil {
		t.Fatalf("run=%+v", current)
	}
	if err = s.db.QueryRow(context.Background(), `SELECT completed_at FROM outbox_events WHERE id=$1`, event.ID).Scan(&completed); err != nil || completed == nil {
		t.Fatalf("outbox completion=%v err=%v", completed, err)
	}
}
