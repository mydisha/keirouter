package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func seedRoutingAccounts(t *testing.T, db *DB, ids ...string) {
	t.Helper()
	for _, id := range ids {
		require.NoError(t, db.Accounts().Create(context.Background(), Account{
			ID: id, TenantID: DefaultTenantID, Provider: "openai", Label: id, AuthKind: AuthAPIKey,
			SecretWrappedDEK: "dek", SecretCiphertext: "ct", Metadata: "{}", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}))
	}
}

func TestCooldownReadsSeeOwnWritesImmediately(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedRoutingAccounts(t, db, "a1", "a2")
	ids := []string{"a1", "a2"}
	routing := db.Routing()

	got, err := routing.ActiveCooldownExpirations(ctx, ids, "gpt-4o")
	require.NoError(t, err)
	require.Empty(t, got) // cached empty result

	until := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	require.NoError(t, routing.SetModelCooldown(ctx, "a1", "gpt-4o", until))
	got, err = routing.ActiveCooldownExpirations(ctx, ids, "gpt-4o")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got["a1"].Equal(until))

	// Mutating the returned map must not leak into the cache.
	delete(got, "a1")
	got, _ = routing.ActiveCooldownExpirations(ctx, ids, "gpt-4o")
	require.Len(t, got, 1)

	require.NoError(t, routing.ClearModelCooldown(ctx, "a1", "gpt-4o"))
	got, _ = routing.ActiveCooldownExpirations(ctx, ids, "gpt-4o")
	require.Empty(t, got)

	require.NoError(t, routing.SetModelCooldown(ctx, "a2", "__all__", until))
	got, _ = routing.ActiveCooldownExpirations(ctx, ids, "gpt-4o")
	require.Contains(t, got, "a2")
	require.NoError(t, routing.ClearAccountModelCooldowns(ctx, "a2"))
	got, _ = routing.ActiveCooldownExpirations(ctx, ids, "gpt-4o")
	require.Empty(t, got)
}

func TestCooldownExpiryIsRecheckedOnCacheHit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedRoutingAccounts(t, db, "a1")
	// Stored times have second precision, so pick a cooldown that ends at the
	// next whole second and then wait past it while the entry is still cached.
	until := time.Now().Add(time.Second).UTC().Truncate(time.Second)
	if time.Until(until) < 100*time.Millisecond {
		until = until.Add(time.Second)
	}
	require.NoError(t, db.Routing().SetModelCooldown(ctx, "a1", "m", until))
	got, err := db.Routing().ActiveCooldownExpirations(ctx, []string{"a1"}, "m")
	require.NoError(t, err)
	require.Contains(t, got, "a1")

	db.cooldownCache.mu.Lock()
	for k, e := range db.cooldownCache.entries {
		e.expires = time.Now().Add(time.Hour) // keep the entry cached
		db.cooldownCache.entries[k] = e
	}
	db.cooldownCache.mu.Unlock()
	time.Sleep(time.Until(until) + 20*time.Millisecond)

	got, err = db.Routing().ActiveCooldownExpirations(ctx, []string{"a1"}, "m")
	require.NoError(t, err)
	require.Empty(t, got, "a cooldown that has ended must not block the account while cached")
}

func TestUnhealthyReadsSeeOwnWritesImmediately(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedRoutingAccounts(t, db, "a1", "a2")
	health := db.Health()
	ids := []string{"a1", "a2"}

	got, err := health.UnhealthyAccounts(ctx, ids, "m")
	require.NoError(t, err)
	require.Empty(t, got)

	now := time.Now()
	require.NoError(t, health.Upsert(ctx, AccountHealth{
		ID: "h1", TenantID: DefaultTenantID, AccountID: "a1", Provider: "openai", Model: "m",
		Status: "unhealthy", LastCheckedAt: now, UpdatedAt: now,
	}))
	got, _ = health.UnhealthyAccounts(ctx, ids, "m")
	require.Equal(t, map[string]bool{"a1": true}, got)

	got["a2"] = true // caller mutation stays local
	got, _ = health.UnhealthyAccounts(ctx, ids, "m")
	require.Equal(t, map[string]bool{"a1": true}, got)

	require.NoError(t, health.MarkHealthy(ctx, "a1", "m"))
	got, _ = health.UnhealthyAccounts(ctx, ids, "m")
	require.Empty(t, got)
}

func TestRoutingReadsBoundStalenessForForeignWrites(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedRoutingAccounts(t, db, "a1")
	_, err := db.Health().UnhealthyAccounts(ctx, []string{"a1"}, "m")
	require.NoError(t, err)

	// Another replica marks a1 unhealthy; this process cannot see the write.
	_, err = db.sql.ExecContext(ctx, db.rebind(`INSERT INTO account_health (`+accountHealthColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, 0, 0, 0, NULL, ?, '', ?)`),
		"h1", DefaultTenantID, "a1", "openai", "m", "unhealthy", formatTime(time.Now()), formatTime(time.Now()))
	require.NoError(t, err)

	db.unhealthyCache.mu.Lock()
	for k, e := range db.unhealthyCache.entries {
		e.expires = time.Now().Add(-time.Millisecond) // the TTL has passed
		db.unhealthyCache.entries[k] = e
	}
	db.unhealthyCache.mu.Unlock()
	got, err := db.Health().UnhealthyAccounts(ctx, []string{"a1"}, "m")
	require.NoError(t, err)
	require.True(t, got["a1"])
}
