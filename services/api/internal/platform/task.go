package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/authz"
	"github.com/devpilot/devpilot/services/api/internal/config"
	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/devpilot/devpilot/services/api/internal/runner"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const taskStartEvent = "task_run.start_inspection.v1"

var validRunTransitions = map[string]map[string]bool{
	"pending":           {"inspecting": true, "cancelled": true, "failed": true, "timed_out": true},
	"inspecting":        {"awaiting_approval": true, "cancelled": true, "failed": true, "timed_out": true},
	"awaiting_approval": {"approved": true, "rejected": true, "cancelled": true},
	"approved":          {"cancelled": true},
}

func ValidTaskRunTransition(from, to string) bool { return validRunTransitions[from][to] }

func IsTerminalTaskRunStatus(status string) bool {
	return status == "rejected" || status == "cancelled" || status == "failed" || status == "timed_out" || status == "completed"
}

func (s *Service) CreateTask(ctx context.Context, actor Actor, repositoryID, title, objective, requestID string) (EngineeringTask, error) {
	if !authz.Allowed(actor.Membership.Role, authz.TasksCreate) {
		return EngineeringTask{}, ErrForbidden
	}
	title, objective = strings.TrimSpace(title), strings.TrimSpace(objective)
	if len(title) < 1 || len(title) > 180 || len(objective) < 1 || len(objective) > 8000 {
		return EngineeringTask{}, ErrInvalidInput
	}
	var repoName string
	err := s.db.QueryRow(ctx, `SELECT r.full_name FROM repositories r JOIN github_installations i ON i.id=r.github_installation_id WHERE r.id=$1 AND r.organization_id=$2 AND r.available AND NOT r.disabled AND i.status='active'`, repositoryID, actor.Organization.ID).Scan(&repoName)
	if errors.Is(err, pgx.ErrNoRows) {
		return EngineeringTask{}, ErrNotFound
	}
	if err != nil {
		return EngineeringTask{}, err
	}
	id, now := newID(), s.now().UTC()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return EngineeringTask{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, `INSERT INTO engineering_tasks(id,organization_id,repository_id,created_by_user_id,title,objective,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'active',$7,$7)`, id, actor.Organization.ID, repositoryID, actor.User.ID, title, objective, now)
	if err == nil {
		err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "engineering_task.created", "engineering_task", id, requestID, mustJSON(map[string]any{"repository_id": repositoryID}), now)
	}
	if err != nil {
		return EngineeringTask{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return EngineeringTask{}, err
	}
	return EngineeringTask{ID: id.String(), RepositoryID: repositoryID, RepositoryFullName: repoName, CreatedByUserID: actor.User.ID, Title: title, Objective: objective, Status: "active", CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Service) Tasks(ctx context.Context, actor Actor) ([]EngineeringTask, error) {
	if !authz.Allowed(actor.Membership.Role, authz.TasksRead) {
		return nil, ErrForbidden
	}
	rows, err := s.db.Query(ctx, `SELECT t.id,t.repository_id,r.full_name,t.created_by_user_id,t.title,t.objective,t.status,t.created_at,t.updated_at,t.closed_at FROM engineering_tasks t JOIN repositories r ON r.id=t.repository_id WHERE t.organization_id=$1 ORDER BY t.created_at DESC`, actor.Organization.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EngineeringTask{}
	for rows.Next() {
		var x EngineeringTask
		if err = rows.Scan(&x.ID, &x.RepositoryID, &x.RepositoryFullName, &x.CreatedByUserID, &x.Title, &x.Objective, &x.Status, &x.CreatedAt, &x.UpdatedAt, &x.ClosedAt); err != nil {
			return nil, err
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

func (s *Service) Task(ctx context.Context, actor Actor, id string) (EngineeringTask, []TaskRun, error) {
	if !authz.Allowed(actor.Membership.Role, authz.TasksRead) {
		return EngineeringTask{}, nil, ErrForbidden
	}
	var t EngineeringTask
	err := s.db.QueryRow(ctx, `SELECT t.id,t.repository_id,r.full_name,t.created_by_user_id,t.title,t.objective,t.status,t.created_at,t.updated_at,t.closed_at FROM engineering_tasks t JOIN repositories r ON r.id=t.repository_id WHERE t.id=$1 AND t.organization_id=$2`, id, actor.Organization.ID).Scan(&t.ID, &t.RepositoryID, &t.RepositoryFullName, &t.CreatedByUserID, &t.Title, &t.Objective, &t.Status, &t.CreatedAt, &t.UpdatedAt, &t.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EngineeringTask{}, nil, ErrNotFound
	}
	if err != nil {
		return EngineeringTask{}, nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT id,engineering_task_id,repository_id,workspace_id,run_number,requested_ref,resolved_commit_sha,status,current_stage,created_by_user_id,failure_code,cancellation_requested_at,created_at,started_at,completed_at FROM task_runs WHERE engineering_task_id=$1 ORDER BY run_number DESC`, id)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	runs := []TaskRun{}
	for rows.Next() {
		var x TaskRun
		x.RepositoryFullName = t.RepositoryFullName
		if err = rows.Scan(&x.ID, &x.EngineeringTaskID, &x.RepositoryID, &x.WorkspaceID, &x.RunNumber, &x.RequestedRef, &x.ResolvedCommitSHA, &x.Status, &x.CurrentStage, &x.CreatedByUserID, &x.FailureCode, &x.CancellationRequestedAt, &x.CreatedAt, &x.StartedAt, &x.CompletedAt); err != nil {
			return t, nil, err
		}
		runs = append(runs, x)
	}
	return t, runs, rows.Err()
}

func (s *Service) CreateTaskRun(ctx context.Context, actor Actor, taskID, ref, requestID string) (TaskRun, error) {
	if !authz.Allowed(actor.Membership.Role, authz.TaskRunsCreate) {
		return TaskRun{}, ErrForbidden
	}
	if !validWorkspaceRef.MatchString(ref) {
		return TaskRun{}, ErrInvalidInput
	}
	content, ok := s.github.(githubapp.ContentClient)
	if !ok {
		return TaskRun{}, ErrUnavailable
	}
	var repositoryID, repoName, owner, name, status string
	var githubRepoID, installationID int64
	err := s.db.QueryRow(ctx, `SELECT t.repository_id,r.full_name,r.owner,r.name,t.status,r.github_repository_id,i.github_installation_id FROM engineering_tasks t JOIN repositories r ON r.id=t.repository_id JOIN github_installations i ON i.id=r.github_installation_id WHERE t.id=$1 AND t.organization_id=$2 AND r.available AND NOT r.disabled AND i.status='active'`, taskID, actor.Organization.ID).Scan(&repositoryID, &repoName, &owner, &name, &status, &githubRepoID, &installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskRun{}, ErrNotFound
	}
	if err != nil {
		return TaskRun{}, err
	}
	if status != "active" {
		return TaskRun{}, ErrConflict
	}
	sha, err := content.ResolveCommit(ctx, installationID, githubRepoID, owner, name, ref)
	if err != nil {
		return TaskRun{}, err
	}
	id, now := newID(), s.now().UTC()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TaskRun{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Lock the aggregate before allocating a run number, including its first run.
	_, err = tx.Exec(ctx, `SELECT 1 FROM engineering_tasks WHERE id=$1 AND organization_id=$2 AND status='active' FOR UPDATE`, taskID, actor.Organization.ID)
	var runNumber int
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT COALESCE(max(run_number),0)+1 FROM task_runs WHERE engineering_task_id=$1`, taskID).Scan(&runNumber)
	}
	if err != nil {
		return TaskRun{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_runs(id,engineering_task_id,organization_id,repository_id,run_number,requested_ref,resolved_commit_sha,status,current_stage,created_by_user_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'pending','repository_snapshot',$8,$9)`, id, taskID, actor.Organization.ID, repositoryID, runNumber, ref, sha, actor.User.ID, now)
	if err != nil {
		return TaskRun{}, err
	}
	if err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "task_run.created", "task_run", id, requestID, mustJSON(map[string]any{"commit_sha": sha}), now); err != nil {
		return TaskRun{}, err
	}
	payload := mustJSON(map[string]any{"version": 1, "task_run_id": id.String()})
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,organization_id,aggregate_type,aggregate_id,event_type,payload,created_at,available_at) VALUES($1,$2,'task_run',$3,$4,$5,$6,$6)`, newID(), actor.Organization.ID, id, taskStartEvent, payload, now)
	if err != nil {
		return TaskRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return TaskRun{}, err
	}
	return TaskRun{ID: id.String(), EngineeringTaskID: taskID, RepositoryID: repositoryID, RepositoryFullName: repoName, RunNumber: runNumber, RequestedRef: ref, ResolvedCommitSHA: sha, Status: "pending", CurrentStage: "repository_snapshot", CreatedByUserID: actor.User.ID, CreatedAt: now}, nil
}

func (s *Service) TaskRun(ctx context.Context, actor Actor, id string) (TaskRun, error) {
	if !authz.Allowed(actor.Membership.Role, authz.TaskRunsRead) {
		return TaskRun{}, ErrForbidden
	}
	var x TaskRun
	err := s.db.QueryRow(ctx, `SELECT tr.id,tr.engineering_task_id,tr.repository_id,r.full_name,tr.workspace_id,tr.run_number,tr.requested_ref,tr.resolved_commit_sha,tr.status,tr.current_stage,tr.created_by_user_id,tr.failure_code,tr.cancellation_requested_at,tr.created_at,tr.started_at,tr.completed_at FROM task_runs tr JOIN repositories r ON r.id=tr.repository_id WHERE tr.id=$1 AND tr.organization_id=$2`, id, actor.Organization.ID).Scan(&x.ID, &x.EngineeringTaskID, &x.RepositoryID, &x.RepositoryFullName, &x.WorkspaceID, &x.RunNumber, &x.RequestedRef, &x.ResolvedCommitSHA, &x.Status, &x.CurrentStage, &x.CreatedByUserID, &x.FailureCode, &x.CancellationRequestedAt, &x.CreatedAt, &x.StartedAt, &x.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskRun{}, ErrNotFound
	}
	if err != nil {
		return TaskRun{}, err
	}
	if x.WorkspaceID != nil {
		w, e := s.Workspace(ctx, actor, *x.WorkspaceID)
		if e == nil {
			x.Workspace = &w
		}
	}
	var p PlanRevision
	var raw []byte
	e := s.db.QueryRow(ctx, `SELECT id,task_run_id,revision_number,source,summary,structured_payload,created_at FROM task_plan_revisions WHERE task_run_id=$1 ORDER BY revision_number DESC LIMIT 1`, id).Scan(&p.ID, &p.TaskRunID, &p.RevisionNumber, &p.Source, &p.Summary, &raw, &p.CreatedAt)
	if e == nil {
		_ = json.Unmarshal(raw, &p.Payload)
		x.Plan = &p
	}
	var a Approval
	e = s.db.QueryRow(ctx, `SELECT id,task_run_id,plan_revision_id,approval_type,revision,status,requested_at,decided_at,decided_by_user_id,decision_comment FROM approvals WHERE task_run_id=$1 ORDER BY revision DESC LIMIT 1`, id).Scan(&a.ID, &a.TaskRunID, &a.PlanRevisionID, &a.ApprovalType, &a.Revision, &a.Status, &a.RequestedAt, &a.DecidedAt, &a.DecidedByUserID, &a.DecisionComment)
	if e == nil {
		x.Approval = &a
	}
	return x, nil
}

func (s *Service) CancelTaskRun(ctx context.Context, actor Actor, id, requestID string) error {
	if !authz.Allowed(actor.Membership.Role, authz.TaskRunsCancel) {
		return ErrForbidden
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var workspaceID *string
	var status, creator string
	err = tx.QueryRow(ctx, `SELECT workspace_id,status,created_by_user_id FROM task_runs WHERE id=$1 AND organization_id=$2 FOR UPDATE`, id, actor.Organization.ID).Scan(&workspaceID, &status, &creator)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if actor.Membership.Role == authz.RoleMember && creator != actor.User.ID {
		return ErrForbidden
	}
	if !ValidTaskRunTransition(status, "cancelled") {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE task_runs SET status='cancelled',current_stage='finished',cancellation_requested_at=COALESCE(cancellation_requested_at,now()),completed_at=now() WHERE id=$1`, id)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE approvals SET status='cancelled',decided_at=now() WHERE task_run_id=$1 AND status='pending'`, id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox_events SET completed_at=now(),last_error_code='cancelled' WHERE aggregate_id=$1 AND completed_at IS NULL`, id)
	}
	if err == nil {
		err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "task_run.cancel_requested", "task_run", uuid.MustParse(id), requestID, nil, s.now().UTC())
	}
	if err == nil {
		err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "task_run.cancelled", "task_run", uuid.MustParse(id), requestID, nil, s.now().UTC())
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if workspaceID != nil {
		_ = s.CancelWorkspace(ctx, actor, *workspaceID, requestID)
	}
	return nil
}

func (s *Service) CancelTask(ctx context.Context, actor Actor, id, requestID string) error {
	if !authz.Allowed(actor.Membership.Role, authz.TasksCancel) {
		return ErrForbidden
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var runID *string
	err = tx.QueryRow(ctx, `UPDATE engineering_tasks SET status='cancelled',closed_at=now(),updated_at=now() WHERE id=$1 AND organization_id=$2 AND status='active' RETURNING (SELECT id FROM task_runs WHERE engineering_task_id=$1 AND status IN ('pending','inspecting','awaiting_approval','approved') ORDER BY run_number DESC LIMIT 1)`, id, actor.Organization.ID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "engineering_task.cancelled", "engineering_task", uuid.MustParse(id), requestID, nil, s.now().UTC()); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if runID != nil {
		return s.CancelTaskRun(ctx, actor, *runID, requestID)
	}
	return nil
}

func (s *Service) DecideApproval(ctx context.Context, actor Actor, id, decision, comment, requestID string) (Approval, error) {
	if !authz.Allowed(actor.Membership.Role, authz.ApprovalsDecide) || (decision != "approved" && decision != "rejected") {
		return Approval{}, ErrForbidden
	}
	comment = strings.TrimSpace(comment)
	if len(comment) > 1000 {
		return Approval{}, ErrInvalidInput
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Approval{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var a Approval
	var creator, status string
	err = tx.QueryRow(ctx, `SELECT a.id,a.task_run_id,a.plan_revision_id,a.approval_type,a.revision,a.status,a.requested_at,tr.created_by_user_id,tr.status FROM approvals a JOIN task_runs tr ON tr.id=a.task_run_id WHERE a.id=$1 AND a.organization_id=$2 FOR UPDATE`, id, actor.Organization.ID).Scan(&a.ID, &a.TaskRunID, &a.PlanRevisionID, &a.ApprovalType, &a.Revision, &a.Status, &a.RequestedAt, &creator, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, ErrNotFound
	}
	if err != nil {
		return Approval{}, err
	}
	if actor.Membership.Role == authz.RoleMember && creator != actor.User.ID {
		return Approval{}, ErrForbidden
	}
	if a.Status != "pending" || status != "awaiting_approval" {
		return Approval{}, ErrConflict
	}
	now := s.now().UTC()
	var commentValue any
	if comment != "" {
		commentValue = comment
	}
	_, err = tx.Exec(ctx, `UPDATE approvals SET status=$2,decided_at=$3,decided_by_user_id=$4,decision_comment=$5 WHERE id=$1 AND status='pending'`, id, decision, now, actor.User.ID, commentValue)
	if err != nil {
		return Approval{}, err
	}
	stage := "finished"
	var completedAt any = now
	if decision == "approved" {
		stage = "approval"
		completedAt = nil
	}
	_, err = tx.Exec(ctx, `UPDATE task_runs SET status=$2,current_stage=$3,completed_at=$4 WHERE id=$1 AND status='awaiting_approval'`, a.TaskRunID, decision, stage, completedAt)
	if err != nil {
		return Approval{}, err
	}
	err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "approval."+decision, "approval", uuid.MustParse(id), requestID, mustJSON(map[string]any{"plan_revision_id": a.PlanRevisionID}), now)
	if err != nil {
		return Approval{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Approval{}, err
	}
	a.Status = decision
	a.DecidedAt = &now
	a.DecidedByUserID = &actor.User.ID
	if comment != "" {
		a.DecisionComment = &comment
	}
	return a, nil
}

func (s *Service) Approvals(ctx context.Context, actor Actor, runID string) ([]Approval, error) {
	if !authz.Allowed(actor.Membership.Role, authz.ApprovalsRead) {
		return nil, ErrForbidden
	}
	rows, err := s.db.Query(ctx, `SELECT a.id,a.task_run_id,a.plan_revision_id,a.approval_type,a.revision,a.status,a.requested_at,a.decided_at,a.decided_by_user_id,a.decision_comment FROM approvals a JOIN task_runs tr ON tr.id=a.task_run_id WHERE a.task_run_id=$1 AND tr.organization_id=$2 ORDER BY a.revision DESC`, runID, actor.Organization.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Approval{}
	for rows.Next() {
		var a Approval
		if err = rows.Scan(&a.ID, &a.TaskRunID, &a.PlanRevisionID, &a.ApprovalType, &a.Revision, &a.Status, &a.RequestedAt, &a.DecidedAt, &a.DecidedByUserID, &a.DecisionComment); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	if len(result) == 0 {
		var exists bool
		if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_runs WHERE id=$1 AND organization_id=$2)`, runID, actor.Organization.ID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return result, rows.Err()
}

type ClaimedEvent struct {
	ID, OrganizationID, AggregateID, EventType string
	Payload                                    []byte
	Attempt                                    int
}

func (s *Service) ClaimOutbox(ctx context.Context, workerID string, stale time.Duration) (ClaimedEvent, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ClaimedEvent{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var e ClaimedEvent
	err = tx.QueryRow(ctx, `SELECT id,organization_id,aggregate_id,event_type,payload,attempt_count FROM outbox_events WHERE completed_at IS NULL AND available_at<=now() AND (claimed_at IS NULL OR claimed_at < now()-$1::interval) ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1`, fmt.Sprintf("%f seconds", stale.Seconds())).Scan(&e.ID, &e.OrganizationID, &e.AggregateID, &e.EventType, &e.Payload, &e.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimedEvent{}, false, nil
	}
	if err != nil {
		return ClaimedEvent{}, false, err
	}
	e.Attempt++
	_, err = tx.Exec(ctx, `UPDATE outbox_events SET claimed_at=now(),claimed_by=$2,attempt_count=attempt_count+1 WHERE id=$1`, e.ID, workerID)
	if err != nil {
		return ClaimedEvent{}, false, err
	}
	return e, true, tx.Commit(ctx)
}

func (s *Service) ProcessOutbox(ctx context.Context, e ClaimedEvent, cfg config.WorkerConfig) error {
	if e.EventType != taskStartEvent {
		return s.failOutbox(ctx, e, cfg, "unknown_event", false)
	}
	var payload struct {
		Version   int    `json:"version"`
		TaskRunID string `json:"task_run_id"`
	}
	if json.Unmarshal(e.Payload, &payload) != nil || payload.Version != 1 || payload.TaskRunID != e.AggregateID {
		return s.failOutbox(ctx, e, cfg, "malformed_payload", false)
	}
	err := s.processTaskRun(ctx, e.AggregateID, e.OrganizationID)
	if err == nil {
		_, err = s.db.Exec(ctx, `UPDATE outbox_events SET completed_at=now(),claimed_at=NULL,claimed_by=NULL,last_error_code=NULL WHERE id=$1`, e.ID)
		return err
	}
	retryable := errors.Is(err, ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, githubapp.ErrUnavailable) || errors.Is(err, githubapp.ErrRateLimited)
	return s.failOutbox(ctx, e, cfg, stableWorkerError(err), retryable)
}

func (s *Service) failOutbox(ctx context.Context, e ClaimedEvent, cfg config.WorkerConfig, code string, retryable bool) error {
	if !retryable || e.Attempt >= cfg.MaxAttempts {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		_, err = tx.Exec(ctx, `UPDATE outbox_events SET completed_at=now(),claimed_at=NULL,claimed_by=NULL,last_error_code=$2 WHERE id=$1`, e.ID, code)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE task_runs SET status='failed',current_stage='finished',failure_code=$2,completed_at=now() WHERE id=$1 AND status IN ('pending','inspecting')`, e.AggregateID, code)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,'task_run.failed','task_run',$3,'worker',$4)`, newID(), e.OrganizationID, e.AggregateID, mustJSON(map[string]any{"failure_code": code}))
		}
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	delay := float64(cfg.BaseRetry) * math.Pow(2, float64(e.Attempt-1))
	if delay > float64(cfg.MaxRetry) {
		delay = float64(cfg.MaxRetry)
	}
	delay *= 0.8 + rand.Float64()*0.4
	_, err := s.db.Exec(ctx, `UPDATE outbox_events SET available_at=now()+$2::interval,claimed_at=NULL,claimed_by=NULL,last_error_code=$3 WHERE id=$1`, e.ID, fmt.Sprintf("%f seconds", delay/float64(time.Second)), code)
	return err
}

func stableWorkerError(err error) string {
	if errors.Is(err, githubapp.ErrRateLimited) {
		return "github_rate_limited"
	}
	if errors.Is(err, githubapp.ErrUnavailable) {
		return "github_unavailable"
	}
	if errors.Is(err, ErrNotFound) {
		return "repository_unavailable"
	}
	if errors.Is(err, ErrInvalidInput) {
		return "invalid_snapshot"
	}
	return "dispatch_unavailable"
}

func (s *Service) processTaskRun(ctx context.Context, runID, organizationID string) error {
	content, ok := s.github.(githubapp.ContentClient)
	if !ok || s.runner == nil {
		return ErrUnavailable
	}
	var status, workspaceID, repoName, owner, name, sha string
	var githubRepoID, installationID int64
	err := s.db.QueryRow(ctx, `SELECT tr.status,COALESCE(tr.workspace_id::text,''),r.full_name,r.owner,r.name,tr.resolved_commit_sha,r.github_repository_id,i.github_installation_id FROM task_runs tr JOIN repositories r ON r.id=tr.repository_id JOIN github_installations i ON i.id=r.github_installation_id WHERE tr.id=$1 AND tr.organization_id=$2 AND r.available AND NOT r.disabled AND i.status='active'`, runID, organizationID).Scan(&status, &workspaceID, &repoName, &owner, &name, &sha, &githubRepoID, &installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "cancelled" || status == "awaiting_approval" || status == "approved" || status == "rejected" {
		return nil
	}
	if workspaceID == "" {
		archiveURL, err := content.ArchiveURL(ctx, installationID, githubRepoID, owner, name, sha)
		if err != nil {
			return err
		}
		wid := newID()
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		var repositoryID, creator, ref string
		err = tx.QueryRow(ctx, `SELECT repository_id,created_by_user_id,requested_ref FROM task_runs WHERE id=$1 AND status='pending' FOR UPDATE`, runID).Scan(&repositoryID, &creator, &ref)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO workspaces(id,organization_id,repository_id,requested_ref,resolved_commit_sha,status,created_by_user_id) VALUES($1,$2,$3,$4,$5,'pending',$6)`, wid, organizationID, repositoryID, ref, sha, creator)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE task_runs SET workspace_id=$2,status='inspecting',current_stage='repository_inspection',started_at=COALESCE(started_at,now()) WHERE id=$1 AND status='pending'`, runID, wid)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,'task_run.started','task_run',$3,'worker','{}')`, newID(), organizationID, runID)
		}
		if err != nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		workspaceID = wid.String()
		s.runWorkspace(runner.Request{WorkspaceID: workspaceID, Repository: repoName, CommitSHA: sha, ArchiveURL: archiveURL, Limits: workspaceLimits}, organizationID)
	} else {
		var existingStatus string
		if err = s.db.QueryRow(ctx, `SELECT status FROM workspaces WHERE id=$1`, workspaceID).Scan(&existingStatus); err != nil {
			return err
		}
		var retryableFailure bool
		if existingStatus == "failed" {
			var code *string
			_ = s.db.QueryRow(ctx, `SELECT failure_code FROM workspaces WHERE id=$1`, workspaceID).Scan(&code)
			retryableFailure = code != nil && *code == "runner_failed"
		}
		if existingStatus == "pending" || existingStatus == "acquiring" || existingStatus == "inspecting" || retryableFailure {
			archiveURL, archiveErr := content.ArchiveURL(ctx, installationID, githubRepoID, owner, name, sha)
			if archiveErr != nil {
				return archiveErr
			}
			// A stale claim means the previous worker can no longer acknowledge its attempt.
			// Reset the durable workspace and close the abandoned attempt before idempotent redelivery.
			_, _ = s.db.Exec(ctx, `UPDATE workspace_attempts SET status='failed',failure_code='worker_claim_stale',completed_at=now() WHERE workspace_id=$1 AND status IN ('acquiring','inspecting')`, workspaceID)
			_, _ = s.db.Exec(ctx, `UPDATE workspaces SET status='pending',failure_code=NULL,completed_at=NULL,updated_at=now() WHERE id=$1 AND (status IN ('acquiring','inspecting') OR (status='failed' AND failure_code='runner_failed'))`, workspaceID)
			s.runWorkspace(runner.Request{WorkspaceID: workspaceID, Repository: repoName, CommitSHA: sha, ArchiveURL: archiveURL, Limits: workspaceLimits}, organizationID)
		}
	}
	var workspaceStatus string
	var artifact []byte
	var failure *string
	err = s.db.QueryRow(ctx, `SELECT status,inspection_artifact,failure_code FROM workspaces WHERE id=$1`, workspaceID).Scan(&workspaceStatus, &artifact, &failure)
	if err != nil {
		return err
	}
	if workspaceStatus != "succeeded" {
		if workspaceStatus == "failed" || workspaceStatus == "timed_out" || workspaceStatus == "cancelled" {
			code := workspaceStatus
			if failure != nil {
				code = *failure
			}
			if code == "runner_failed" {
				return ErrUnavailable
			}
			tx, txErr := s.db.Begin(ctx)
			if txErr != nil {
				return txErr
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			result, txErr := tx.Exec(ctx, `UPDATE task_runs SET status=$2,current_stage='finished',failure_code=$3,completed_at=now() WHERE id=$1 AND status='inspecting'`, runID, workspaceStatus, code)
			if txErr != nil {
				return txErr
			}
			if result.RowsAffected() == 1 {
				_, txErr = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata,created_at) VALUES($1,$2,NULL,'task_run.failed','task_run',$3,'worker',$4,$5)`, newID(), organizationID, runID, mustJSON(map[string]any{"failure_code": code, "outcome": workspaceStatus}), s.now().UTC())
			}
			if txErr != nil {
				return txErr
			}
			return tx.Commit(ctx)
		}
		return ErrUnavailable
	}
	return s.finalizeInspection(ctx, runID, organizationID, repoName, sha, artifact)
}

func (s *Service) finalizeInspection(ctx context.Context, runID, organizationID, repoName, sha string, artifact []byte) error {
	var inspection map[string]any
	if json.Unmarshal(artifact, &inspection) != nil {
		return ErrInvalidInput
	}
	var objective string
	err := s.db.QueryRow(ctx, `SELECT t.objective FROM task_runs tr JOIN engineering_tasks t ON t.id=tr.engineering_task_id WHERE tr.id=$1`, runID).Scan(&objective)
	if err != nil {
		return err
	}
	payload := map[string]any{"objective": objective, "repository": repoName, "commit_sha": sha, "detected_ecosystems": inspection["ecosystems"], "manifests": inspection["manifests"], "inspection_summary": map[string]any{"regular_file_count": inspection["regular_file_count"], "total_bytes": inspection["total_bytes"], "directory_count": inspection["directory_count"]}, "recommended_next_stage": "repository_analysis_agent", "source_trust": "untrusted_repository_data"}
	raw := mustJSON(payload)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM task_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&status)
	if err != nil {
		return err
	}
	if status == "awaiting_approval" || status == "approved" || status == "rejected" || status == "cancelled" {
		return nil
	}
	if status != "inspecting" {
		return ErrConflict
	}
	planID, approvalID := newID(), newID()
	_, err = tx.Exec(ctx, `INSERT INTO task_plan_revisions(id,task_run_id,revision_number,source,summary,structured_payload) VALUES($1,$2,1,'deterministic','Deterministic repository inspection context; no AI-generated plan exists.',$3) ON CONFLICT(task_run_id,revision_number) DO NOTHING`, planID, runID, raw)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM task_plan_revisions WHERE task_run_id=$1 AND revision_number=1`, runID).Scan(&planID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO approvals(id,organization_id,task_run_id,plan_revision_id,approval_type,revision,status) VALUES($1,$2,$3,$4,'proceed_to_analysis',1,'pending') ON CONFLICT(task_run_id,approval_type,revision) DO NOTHING`, approvalID, organizationID, runID, planID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE task_runs SET status='awaiting_approval',current_stage='approval' WHERE id=$1 AND status='inspecting'`, runID)
	if err != nil {
		return err
	}
	for _, event := range []string{"task_run.awaiting_approval", "approval.requested"} {
		resource := runID
		kind := "task_run"
		if event == "approval.requested" {
			kind = "approval"
			_ = tx.QueryRow(ctx, `SELECT id FROM approvals WHERE task_run_id=$1 AND revision=1`, runID).Scan(&resource)
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,$3,$4,$5,'worker',$6)`, newID(), organizationID, event, kind, resource, mustJSON(map[string]any{"plan_revision_id": planID}))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
