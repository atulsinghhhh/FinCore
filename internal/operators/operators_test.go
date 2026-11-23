package operators

import (
	"context"
	"flag"
	"log"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"fincore/internal/testutil"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	pool, cleanup, err := testutil.StartPostgres(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func svc(t *testing.T) *Service {
	t.Helper()
	if testPool == nil {
		t.Skip("integration test")
	}
	return NewService(testPool)
}

const pw = "correct horse battery"

func email() string { return "op-" + uuid.NewString()[:8] + "@example.com" }

// join invites and accepts in one step.
func join(t *testing.T, s *Service, actor *uuid.UUID, role string) Operator {
	t.Helper()
	_, token, err := s.Invite(context.Background(), actor, email(), role)
	require.NoError(t, err)
	op, err := s.AcceptInvite(context.Background(), token, "Test Operator", pw)
	require.NoError(t, err)
	return op
}

func TestInviteAcceptLogin(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	admin := join(t, s, nil, RoleAdmin)

	addr := email()
	inv, token, err := s.Invite(ctx, &admin.ID, "  "+addr+" ", RoleViewer)
	require.NoError(t, err)
	require.Equal(t, addr, inv.Email, "emails are trimmed and lower-cased")
	require.Len(t, token, 64)

	looked, err := s.LookupInvite(ctx, token)
	require.NoError(t, err)
	require.Equal(t, "Test Operator", *looked.InvitedByName)

	_, err = s.AcceptInvite(ctx, token, "Viewer", "short")
	require.ErrorIs(t, err, ErrInvalid, "password policy")

	op, err := s.AcceptInvite(ctx, token, "Vee Viewer", pw)
	require.NoError(t, err)
	require.Equal(t, RoleViewer, op.Role)

	_, err = s.AcceptInvite(ctx, token, "Again", pw)
	require.ErrorIs(t, err, ErrInviteInvalid, "an invite works once")
	_, err = s.LookupInvite(ctx, token)
	require.ErrorIs(t, err, ErrInviteInvalid)

	got, err := s.Login(ctx, addr, pw)
	require.NoError(t, err)
	require.Equal(t, op.ID, got.ID)
	_, err = s.Login(ctx, addr, "wrong password!!")
	require.ErrorIs(t, err, ErrBadCredentials)
	_, err = s.Login(ctx, "nobody@example.com", pw)
	require.ErrorIs(t, err, ErrBadCredentials, "unknown email looks the same as a wrong password")

	_, _, err = s.Invite(ctx, &admin.ID, addr, RoleAdmin)
	require.ErrorIs(t, err, ErrConflict, "cannot invite someone who already has an account")
}

func TestInviteValidation(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	_, _, err := s.Invite(ctx, nil, "not-an-email", RoleAdmin)
	require.ErrorIs(t, err, ErrInvalid)
	_, _, err = s.Invite(ctx, nil, email(), "owner")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = s.AcceptInvite(ctx, "deadbeef", "X", pw)
	require.ErrorIs(t, err, ErrInviteInvalid)
}

func TestReinviteRevokesPreviousLink(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	addr := email()
	_, first, err := s.Invite(ctx, nil, addr, RoleViewer)
	require.NoError(t, err)
	_, second, err := s.Invite(ctx, nil, addr, RoleAdmin)
	require.NoError(t, err)

	_, err = s.AcceptInvite(ctx, first, "Old link", pw)
	require.ErrorIs(t, err, ErrInviteInvalid)
	op, err := s.AcceptInvite(ctx, second, "New link", pw)
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, op.Role)
}

func TestExpiredAndRevokedInvites(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	inv, token, err := s.Invite(ctx, nil, email(), RoleViewer)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE operator_invites SET expires_at = now() - interval '1 minute' WHERE id = $1`, inv.ID)
	require.NoError(t, err)
	_, err = s.AcceptInvite(ctx, token, "Late", pw)
	require.ErrorIs(t, err, ErrInviteInvalid)

	inv2, token2, err := s.Invite(ctx, nil, email(), RoleViewer)
	require.NoError(t, err)
	require.NoError(t, s.RevokeInvite(ctx, nil, inv2.ID))
	_, err = s.AcceptInvite(ctx, token2, "Revoked", pw)
	require.ErrorIs(t, err, ErrInviteInvalid)
	require.ErrorIs(t, s.RevokeInvite(ctx, nil, inv2.ID), ErrNotFound)
}

func TestAccessChangesAndLastAdminGuard(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	// Start from a known state: disable every existing admin except ours.
	a := join(t, s, nil, RoleAdmin)
	_, err := testPool.Exec(ctx, `UPDATE operators SET status = 'disabled' WHERE role = 'admin' AND id <> $1`, a.ID)
	require.NoError(t, err)
	v := join(t, s, &a.ID, RoleViewer)

	_, err = s.SetAccess(ctx, &a.ID, a.ID, RoleViewer, StatusActive)
	require.ErrorIs(t, err, ErrInvalid, "nobody changes their own access")
	_, err = s.SetAccess(ctx, &v.ID, a.ID, RoleViewer, StatusActive)
	require.ErrorIs(t, err, ErrLastAdmin, "the only admin cannot be demoted")
	_, err = s.SetAccess(ctx, &v.ID, a.ID, RoleAdmin, StatusDisabled)
	require.ErrorIs(t, err, ErrLastAdmin, "or disabled")

	promoted, err := s.SetAccess(ctx, &a.ID, v.ID, RoleAdmin, StatusActive)
	require.NoError(t, err)
	require.Equal(t, v.SessionVersion+1, promoted.SessionVersion, "role change invalidates existing sessions")

	disabled, err := s.SetAccess(ctx, &v.ID, a.ID, RoleAdmin, StatusDisabled)
	require.NoError(t, err, "with two admins, one can be disabled")
	require.Equal(t, StatusDisabled, disabled.Status)
	_, err = s.Login(ctx, a.Email, pw)
	require.ErrorIs(t, err, ErrBadCredentials, "disabled accounts cannot sign in")

	events, err := s.Events(ctx, 20)
	require.NoError(t, err)
	var actions []string
	for _, e := range events {
		actions = append(actions, e.Action)
	}
	require.Contains(t, actions, "access.changed")
	require.Contains(t, actions, "invite.accepted")
	require.Contains(t, actions, "login.failed")
}

func TestConcurrentDemotionsKeepOneAdmin(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	a := join(t, s, nil, RoleAdmin)
	_, err := testPool.Exec(ctx, `UPDATE operators SET status = 'disabled' WHERE role = 'admin' AND id <> $1`, a.ID)
	require.NoError(t, err)
	b := join(t, s, &a.ID, RoleAdmin)

	// Each admin tries to demote the other at the same moment: only one wins.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Go(func() { _, errs[0] = s.SetAccess(ctx, &a.ID, b.ID, RoleViewer, StatusActive) })
	wg.Go(func() { _, errs[1] = s.SetAccess(ctx, &b.ID, a.ID, RoleViewer, StatusActive) })
	wg.Wait()

	var admins int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM operators WHERE role = 'admin' AND status = 'active'`).Scan(&admins))
	require.Equal(t, 1, admins)
	require.True(t, (errs[0] == nil) != (errs[1] == nil), "exactly one demotion succeeds: %v", errs)
}
