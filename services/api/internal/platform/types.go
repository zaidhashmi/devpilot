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
