package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// Bootstrap creates an initial API key for local use and returns its plaintext.
// It is invoked by `keirouter -bootstrap` so a fresh install is immediately
// usable without a dashboard. The plaintext is shown once and never recoverable.
func Bootstrap(ctx context.Context, cfg config.Config, name string) (string, error) {
	dataDir, err := resolveDataDir(cfg)
	if err != nil {
		return "", err
	}

	db, err := store.Open(ctx, cfg.Database, dataDir)
	if err != nil {
		return "", err
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		return "", fmt.Errorf("bootstrap: migrate: %w", err)
	}
	if err := db.Tenants().EnsureDefault(ctx); err != nil {
		return "", fmt.Errorf("bootstrap: ensure tenant: %w", err)
	}

	// The key is indexed under the same master key the server loads, so it
	// authenticates without a fallback on the server's first request.
	masterKey, err := loadOrCreateMasterKey(cfg, dataDir, slog.Default())
	if err != nil {
		return "", fmt.Errorf("bootstrap: master key: %w", err)
	}

	if name == "" {
		name = "default"
	}
	issued, err := identity.New(db.APIKeys(), crypto.NewKeyLookup(masterKey)).Create(ctx, store.DefaultTenantID, "", name)
	if err != nil {
		return "", fmt.Errorf("bootstrap: create key: %w", err)
	}
	return issued.Plaintext, nil
}