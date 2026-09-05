package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/authz"
	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const installationStateTTL = 10 * time.Minute

func (s *Service) BeginGitHubInstallation(ctx context.Context, actor Actor) (string, error) {
	if !authz.Allowed(actor.Membership.Role, authz.GitHubManage) {
		return "", ErrForbidden
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	_, err := s.db.Exec(ctx, `INSERT INTO github_installation_states(id,token_hash,organization_id,initiating_user_id,expires_at) VALUES($1,$2,$3,$4,$5)`, newID(), hash[:], actor.Organization.ID, actor.User.ID, s.now().UTC().Add(installationStateTTL))
	if err != nil {
		return "", fmt.Errorf("create GitHub installation state: %w", err)
	}
	return token, nil
}

func (s *Service) CompleteGitHubInstallation(ctx context.Context, actor Actor, state, authorizationCode string, githubInstallationID int64, requestID string) (GitHubInstallation, error) {
	if !authz.Allowed(actor.Membership.Role, authz.GitHubManage) {
		return GitHubInstallation{}, ErrForbidden
	}
	if s.github == nil {
		return GitHubInstallation{}, ErrUnavailable
	}
	hash := sha256.Sum256([]byte(state))
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return GitHubInstallation{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var stateID, organizationID, userID string
	err = tx.QueryRow(ctx, `SELECT id,organization_id,initiating_user_id FROM github_installation_states WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now() FOR UPDATE`, hash[:]).Scan(&stateID, &organizationID, &userID)
	if err != nil || organizationID != actor.Organization.ID || userID != actor.User.ID {
		return GitHubInstallation{}, ErrForbidden
	}
	userToken, err := s.github.ExchangeUserCode(ctx, authorizationCode)
	if err != nil {
		return GitHubInstallation{}, s.bindingVerificationFailure(ctx, tx, stateID, err)
	}
	// The user token is intentionally scoped to this verification call chain. It is
	// never persisted, cached, logged, audited, or used for repository operations.
	userInstallation, err := s.github.GetUserInstallation(ctx, userToken, githubInstallationID)
	if err != nil {
		return GitHubInstallation{}, s.bindingVerificationFailure(ctx, tx, stateID, err)
	}
	metadata, err := s.github.GetInstallation(ctx, githubInstallationID)
	if err != nil {
		return GitHubInstallation{}, s.bindingVerificationFailure(ctx, tx, stateID, err)
	}
	if userInstallation.ID != metadata.ID || userInstallation.AccountID != metadata.AccountID {
		return GitHubInstallation{}, s.consumeRejectedBinding(ctx, tx, stateID)
	}
	repositories, err := s.github.ListRepositories(ctx, githubInstallationID)
	if err != nil {
		return GitHubInstallation{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE github_installation_states SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, stateID)
	if err != nil || tag.RowsAffected() != 1 {
		return GitHubInstallation{}, ErrForbidden
	}
	installationID := newID()
	now := s.now().UTC()
	var result GitHubInstallation
	err = tx.QueryRow(ctx, `INSERT INTO github_installations(id,organization_id,github_installation_id,github_account_id,github_account_login,github_account_type,repository_selection,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,'active',$8,$8)
		ON CONFLICT (github_installation_id) DO UPDATE SET github_account_id=excluded.github_account_id,github_account_login=excluded.github_account_login,github_account_type=excluded.github_account_type,repository_selection=excluded.repository_selection,status='active',suspended_at=NULL,updated_at=excluded.updated_at
		WHERE github_installations.organization_id=excluded.organization_id
		RETURNING id,github_installation_id,github_account_id,github_account_login,github_account_type,repository_selection,status,suspended_at,created_at,updated_at`, installationID, organizationID, metadata.ID, metadata.AccountID, metadata.AccountLogin, metadata.AccountType, metadata.RepositorySelection, now).Scan(&result.ID, &result.GitHubInstallationID, &result.GitHubAccountID, &result.GitHubAccountLogin, &result.GitHubAccountType, &result.RepositorySelection, &result.Status, &result.SuspendedAt, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GitHubInstallation{}, ErrConflict
		}
		if isUniqueViolation(err) {
			return GitHubInstallation{}, ErrConflict
		}
		return GitHubInstallation{}, err
	}
	if err := reconcileRepositories(ctx, tx, organizationID, result.ID, repositories, now); err != nil {
		return GitHubInstallation{}, err
	}
	result.RepositoryCount = len(repositories)
	if err := insertAudit(ctx, tx, uuid.MustParse(organizationID), uuid.MustParse(actor.User.ID), "github.installation_connected", "github_installation", uuid.MustParse(result.ID), requestID, mustJSON(map[string]any{"github_installation_id": githubInstallationID, "repository_count": len(repositories)}), now); err != nil {
		return GitHubInstallation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GitHubInstallation{}, err
	}
	return result, nil
}

func (s *Service) bindingVerificationFailure(ctx context.Context, tx pgx.Tx, stateID string, err error) error {
	if errors.Is(err, githubapp.ErrUnauthorized) || errors.Is(err, githubapp.ErrForbidden) || errors.Is(err, githubapp.ErrNotFound) {
		return s.consumeRejectedBinding(ctx, tx, stateID)
	}
	// Transient GitHub and rate-limit failures roll the transaction back, preserving
	// the state for a legitimate retry while the row lock prevents concurrent use.
	return err
}

func (s *Service) consumeRejectedBinding(ctx context.Context, tx pgx.Tx, stateID string) error {
	if _, err := tx.Exec(ctx, `UPDATE github_installation_states SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, stateID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return ErrForbidden
}

func (s *Service) GitHubInstallation(ctx context.Context, actor Actor) (*GitHubInstallation, error) {
	if !authz.Allowed(actor.Membership.Role, authz.GitHubRead) {
		return nil, ErrForbidden
	}
	var v GitHubInstallation
	err := s.db.QueryRow(ctx, `SELECT i.id,i.github_installation_id,i.github_account_id,i.github_account_login,i.github_account_type,i.repository_selection,i.status,i.suspended_at,i.created_at,i.updated_at,count(r.id) FILTER (WHERE r.available) FROM github_installations i LEFT JOIN repositories r ON r.github_installation_id=i.id WHERE i.organization_id=$1 AND i.status<>'removed' GROUP BY i.id`, actor.Organization.ID).Scan(&v.ID, &v.GitHubInstallationID, &v.GitHubAccountID, &v.GitHubAccountLogin, &v.GitHubAccountType, &v.RepositorySelection, &v.Status, &v.SuspendedAt, &v.CreatedAt, &v.UpdatedAt, &v.RepositoryCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, err
}

func (s *Service) Repositories(ctx context.Context, actor Actor) ([]Repository, error) {
	if !authz.Allowed(actor.Membership.Role, authz.RepositoriesRead) {
		return nil, ErrForbidden
	}
	rows, err := s.db.Query(ctx, `SELECT id,github_repository_id,owner,name,full_name,default_branch,private,archived,disabled,available,html_url,github_updated_at,last_synced_at FROM repositories WHERE organization_id=$1 AND available=true ORDER BY full_name`, actor.Organization.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Repository{}
	for rows.Next() {
		var r Repository
		if err := rows.Scan(&r.ID, &r.GitHubRepositoryID, &r.Owner, &r.Name, &r.FullName, &r.DefaultBranch, &r.Private, &r.Archived, &r.Disabled, &r.Available, &r.HTMLURL, &r.GitHubUpdatedAt, &r.LastSyncedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Service) SyncGitHub(ctx context.Context, actor Actor, requestID string) (int, error) {
	if !authz.Allowed(actor.Membership.Role, authz.GitHubManage) {
		return 0, ErrForbidden
	}
	if s.github == nil {
		return 0, ErrUnavailable
	}
	var id string
	var externalID int64
	var status string
	if err := s.db.QueryRow(ctx, `SELECT id,github_installation_id,status FROM github_installations WHERE organization_id=$1 AND status<>'removed'`, actor.Organization.ID).Scan(&id, &externalID, &status); err != nil {
		return 0, ErrNotFound
	}
	if status != "active" {
		return 0, ErrConflict
	}
	repos, err := s.github.ListRepositories(ctx, externalID)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := s.now().UTC()
	if err := reconcileRepositories(ctx, tx, actor.Organization.ID, id, repos, now); err != nil {
		return 0, err
	}
	if err := insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "github.repositories_synchronized", "github_installation", uuid.MustParse(id), requestID, mustJSON(map[string]any{"repository_count": len(repos)}), now); err != nil {
		return 0, err
	}
	return len(repos), tx.Commit(ctx)
}

func (s *Service) DisconnectGitHub(ctx context.Context, actor Actor, requestID string) error {
	if !authz.Allowed(actor.Membership.Role, authz.GitHubManage) {
		return ErrForbidden
	}
	var id string
	var externalID int64
	err := s.db.QueryRow(ctx, `SELECT id,github_installation_id FROM github_installations WHERE organization_id=$1 AND status<>'removed'`, actor.Organization.ID).Scan(&id, &externalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if s.github == nil {
		return ErrUnavailable
	}
	if err = s.github.DeleteInstallation(ctx, externalID); err != nil && !errors.Is(err, githubapp.ErrNotFound) {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `UPDATE github_installations SET status='removed',suspended_at=NULL,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE repositories SET available=false,updated_at=now() WHERE github_installation_id=$1`, id); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "github.installation_disconnected", "github_installation", uuid.MustParse(id), requestID, nil, s.now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func reconcileRepositories(ctx context.Context, tx pgx.Tx, organizationID, installationID string, repos []githubapp.Repository, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE repositories SET available=false,updated_at=$3 WHERE organization_id=$1 AND github_installation_id=$2`, organizationID, installationID, now); err != nil {
		return err
	}
	for _, r := range repos {
		_, err := tx.Exec(ctx, `INSERT INTO repositories(id,organization_id,github_installation_id,github_repository_id,owner,name,full_name,default_branch,private,archived,disabled,available,html_url,github_updated_at,last_synced_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,$12,$13,$14,$14,$14) ON CONFLICT (organization_id,github_repository_id) DO UPDATE SET github_installation_id=excluded.github_installation_id,owner=excluded.owner,name=excluded.name,full_name=excluded.full_name,default_branch=excluded.default_branch,private=excluded.private,archived=excluded.archived,disabled=excluded.disabled,available=true,html_url=excluded.html_url,github_updated_at=excluded.github_updated_at,last_synced_at=excluded.last_synced_at,updated_at=excluded.updated_at`, newID(), organizationID, installationID, r.ID, r.Owner, r.Name, r.FullName, r.DefaultBranch, r.Private, r.Archived, r.Disabled, r.HTMLURL, nullableTime(r.UpdatedAt), now)
		if err != nil {
			return err
		}
	}
	return nil
}
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }
