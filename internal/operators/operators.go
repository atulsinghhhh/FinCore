// Package operators manages dashboard accounts: invite-only sign-up, two
// roles (admin, viewer), sign-in, and an append-only audit trail.
//
// The dashboard is the only client (it authenticates to the API with the
// admin token and passes the acting operator's ID). There is no public
// sign-up: every account starts as an invite from an admin, or from the
// `operator` CLI for the very first admin.
package operators

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"fincore/internal/store"
)

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"

	StatusActive   = "active"
	StatusDisabled = "disabled"

	// InviteValidity is how long an invite link works.
	InviteValidity    = 72 * time.Hour
	MinPasswordLength = 12
	bcryptCost        = 12
)

var (
	ErrInvalid        = errors.New("operators: invalid request")
	ErrNotFound       = errors.New("operators: not found")
	ErrConflict       = errors.New("operators: conflict")
	ErrInviteInvalid  = errors.New("operators: invite is invalid, used, revoked or expired")
	ErrBadCredentials = errors.New("operators: incorrect email or password")
	// ErrLastAdmin protects against locking everyone out.
	ErrLastAdmin = errors.New("operators: at least one active admin must remain")
)

type Operator struct {
	ID             uuid.UUID  `json:"id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	SessionVersion int32      `json:"session_version"`
	CreatedAt      time.Time  `json:"created_at"`
	LastLoginAt    *time.Time `json:"last_login_at"`
}

type Invite struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	Role          string     `json:"role"`
	InvitedBy     *uuid.UUID `json:"invited_by"`
	InvitedByName *string    `json:"invited_by_name"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

type Event struct {
	ID        int64      `json:"id"`
	ActorID   *uuid.UUID `json:"actor_id"`
	ActorName *string    `json:"actor_name"`
	Action    string     `json:"action"`
	Target    string     `json:"target"`
	Detail    string     `json:"detail"`
	CreatedAt time.Time  `json:"created_at"`
}

type Service struct {
	pool *pgxpool.Pool
	q    *store.Queries
	// dummyHash keeps sign-in timing the same for unknown emails.
	dummyHash []byte
}

func NewService(pool *pgxpool.Pool) *Service {
	h, _ := bcrypt.GenerateFromPassword([]byte("timing-equaliser"), bcryptCost)
	return &Service{pool: pool, q: store.New(pool), dummyHash: h}
}

// Invite creates an invite and returns its one-time token. Inviting an email
// that already has an open invite replaces it.
func (s *Service) Invite(ctx context.Context, actor *uuid.UUID, email, role string) (Invite, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return Invite{}, "", err
	}
	if role != RoleAdmin && role != RoleViewer {
		return Invite{}, "", fmt.Errorf("%w: role must be admin or viewer", ErrInvalid)
	}
	if _, err := s.q.GetOperatorByEmail(ctx, email); err == nil {
		return Invite{}, "", fmt.Errorf("%w: %s already has an account", ErrConflict, email)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, "", err
	}

	token := newToken()
	var row store.OperatorInvite
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.RevokeOpenInvitesForEmail(ctx, email); err != nil {
			return err
		}
		row, err = q.CreateInvite(ctx, store.CreateInviteParams{
			Email: email, Role: role, TokenHash: hashToken(token), InvitedBy: nullable(actor), ValidHours: int32(InviteValidity.Hours()),
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, actor, "invite.created", email, "role "+role)
	})
	if err != nil {
		return Invite{}, "", err
	}
	return Invite{ID: row.ID, Email: row.Email, Role: row.Role, InvitedBy: ptr(row.InvitedBy), ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}, token, nil
}

// LookupInvite returns an open invite for the accept page.
func (s *Service) LookupInvite(ctx context.Context, token string) (Invite, error) {
	row, err := s.q.GetInviteByTokenHash(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Invite{}, ErrInviteInvalid
	}
	if err != nil {
		return Invite{}, err
	}
	if row.AcceptedAt != nil || row.RevokedAt != nil || time.Now().After(row.ExpiresAt) {
		return Invite{}, ErrInviteInvalid
	}
	return Invite{ID: row.ID, Email: row.Email, Role: row.Role, InvitedBy: ptr(row.InvitedBy), InvitedByName: row.InvitedByName, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}, nil
}

// AcceptInvite turns an invite into an active account. The token works once.
func (s *Service) AcceptInvite(ctx context.Context, token, name, password string) (Operator, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return Operator{}, fmt.Errorf("%w: name is required (max 100 characters)", ErrInvalid)
	}
	if err := checkPassword(password); err != nil {
		return Operator{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return Operator{}, err
	}

	var op store.Operator
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inv, err := q.LockInviteByTokenHash(ctx, hashToken(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteInvalid
		}
		if err != nil {
			return err
		}
		if inv.AcceptedAt != nil || inv.RevokedAt != nil || time.Now().After(inv.ExpiresAt) {
			return ErrInviteInvalid
		}
		op, err = q.CreateOperator(ctx, store.CreateOperatorParams{Email: inv.Email, Name: name, Role: inv.Role, PasswordHash: string(hash)})
		if isUnique(err) {
			return fmt.Errorf("%w: %s already has an account", ErrConflict, inv.Email)
		}
		if err != nil {
			return err
		}
		if err := q.MarkInviteAccepted(ctx, inv.ID); err != nil {
			return err
		}
		return audit(ctx, q, &op.ID, "invite.accepted", op.Email, "joined as "+op.Role)
	})
	if err != nil {
		return Operator{}, err
	}
	return fromRow(op), nil
}

// Login checks credentials. Unknown emails take as long as wrong passwords.
func (s *Service) Login(ctx context.Context, email, password string) (Operator, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	row, err := s.q.GetOperatorByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return Operator{}, ErrBadCredentials
	}
	if err != nil {
		return Operator{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)) != nil || row.Status != StatusActive {
		_ = audit(ctx, s.q, &row.ID, "login.failed", row.Email, "")
		return Operator{}, ErrBadCredentials
	}
	if err := s.q.TouchOperatorLogin(ctx, row.ID); err != nil {
		return Operator{}, err
	}
	_ = audit(ctx, s.q, &row.ID, "login", row.Email, "")
	return fromRow(row), nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Operator, error) {
	row, err := s.q.GetOperator(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Operator{}, ErrNotFound
	}
	if err != nil {
		return Operator{}, err
	}
	return fromRow(row), nil
}

func (s *Service) List(ctx context.Context) ([]Operator, []Invite, error) {
	rows, err := s.q.ListOperators(ctx)
	if err != nil {
		return nil, nil, err
	}
	ops := make([]Operator, 0, len(rows))
	for _, r := range rows {
		ops = append(ops, fromRow(r))
	}
	invRows, err := s.q.ListOpenInvites(ctx)
	if err != nil {
		return nil, nil, err
	}
	invites := make([]Invite, 0, len(invRows))
	for _, r := range invRows {
		invites = append(invites, Invite{ID: r.ID, Email: r.Email, Role: r.Role, InvitedBy: ptr(r.InvitedBy), InvitedByName: r.InvitedByName, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt})
	}
	return ops, invites, nil
}

func (s *Service) Events(ctx context.Context, limit int32) ([]Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.q.ListOperatorEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{ID: r.ID, ActorID: ptr(r.ActorID), ActorName: r.ActorName, Action: r.Action, Target: r.Target, Detail: r.Detail, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// SetAccess changes an operator's role and/or status. Existing sessions are
// invalidated (session_version bump). The last active admin cannot be
// demoted or disabled, and nobody can change their own access.
func (s *Service) SetAccess(ctx context.Context, actor *uuid.UUID, id uuid.UUID, role, status string) (Operator, error) {
	if role != RoleAdmin && role != RoleViewer {
		return Operator{}, fmt.Errorf("%w: role must be admin or viewer", ErrInvalid)
	}
	if status != StatusActive && status != StatusDisabled {
		return Operator{}, fmt.Errorf("%w: status must be active or disabled", ErrInvalid)
	}
	if actor != nil && *actor == id {
		return Operator{}, fmt.Errorf("%w: you cannot change your own access", ErrInvalid)
	}
	var out store.Operator
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		admins, err := q.LockActiveAdmins(ctx)
		if err != nil {
			return err
		}
		cur, err := q.GetOperatorForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.Role == role && cur.Status == status {
			out = cur
			return nil
		}
		wasActiveAdmin := cur.Role == RoleAdmin && cur.Status == StatusActive
		staysActiveAdmin := role == RoleAdmin && status == StatusActive
		if wasActiveAdmin && !staysActiveAdmin && len(admins) <= 1 {
			return ErrLastAdmin
		}
		out, err = q.UpdateOperatorAccess(ctx, store.UpdateOperatorAccessParams{ID: id, Role: role, Status: status})
		if err != nil {
			return err
		}
		return audit(ctx, q, actor, "access.changed", cur.Email, fmt.Sprintf("%s/%s → %s/%s", cur.Role, cur.Status, role, status))
	})
	if err != nil {
		return Operator{}, err
	}
	return fromRow(out), nil
}

func (s *Service) RevokeInvite(ctx context.Context, actor *uuid.UUID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inv, err := q.RevokeInvite(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return audit(ctx, q, actor, "invite.revoked", inv.Email, "")
	})
}

func (s *Service) Count(ctx context.Context) (int64, error) { return s.q.CountOperators(ctx) }

// Record adds an entry to the audit trail for actions taken elsewhere
// (e.g. issuing or revoking a merchant's API key).
func (s *Service) Record(ctx context.Context, actor *uuid.UUID, action, target, detail string) error {
	return audit(ctx, s.q, actor, action, target, detail)
}

func audit(ctx context.Context, q *store.Queries, actor *uuid.UUID, action, target, detail string) error {
	return q.InsertOperatorEvent(ctx, store.InsertOperatorEventParams{ActorID: nullable(actor), Action: action, Target: target, Detail: detail})
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", fmt.Errorf("%w: %q is not a valid email address", ErrInvalid, email)
	}
	return email, nil
}

func checkPassword(p string) error {
	if utf8.RuneCountInString(p) < MinPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, MinPasswordLength)
	}
	if len(p) > 72 { // bcrypt's limit
		return fmt.Errorf("%w: password must be at most 72 bytes", ErrInvalid)
	}
	return nil
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func nullable(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func ptr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	return &n.UUID
}

func fromRow(r store.Operator) Operator {
	return Operator{ID: r.ID, Email: r.Email, Name: r.Name, Role: r.Role, Status: r.Status, SessionVersion: r.SessionVersion, CreatedAt: r.CreatedAt, LastLoginAt: r.LastLoginAt}
}
