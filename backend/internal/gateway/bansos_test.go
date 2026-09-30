package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

func newBansosTestServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)

	s := New(Deps{
		Config:   config.Default(),
		DB:       db,
		Identity: identity.New(db.APIKeys()),
		Budgets:  db.Budgets(),
		Usage:    db.Usage(),
		Settings: db.Settings(),
		Vault:    vault.New(sealer),
		Chains:   db.Chains(),
	})
	return s, db
}

func TestBansosConfigRoundTrip(t *testing.T) {
	s, _ := newBansosTestServer(t)
	ctx := context.Background()

	_, ok, err := s.loadBansos(ctx)
	require.NoError(t, err)
	require.False(t, ok, "unconfigured bansos must report not-configured")
	require.Empty(t, s.bansosKeyID(ctx))

	cfg := bansosConfig{
		KeyID: "key-1", PlanID: "plan-1", Active: true, Mode: bansosModeCredit,
		MaskedDisplay: "tkr_ab••••", AllowedModels: []string{"claude-*"},
		RPM: 60, TPM: 200000,
	}
	require.NoError(t, s.saveBansos(ctx, cfg))

	got, ok, err := s.loadBansos(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "key-1", got.KeyID)
	require.Equal(t, bansosModeCredit, got.Mode)
	require.True(t, got.Active)
	require.Equal(t, []string{"claude-*"}, got.AllowedModels)
	require.Equal(t, int64(60), got.RPM)
	require.Equal(t, "key-1", s.bansosKeyID(ctx))
}
