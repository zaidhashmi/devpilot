package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/devpilot/devpilot/services/api/internal/authz"
	"github.com/devpilot/devpilot/services/api/internal/githubapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

type Service struct {
	db         *pgxpool.Pool
	sessionTTL time.Duration
	now        func() time.Time
	github     githubapp.Client
}

func New(db *pgxpool.Pool, sessionTTL time.Duration) *Service {
	return &Service{db: db, sessionTTL: sessionTTL, now: time.Now}
}

func (s *Service) SetGitHubClient(client githubapp.Client) { s.github = client }

func (s *Service) Register(ctx context.Context, input Registration) (Session, error) {
	normalizedEmail := normalizeEmail(input.Email)
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return Session{}, err
	}
	userID, organizationID, membershipID := newID(), newID(), newID()
	slug := slugify(input.OrganizationName) + "-" + strings.ToLower(strings.ReplaceAll(organizationID.String(), "-", ""))[:8]
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, fmt.Errorf("begin registration: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `INSERT INTO users(id,email,normalized_email,display_name,password_hash,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)`, userID, strings.TrimSpace(input.Email), normalizedEmail, strings.TrimSpace(input.DisplayName), passwordHash, now)
	if isUniqueViolation(err) {
		return Session{}, ErrConflict
	}
	if err != nil {
		return Session{}, fmt.Errorf("create user: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO organizations(id,name,slug,created_at,updated_at) VALUES($1,$2,$3,$4,$4)`, organizationID, strings.TrimSpace(input.OrganizationName), slug, now)
	if err != nil {
		return Session{}, fmt.Errorf("create organization: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_memberships(id,organization_id,user_id,role,created_at,updated_at) VALUES($1,$2,$3,'owner',$4,$4)`, membershipID, organizationID, userID, now)
	if err != nil {
		return Session{}, fmt.Errorf("create membership: %w", err)
	}
	for _, event := range []struct {
		kind, resource string
		id             uuid.UUID
	}{{"user.registered", "user", userID}, {"organization.created", "organization", organizationID}, {"membership.created", "organization_membership", membershipID}} {
		if err := insertAudit(ctx, tx, organizationID, userID, event.kind, event.resource, event.id, input.RequestID, nil, now); err != nil {
			return Session{}, err
		}
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return Session{}, err
	}
	sessionID := newID()
	expires := now.Add(s.sessionTTL)
	_, err = tx.Exec(ctx, `INSERT INTO sessions(id,user_id,token_hash,expires_at,created_at,last_seen_at,user_agent) VALUES($1,$2,$3,$4,$5,$5,$6)`, sessionID, userID, tokenHash, expires, now, cleanUserAgent(input.UserAgent))
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit registration: %w", err)
	}
	return Session{Token: token, ExpiresAt: expires, Actor: Actor{User: User{ID: userID.String(), Email: strings.TrimSpace(input.Email), DisplayName: strings.TrimSpace(input.DisplayName), Status: "active", CreatedAt: now}, Organization: Organization{ID: organizationID.String(), Name: strings.TrimSpace(input.OrganizationName), Slug: slug, Status: "active", CreatedAt: now, UpdatedAt: now}, Membership: Membership{ID: membershipID.String(), OrganizationID: organizationID.String(), UserID: userID.String(), Role: authz.RoleOwner, Status: "active"}, SessionID: sessionID.String()}}, nil
}

func (s *Service) Login(ctx context.Context, email, password, requestID, userAgent string) (Session, error) {
	var user User
	var passwordHash string
	err := s.db.QueryRow(ctx, `SELECT id,email,display_name,status,created_at,password_hash FROM users WHERE normalized_email=$1`, normalizeEmail(email)).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Status, &user.CreatedAt, &passwordHash)
	if err != nil {
		// Equalize the obvious missing-user path with a real Argon2id operation.
		_, _ = hashPassword(password)
		return Session{}, ErrInvalidCredentials
	}
	if user.Status != "active" || !verifyPassword(passwordHash, password) {
		return Session{}, ErrInvalidCredentials
	}
	actor, err := s.actorForUser(ctx, user)
	if err != nil {
		return Session{}, err
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return Session{}, err
	}
	now, expires, sessionID := s.now().UTC(), s.now().UTC().Add(s.sessionTTL), newID()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, `INSERT INTO sessions(id,user_id,token_hash,expires_at,created_at,last_seen_at,user_agent) VALUES($1,$2,$3,$4,$5,$5,$6)`, sessionID, user.ID, tokenHash, expires, now, cleanUserAgent(userAgent))
	if err == nil {
		err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(user.ID), "auth.login_succeeded", "session", sessionID, requestID, nil, now)
	}
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	actor.SessionID = sessionID.String()
	return Session{Token: token, ExpiresAt: expires, Actor: actor}, nil
}

func (s *Service) Authenticate(ctx context.Context, token, organizationID string) (Actor, error) {
	hash := sha256.Sum256([]byte(token))
	var user User
	var sessionID string
	err := s.db.QueryRow(ctx, `SELECT u.id,u.email,u.display_name,u.status,u.created_at,s.id FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now()`, hash[:]).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Status, &user.CreatedAt, &sessionID)
	if err != nil || user.Status != "active" {
		return Actor{}, ErrUnauthenticated
	}
	actor, err := s.actorForUserAndOrganization(ctx, user, organizationID)
	if err != nil {
		return Actor{}, err
	}
	actor.SessionID = sessionID
	_, _ = s.db.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE id=$1 AND last_seen_at < now()-interval '5 minutes'`, sessionID)
	return actor, nil
}

func (s *Service) Logout(ctx context.Context, actor Actor, requestID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, actor.SessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if err = insertAudit(ctx, tx, uuid.MustParse(actor.Organization.ID), uuid.MustParse(actor.User.ID), "auth.logout", "session", uuid.MustParse(actor.SessionID), requestID, nil, s.now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) UpdateOrganization(ctx context.Context, actor Actor, name, requestID string) (Organization, error) {
	if !authz.Allowed(actor.Membership.Role, authz.OrganizationManage) {
		return Organization{}, ErrForbidden
	}
	var org Organization
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	err = tx.QueryRow(ctx, `UPDATE organizations SET name=$1,updated_at=now() WHERE id=$2 AND status='active' RETURNING id,name,slug,status,created_at,updated_at`, strings.TrimSpace(name), actor.Organization.ID).Scan(&org.ID, &org.Name, &org.Slug, &org.Status, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		return Organization{}, err
	}
	meta, _ := json.Marshal(map[string]string{"name": org.Name})
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata) VALUES($1,$2,$3,'organization.updated','organization',$2,$4,$5)`, newID(), org.ID, actor.User.ID, requestID, meta)
	if err != nil {
		return Organization{}, err
	}
	return org, tx.Commit(ctx)
}

func (s *Service) Members(ctx context.Context, actor Actor) ([]Membership, error) {
	if !authz.Allowed(actor.Membership.Role, authz.MembersRead) {
		return nil, ErrForbidden
	}
	rows, err := s.db.Query(ctx, `SELECT m.id,m.organization_id,m.user_id,m.role,m.status,u.email,u.display_name FROM organization_memberships m JOIN users u ON u.id=m.user_id WHERE m.organization_id=$1 ORDER BY m.created_at`, actor.Organization.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Membership{}
	for rows.Next() {
		var m Membership
		var role string
		var u MemberUser
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.UserID, &role, &m.Status, &u.Email, &u.DisplayName); err != nil {
			return nil, err
		}
		m.Role = authz.Role(role)
		m.User = &u
		result = append(result, m)
	}
	return result, rows.Err()
}

func (s *Service) actorForUser(ctx context.Context, user User) (Actor, error) {
	return s.actorForUserAndOrganization(ctx, user, "")
}
func (s *Service) actorForUserAndOrganization(ctx context.Context, user User, organizationID string) (Actor, error) {
	query := `SELECT o.id,o.name,o.slug,o.status,o.created_at,o.updated_at,m.id,m.role,m.status FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id WHERE m.user_id=$1 AND m.status='active' AND o.status='active'`
	args := []any{user.ID}
	if organizationID != "" {
		query += ` AND o.id=$2`
		args = append(args, organizationID)
	}
	query += ` ORDER BY m.created_at LIMIT 1`
	var a Actor
	a.User = user
	var role string
	err := s.db.QueryRow(ctx, query, args...).Scan(&a.Organization.ID, &a.Organization.Name, &a.Organization.Slug, &a.Organization.Status, &a.Organization.CreatedAt, &a.Organization.UpdatedAt, &a.Membership.ID, &role, &a.Membership.Status)
	if err != nil {
		return Actor{}, ErrForbidden
	}
	a.Membership.OrganizationID = a.Organization.ID
	a.Membership.UserID = user.ID
	a.Membership.Role = authz.Role(role)
	return a, nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, org, user uuid.UUID, event, resource string, resourceID uuid.UUID, requestID string, metadata []byte, now time.Time) error {
	if metadata == nil {
		metadata = []byte(`{}`)
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,organization_id,actor_user_id,event_type,resource_type,resource_id,request_id,metadata,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, newID(), org, user, event, resource, resourceID, requestID, metadata, now)
	return err
}
func newID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic("secure UUID generation failed: " + err.Error())
	}
	return id
}
func normalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func slugify(value string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-"), "-")
	if slug == "" {
		return "organization"
	}
	return slug
}
func newSessionToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}
func cleanUserAgent(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return err != nil && errors.As(err, &pgErr) && pgErr.Code == "23505"
}
