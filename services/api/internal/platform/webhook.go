package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type GitHubWebhook struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	RepositoriesAdded   []webhookRepository `json:"repositories_added"`
	RepositoriesRemoved []webhookRepository `json:"repositories_removed"`
}

type webhookRepository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Disabled      bool   `json:"disabled"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Service) ProcessGitHubWebhook(ctx context.Context, deliveryID, eventType string, payload []byte) (bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO github_webhook_deliveries(delivery_id,event_type,status) VALUES($1,$2,'processing') ON CONFLICT DO NOTHING`, deliveryID, eventType)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		tag, err = s.db.Exec(ctx, `UPDATE github_webhook_deliveries SET status='processing',error_code=NULL,processed_at=NULL WHERE delivery_id=$1 AND (status='failed' OR (status='processing' AND received_at < now()-interval '5 minutes'))`, deliveryID)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == 0 {
			return true, nil
		}
	}
	var event GitHubWebhook
	if err := json.Unmarshal(payload, &event); err != nil {
		_, _ = s.db.Exec(ctx, `UPDATE github_webhook_deliveries SET status='failed',error_code='malformed_payload',processed_at=now() WHERE delivery_id=$1`, deliveryID)
		return false, fmt.Errorf("%w: %v", ErrInvalidWebhook, err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE github_webhook_deliveries SET action=$2,github_installation_id=$3 WHERE delivery_id=$1`, deliveryID, event.Action, nullableInt64(event.Installation.ID)); err != nil {
		return false, err
	}
	if err = s.processWebhook(ctx, deliveryID, eventType, event); err != nil {
		_, _ = s.db.Exec(ctx, `UPDATE github_webhook_deliveries SET status='failed',error_code='processing_failed',processed_at=now() WHERE delivery_id=$1`, deliveryID)
		return false, err
	}
	return false, nil
}

func (s *Service) processWebhook(ctx context.Context, deliveryID, eventType string, event GitHubWebhook) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var installationID, organizationID string
	err = tx.QueryRow(ctx, `SELECT id,organization_id FROM github_installations WHERE github_installation_id=$1`, event.Installation.ID).Scan(&installationID, &organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `UPDATE github_webhook_deliveries SET status='ignored',processed_at=now() WHERE delivery_id=$1`, deliveryID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	now := s.now().UTC()
	auditType := ""
	switch eventType + ":" + event.Action {
	case "installation:created":
		_, err = tx.Exec(ctx, `UPDATE github_installations SET status='active',suspended_at=NULL,updated_at=$2 WHERE id=$1`, installationID, now)
		auditType = "github.installation_activated"
	case "installation:deleted":
		_, err = tx.Exec(ctx, `UPDATE github_installations SET status='removed',suspended_at=NULL,updated_at=$2 WHERE id=$1`, installationID, now)
		auditType = "github.installation_removed"
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE repositories SET available=false,updated_at=$2 WHERE github_installation_id=$1`, installationID, now)
		}
	case "installation:suspend", "installation:suspended":
		_, err = tx.Exec(ctx, `UPDATE github_installations SET status='suspended',suspended_at=$2,updated_at=$2 WHERE id=$1`, installationID, now)
		auditType = "github.installation_suspended"
	case "installation:unsuspend", "installation:unsuspended":
		_, err = tx.Exec(ctx, `UPDATE github_installations SET status='active',suspended_at=NULL,updated_at=$2 WHERE id=$1`, installationID, now)
		auditType = "github.installation_unsuspended"
	case "installation_repositories:added":
		for _, r := range event.RepositoriesAdded {
			if err = upsertWebhookRepository(ctx, tx, organizationID, installationID, r, now); err != nil {
				break
			}
		}
		auditType = "github.repositories_added"
	case "installation_repositories:removed":
		for _, r := range event.RepositoriesRemoved {
			_, err = tx.Exec(ctx, `UPDATE repositories SET available=false,updated_at=$3 WHERE github_installation_id=$1 AND github_repository_id=$2`, installationID, r.ID, now)
			if err != nil {
				break
			}
		}
		auditType = "github.repositories_removed"
	default:
		_, err = tx.Exec(ctx, `UPDATE github_webhook_deliveries SET status='ignored',processed_at=$2 WHERE delivery_id=$1`, deliveryID, now)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if err = insertSystemAudit(ctx, tx, organizationID, auditType, installationID, deliveryID, map[string]any{"github_installation_id": event.Installation.ID}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE github_webhook_deliveries SET status='processed',processed_at=$2 WHERE delivery_id=$1`, deliveryID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertWebhookRepository(ctx context.Context, tx pgx.Tx, organizationID, installationID string, r webhookRepository, now time.Time) error {
	owner := r.Owner.Login
	if owner == "" {
		parts := splitFullName(r.FullName)
		owner = parts[0]
	}
	name := r.Name
	if name == "" {
		parts := splitFullName(r.FullName)
		name = parts[1]
	}
	_, err := tx.Exec(ctx, `INSERT INTO repositories(id,organization_id,github_installation_id,github_repository_id,owner,name,full_name,default_branch,private,archived,disabled,available,html_url,github_updated_at,last_synced_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,$12,$13,$14,$14,$14) ON CONFLICT (organization_id,github_repository_id) DO UPDATE SET github_installation_id=excluded.github_installation_id,owner=excluded.owner,name=excluded.name,full_name=excluded.full_name,default_branch=excluded.default_branch,private=excluded.private,archived=excluded.archived,disabled=excluded.disabled,available=true,html_url=excluded.html_url,github_updated_at=excluded.github_updated_at,last_synced_at=excluded.last_synced_at,updated_at=excluded.updated_at`, newID(), organizationID, installationID, r.ID, owner, name, r.FullName, r.DefaultBranch, r.Private, r.Archived, r.Disabled, r.HTMLURL, nullableTime(r.UpdatedAt), now)
	return err
}
func splitFullName(value string) [2]string {
	for i, c := range value {
		if c == '/' {
			return [2]string{value[:i], value[i+1:]}
		}
	}
	return [2]string{"", value}
}
func insertSystemAudit(ctx context.Context, tx pgx.Tx, organizationID, eventType, resourceID, deliveryID string, metadata map[string]any) error {
	data, _ := json.Marshal(metadata)
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,NULL,$3,'github_installation',$4,$5,$6)`, newID(), organizationID, eventType, resourceID, "github:"+deliveryID, data)
	return err
}
func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
