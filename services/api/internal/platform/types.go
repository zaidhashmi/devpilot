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
