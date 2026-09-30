package auth

import (
	"context"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func newTestAuth(t *testing.T) (*Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })

	svc := New(db.Settings(), "", time.Hour)
	seeded, err := svc.EnsureDefaults(ctx)
	require.NoError(t, err)
	require.True(t, seeded, "first run must seed the default password")
	return svc, ctx
}

func TestEnsureDefaults_SeedsOnceThenIdempotent(t *testing.T) {
	svc, ctx := newTestAuth(t)
	// Second call must not re-seed.
	seeded, err := svc.EnsureDefaults(ctx)
	require.NoError(t, err)
	require.False(t, seeded)
}

func TestDefaultPasswordVerifies(t *testing.T) {
	svc, ctx := newTestAuth(t)
	ok, err := svc.VerifyPassword(ctx, DefaultPassword)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, svc.UsingDefaultPassword(ctx))
}

func TestSetPassword(t *testing.T) {
	svc, ctx := newTestAuth(t)

	require.Error(t, svc.SetPassword(ctx, "short"), "must reject short passwords")

	require.NoError(t, svc.SetPassword(ctx, "a-strong-password"))
	require.False(t, svc.UsingDefaultPassword(ctx), "default no longer in use")

	ok, err := svc.VerifyPassword(ctx, "a-strong-password")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = svc.VerifyPassword(ctx, DefaultPassword)
	require.NoError(t, err)
	require.False(t, ok, "old default must no longer verify")
}

func TestOnboardingFlow(t *testing.T) {
	svc, ctx := newTestAuth(t)
	require.False(t, svc.OnboardingComplete(ctx))
	require.NoError(t, svc.CompleteOnboarding(ctx))
	require.True(t, svc.OnboardingComplete(ctx))
}

func TestSession_IssueVerify(t *testing.T) {
	svc, _ := newTestAuth(t)
	token, err := svc.IssueSession()
	require.NoError(t, err)
	require.True(t, svc.VerifySession(token))

	require.False(t, svc.VerifySession("garbage"))
	require.False(t, svc.VerifySession("a.b"))
	require.False(t, svc.VerifySession(token+"tampered"))
}

func TestSession_Expiry(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })

	svc := New(db.Settings(), "", -time.Hour) // already-expired TTL clamps to default though
	_, err = svc.EnsureDefaults(ctx)
	require.NoError(t, err)
	// TTL <= 0 is clamped to 24h in New, so issue should still be valid.
	token, err := svc.IssueSession()
	require.NoError(t, err)
	require.True(t, svc.VerifySession(token))
}

func TestSession_SigningKeyPersistsAcrossInstances(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })

	svc1 := New(db.Settings(), "", time.Hour)
	_, err = svc1.EnsureDefaults(ctx)
	require.NoError(t, err)
	token, err := svc1.IssueSession()
	require.NoError(t, err)

	// A fresh service over the same store must accept tokens from the first,
	// because the signing key is persisted.
	svc2 := New(db.Settings(), "", time.Hour)
	_, err = svc2.EnsureDefaults(ctx)
	require.NoError(t, err)
	require.True(t, svc2.VerifySession(token))
}

func TestSessionAudienceIsolation(t *testing.T) {
	svc, _ := newTestAuth(t)

	admin, err := svc.IssueSession()
	require.NoError(t, err)
	portal, err := svc.IssueSessionFor("portal:sub-1")
	require.NoError(t, err)

	require.True(t, svc.VerifySession(admin), "admin session must verify as dashboard")
	require.False(t, svc.VerifySession(portal), "portal session must NOT verify as dashboard")
	require.True(t, svc.VerifySessionSub(portal, "portal:sub-1"))
	require.False(t, svc.VerifySessionSub(portal, "portal:other"))

	got, ok := svc.SessionSubject(portal)
	require.True(t, ok)
	require.Equal(t, "portal:sub-1", got)

	_, ok = svc.SessionSubject("garbage")
	require.False(t, ok)
}
