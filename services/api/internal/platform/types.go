package platform

import (
	"errors"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/authz"
)

var (
	ErrConflict           = errors.New("resource already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrForbidden          = errors.New("forbidden")
	ErrNotFound           = errors.New("not found")
	ErrUnavailable        = errors.New("integration unavailable")
	ErrInvalidWebhook     = errors.New("invalid webhook payload")
	ErrInvalidInput       = errors.New("invalid input")
)

type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Membership struct {
	ID             string      `json:"id"`
	OrganizationID string      `json:"organization_id"`
	UserID         string      `json:"user_id"`
	Role           authz.Role  `json:"role"`
	Status         string      `json:"status"`
	User           *MemberUser `json:"user,omitempty"`
}

type MemberUser struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type Actor struct {
	User         User         `json:"user"`
	Organization Organization `json:"organization"`
	Membership   Membership   `json:"membership"`
	SessionID    string       `json:"-"`
}

type Registration struct {
	Email            string
	DisplayName      string
	Password         string
	OrganizationName string
	RequestID        string
	UserAgent        string
}

type Session struct {
	Token     string
	ExpiresAt time.Time
	Actor     Actor
}

type GitHubInstallation struct {
	ID                   string     `json:"id"`
	GitHubInstallationID int64      `json:"github_installation_id"`
	GitHubAccountID      int64      `json:"github_account_id"`
	GitHubAccountLogin   string     `json:"github_account_login"`
	GitHubAccountType    string     `json:"github_account_type"`
	RepositorySelection  string     `json:"repository_selection"`
	Status               string     `json:"status"`
	SuspendedAt          *time.Time `json:"suspended_at,omitempty"`
	RepositoryCount      int        `json:"repository_count"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type Repository struct {
	ID                 string     `json:"id"`
	GitHubRepositoryID int64      `json:"github_repository_id"`
	Owner              string     `json:"owner"`
	Name               string     `json:"name"`
	FullName           string     `json:"full_name"`
	DefaultBranch      string     `json:"default_branch"`
	Private            bool       `json:"private"`
	Archived           bool       `json:"archived"`
	Disabled           bool       `json:"disabled"`
	Available          bool       `json:"available"`
	HTMLURL            string     `json:"html_url"`
	GitHubUpdatedAt    *time.Time `json:"github_updated_at,omitempty"`
	LastSyncedAt       time.Time  `json:"last_synced_at"`
}

type Workspace struct {
	ID                      string     `json:"id"`
	RepositoryID            string     `json:"repository_id"`
	RepositoryFullName      string     `json:"repository_full_name"`
	RequestedRef            string     `json:"requested_ref"`
	ResolvedCommitSHA       string     `json:"resolved_commit_sha"`
	Status                  string     `json:"status"`
	InspectionArtifact      any        `json:"inspection_artifact,omitempty"`
	FailureCode             *string    `json:"failure_code,omitempty"`
	CancellationRequestedAt *time.Time `json:"cancellation_requested_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	StartedAt               *time.Time `json:"started_at,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
}

type EngineeringTask struct {
	ID                 string     `json:"id"`
	RepositoryID       string     `json:"repository_id"`
	RepositoryFullName string     `json:"repository_full_name"`
	CreatedByUserID    string     `json:"created_by_user_id"`
	Title              string     `json:"title"`
	Objective          string     `json:"objective"`
	Status             string     `json:"status"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	LatestRun          *TaskRun   `json:"latest_run,omitempty"`
}

type TaskRun struct {
	ID                      string        `json:"id"`
	EngineeringTaskID       string        `json:"engineering_task_id"`
	RepositoryID            string        `json:"repository_id"`
	RepositoryFullName      string        `json:"repository_full_name"`
	WorkspaceID             *string       `json:"workspace_id,omitempty"`
	RunNumber               int           `json:"run_number"`
	RequestedRef            string        `json:"requested_ref"`
	ResolvedCommitSHA       string        `json:"resolved_commit_sha"`
	Status                  string        `json:"status"`
	CurrentStage            string        `json:"current_stage"`
	CreatedByUserID         string        `json:"created_by_user_id"`
	FailureCode             *string       `json:"failure_code,omitempty"`
	CancellationRequestedAt *time.Time    `json:"cancellation_requested_at,omitempty"`
	CreatedAt               time.Time     `json:"created_at"`
	StartedAt               *time.Time    `json:"started_at,omitempty"`
	CompletedAt             *time.Time    `json:"completed_at,omitempty"`
	Workspace               *Workspace    `json:"workspace,omitempty"`
	Plan                    *PlanRevision `json:"plan_revision,omitempty"`
	Approval                *Approval     `json:"approval,omitempty"`
}

type PlanRevision struct {
	ID             string         `json:"id"`
	TaskRunID      string         `json:"task_run_id"`
	RevisionNumber int            `json:"revision_number"`
	Source         string         `json:"source"`
	Summary        string         `json:"summary"`
	Payload        map[string]any `json:"structured_payload"`
	CreatedAt      time.Time      `json:"created_at"`
}
type Approval struct {
	ID              string     `json:"id"`
	TaskRunID       string     `json:"task_run_id"`
	PlanRevisionID  string     `json:"plan_revision_id"`
	ApprovalType    string     `json:"approval_type"`
	Revision        int        `json:"revision"`
	Status          string     `json:"status"`
	RequestedAt     time.Time  `json:"requested_at"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	DecidedByUserID *string    `json:"decided_by_user_id,omitempty"`
	DecisionComment *string    `json:"decision_comment,omitempty"`
}
