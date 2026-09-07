package platform

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/authz"
	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/devpilot/devpilot/services/api/internal/runner"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var validWorkspaceRef = regexp.MustCompile(`^[A-Za-z0-9._/@+:-]{1,255}$`)
var workspaceLimits = runner.Limits{MaxArchiveBytes: 100 << 20, MaxExpandedBytes: 500 << 20, MaxFileBytes: 25 << 20, MaxFiles: 50000, MaxDepth: 50, AcquisitionTimeoutSeconds: 120, InspectionTimeoutSeconds: 120, OverallTimeoutSeconds: 300}

// RecoverInterruptedWorkspaces prevents process restarts from leaving work permanently active.
// Acquisition capabilities are intentionally non-durable, so interrupted work is failed closed
// and a user may create a fresh immutable workspace.
func (s *Service) RecoverInterruptedWorkspaces(ctx context.Context) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `UPDATE workspaces w SET status='failed',failure_code='control_plane_interrupted',completed_at=now(),updated_at=now() WHERE status IN ('pending','acquiring','inspecting') AND NOT EXISTS (SELECT 1 FROM task_runs tr WHERE tr.workspace_id=w.id AND tr.status IN ('pending','inspecting')) RETURNING id,organization_id`)
	if err != nil {
		return err
	}
	type item struct{ id, org string }
	var items []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.org); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, x := range items {
		_, err = tx.Exec(ctx, `UPDATE workspace_attempts SET status='failed',failure_code='control_plane_interrupted',completed_at=now() WHERE workspace_id=$1 AND status IN ('acquiring','inspecting')`, x.id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,'workspace.failed','workspace',$3,'system','{"failure_code":"control_plane_interrupted"}')`, newID(), x.org, x.id)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) CreateWorkspace(ctx context.Context, actor Actor, repositoryID, ref, requestID string) (Workspace, error) {
	if !authz.Allowed(actor.Membership.Role, authz.WorkspacesCreate) {
		return Workspace{}, ErrForbidden
	}
	content, ok := s.github.(githubapp.ContentClient)
	if !ok || s.runner == nil {
		return Workspace{}, ErrUnavailable
	}
	if !validWorkspaceRef.MatchString(ref) {
		return Workspace{}, ErrInvalidInput
	}
	var repo Repository
	var installationID int64
	err := s.db.QueryRow(ctx, `SELECT r.id,r.github_repository_id,r.owner,r.name,r.full_name,r.default_branch,r.private,r.archived,r.disabled,r.available,i.github_installation_id FROM repositories r JOIN github_installations i ON i.id=r.github_installation_id WHERE r.id=$1 AND r.organization_id=$2 AND r.available=true AND r.disabled=false AND i.status='active'`, repositoryID, actor.Organization.ID).Scan(&repo.ID, &repo.GitHubRepositoryID, &repo.Owner, &repo.Name, &repo.FullName, &repo.DefaultBranch, &repo.Private, &repo.Archived, &repo.Disabled, &repo.Available, &installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, err
	}
	sha, err := content.ResolveCommit(ctx, installationID, repo.GitHubRepositoryID, repo.Owner, repo.Name, ref)
	if err != nil {
		return Workspace{}, err
	}
	archiveURL, err := content.ArchiveURL(ctx, installationID, repo.GitHubRepositoryID, repo.Owner, repo.Name, sha)
	if err != nil {
		return Workspace{}, err
	}
	now, id := s.now().UTC(), newID()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, `INSERT INTO workspaces(id,organization_id,repository_id,requested_ref,resolved_commit_sha,status,created_by_user_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,$7)`, id, actor.Organization.ID, repo.ID, ref, sha, actor.User.ID, now)
	if err != nil {
		return Workspace{}, err
	}
	if err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "workspace.created", "workspace", id, requestID, mustJSON(map[string]any{"repository_id": repo.ID, "requested_ref": ref, "resolved_commit_sha": sha}), now); err != nil {
		return Workspace{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Workspace{}, err
	}
	go s.runWorkspace(runner.Request{WorkspaceID: id.String(), Repository: repo.FullName, CommitSHA: sha, ArchiveURL: archiveURL, Limits: workspaceLimits}, actor.Organization.ID)
	return Workspace{ID: id.String(), RepositoryID: repo.ID, RepositoryFullName: repo.FullName, RequestedRef: ref, ResolvedCommitSHA: sha, Status: "pending", CreatedAt: now}, nil
}

func (s *Service) runWorkspace(input runner.Request, organizationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(input.Limits.OverallTimeoutSeconds)*time.Second)
	defer cancel()
	attemptID := newID()
	_, _ = s.db.Exec(ctx, `UPDATE workspaces SET status='acquiring',started_at=now(),updated_at=now() WHERE id=$1 AND status='pending'`, input.WorkspaceID)
	_, _ = s.db.Exec(ctx, `INSERT INTO workspace_attempts(id,workspace_id,attempt_number,status) SELECT $1,$2,COALESCE(max(attempt_number),0)+1,'acquiring' FROM workspace_attempts WHERE workspace_id=$2`, attemptID, input.WorkspaceID)
	result, runErr := s.runner.Inspect(ctx, input)
	status, failure := "failed", "runner_failed"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status, failure = "timed_out", "overall_timeout"
	} else if runErr == nil {
		status, failure = result.Status, result.FailureCode
		if result.WorkspaceID != input.WorkspaceID {
			status, failure = "failed", "invalid_runner_result"
		}
	}
	if status != "succeeded" && status != "failed" && status != "cancelled" && status != "timed_out" {
		status, failure = "failed", "invalid_runner_result"
	}
	var artifact []byte
	if status == "succeeded" && result.Artifact != nil {
		var marshalErr error
		artifact, marshalErr = json.Marshal(result.Artifact)
		if marshalErr != nil {
			status, failure = "failed", "invalid_artifact"
		}
	} else if status == "succeeded" {
		status, failure = "failed", "missing_artifact"
	}
	tx, err := s.db.Begin(context.Background())
	if err != nil {
		return
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	tag, err := tx.Exec(context.Background(), `UPDATE workspaces SET status=$2,failure_code=NULLIF($3,''),inspection_artifact=$4,completed_at=now(),updated_at=now() WHERE id=$1 AND status IN ('pending','acquiring','inspecting')`, input.WorkspaceID, status, failure, artifact)
	if err != nil || tag.RowsAffected() == 0 {
		return
	}
	_, _ = tx.Exec(context.Background(), `UPDATE workspace_attempts SET status=$2,failure_code=NULLIF($3,''),completed_at=now() WHERE id=$1`, attemptID, status, failure)
	_, _ = tx.Exec(context.Background(), `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata,created_at) VALUES($1,$2,NULL,$3,'workspace',$4,'system',$5,now())`, newID(), organizationID, "workspace."+status, input.WorkspaceID, mustJSON(map[string]any{"failure_code": failure}))
	_ = tx.Commit(context.Background())
}

func (s *Service) Workspace(ctx context.Context, actor Actor, id string) (Workspace, error) {
	if !authz.Allowed(actor.Membership.Role, authz.WorkspacesRead) {
		return Workspace{}, ErrForbidden
	}
	var w Workspace
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT w.id,w.repository_id,r.full_name,w.requested_ref,w.resolved_commit_sha,w.status,w.inspection_artifact,w.failure_code,w.cancellation_requested_at,w.created_at,w.started_at,w.completed_at FROM workspaces w JOIN repositories r ON r.id=w.repository_id WHERE w.id=$1 AND w.organization_id=$2`, id, actor.Organization.ID).Scan(&w.ID, &w.RepositoryID, &w.RepositoryFullName, &w.RequestedRef, &w.ResolvedCommitSHA, &w.Status, &raw, &w.FailureCode, &w.CancellationRequestedAt, &w.CreatedAt, &w.StartedAt, &w.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &w.InspectionArtifact)
	}
	return w, nil
}

func (s *Service) CancelWorkspace(ctx context.Context, actor Actor, id, requestID string) error {
	if !authz.Allowed(actor.Membership.Role, authz.WorkspacesCancel) {
		return ErrForbidden
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `UPDATE workspaces SET cancellation_requested_at=COALESCE(cancellation_requested_at,now()),status='cancelled',completed_at=now(),updated_at=now() WHERE id=$1 AND organization_id=$2 AND status IN ('pending','acquiring','inspecting')`, id, actor.Organization.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE id=$1 AND organization_id=$2)`, id, actor.Organization.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return ErrConflict
	}
	_, _ = tx.Exec(ctx, `UPDATE workspace_attempts SET status='cancelled',failure_code='cancelled_by_user',completed_at=now() WHERE workspace_id=$1 AND status IN ('acquiring','inspecting')`, id)
	if err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "workspace.cancel_requested", "workspace", uuid.MustParse(id), requestID, nil, s.now().UTC()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,'workspace.cancelled','workspace',$3,'system','{}')`, newID(), actor.Organization.ID, id)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.runner.Cancel(c, id)
	}()
	return nil
}
