package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/stretchr/testify/require"
)

// newTestDB opens a migrated in-memory SQLite database for a single test.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// InsertIfAbsentOnTx inserts a settings row inside a transaction and reports
// ErrAlreadyExists when the key is taken, unlike Set's upsert. This is the
// exclusive lock that makes bansos create atomic.
func TestSettingsRepo_InsertIfAbsentOnTx(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Commit a first insert.
	tx := mustTx(t, db)
	require.NoError(t, db.Settings().InsertIfAbsentOnTx(ctx, tx, "lock", "v1"))
	require.NoError(t, tx.Commit())

	got, err := db.Settings().Get(ctx, "lock")
	require.NoError(t, err)
	require.Equal(t, "v1", got)

	// A second insert on the taken key errors with the sentinel; roll back so
	// the write lock is released before the next statement.
	tx2 := mustTx(t, db)
	require.ErrorIs(t, db.Settings().InsertIfAbsentOnTx(ctx, tx2, "lock", "v2"), ErrAlreadyExists)
	require.NoError(t, tx2.Rollback())

	// The original value is untouched.
	got, err = db.Settings().Get(ctx, "lock")
	require.NoError(t, err)
	require.Equal(t, "v1", got)

	// A rolled-back insert does not reserve the key.
	tx3 := mustTx(t, db)
	require.NoError(t, db.Settings().InsertIfAbsentOnTx(ctx, tx3, "fresh", "v"))
	require.NoError(t, tx3.Rollback())
	_, err = db.Settings().Get(ctx, "fresh")
	require.ErrorIs(t, err, ErrNotFound)
}

func mustTx(t *testing.T, db *DB) *sql.Tx {
	t.Helper()
	tx, err := db.sql.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestMigrate_Idempotent(t *testing.T) {
	db := newTestDB(t)
	// Running again must be a no-op, not an error.
	require.NoError(t, db.Migrate(context.Background()))
}

// TestMigrate_ToleratesPreexistingAddColumn reproduces the renamed-migration
// hazard: a migration file was renumbered after being applied to a database, so
// its new version is not recorded in schema_migrations and re-runs, but the
// column it adds is already present. runMigration must treat the duplicate
// "ADD COLUMN" as already-satisfied instead of aborting startup.
func TestMigrate_ToleratesPreexistingAddColumn(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Run the real migrations to get a fully-migrated schema.
	require.NoError(t, db.Migrate(ctx))

	// Simulate the orphaned state: forget that a column-adding migration ran
	// while leaving its column in place. Re-running the migration would then
	// attempt to ADD an already-existing column.
	_, err = db.sql.ExecContext(ctx,
		"DELETE FROM schema_migrations WHERE version = '0022_headroom_ponytail_savings'")
	require.NoError(t, err)

	// Migrate again: the duplicate ADD COLUMN must be tolerated, not fatal.
	require.NoError(t, db.Migrate(ctx))

	// And the migration is recorded again so future runs skip it.
	applied, err := db.appliedVersions(ctx)
	require.NoError(t, err)
	_, ok := applied["0022_headroom_ponytail_savings"]
	require.True(t, ok, "migration should be recorded after the tolerated re-run")
}

// TestMigrate_RealErrorsStillFail ensures the duplicate-column tolerance is
// tightly scoped: a genuinely broken statement still aborts the migration.
func TestMigrate_RealErrorsStillFail(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.sql.ExecContext(ctx, migrationsTable)
	require.NoError(t, err)

	err = db.runMigration(ctx, "9999_bad", "SELECT * FROM table_that_does_not_exist;")
	require.Error(t, err)
}

func TestAPIKeyRepo_CRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	k := APIKey{
		ID:         "key1",
		TenantID:   DefaultTenantID,
		Name:       "laptop",
		KeyHash:    "$argon2id$...",
		LookupHash: "abc123",
		Display:    "kr_AbC1…7xQ2",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, db.APIKeys().Create(ctx, k))

	got, err := db.APIKeys().FindByLookup(ctx, "abc123")
	require.NoError(t, err)
	require.Equal(t, "key1", got.ID)
	require.Equal(t, "laptop", got.Name)
	require.False(t, got.Disabled)

	_, err = db.APIKeys().FindByLookup(ctx, "missing")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, db.APIKeys().SetDisabled(ctx, "key1", true))
	got, err = db.APIKeys().FindByLookup(ctx, "abc123")
	require.NoError(t, err)
	require.True(t, got.Disabled)

	list, err := db.APIKeys().List(ctx, DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, db.APIKeys().Delete(ctx, "key1"))
	_, err = db.APIKeys().FindByLookup(ctx, "abc123")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAccountRepo_CRUDAndCooldown(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	a := Account{
		ID:               "acc1",
		TenantID:         DefaultTenantID,
		Provider:         "openai",
		Label:            "primary",
		AuthKind:         AuthAPIKey,
		SecretWrappedDEK: "dek",
		SecretCiphertext: "ct",
		Metadata:         "{}",
		Priority:         10,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	require.NoError(t, db.Accounts().Create(ctx, a))

	got, err := db.Accounts().Get(ctx, "acc1")
	require.NoError(t, err)
	require.Equal(t, "openai", got.Provider)
	require.Equal(t, "ct", got.SecretCiphertext)
	require.Nil(t, got.CooldownUntil)

	until := time.Now().Add(5 * time.Minute)
	require.NoError(t, db.Accounts().SetCooldown(ctx, "acc1", until))
	got, err = db.Accounts().Get(ctx, "acc1")
	require.NoError(t, err)
	require.NotNil(t, got.CooldownUntil)

	list, err := db.Accounts().ListByProvider(ctx, DefaultTenantID, "openai")
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, db.Accounts().Delete(ctx, "acc1"))
	_, err = db.Accounts().Get(ctx, "acc1")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestUsageRepo_RecordAndSummarize(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Usage().Record(ctx, UsageRecord{
			ID:               "u" + time.Now().Format("150405.000000000"),
			TenantID:         DefaultTenantID,
			APIKeyID:         "key1",
			Provider:         "openai",
			Model:            "gpt-4o",
			PromptTokens:     100,
			CompletionTokens: 50,
			CostMicros:       1500,
			CreatedAt:        now,
		}))
		time.Sleep(time.Millisecond)
	}

	sum, err := db.Usage().Summarize(ctx, DefaultTenantID, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(3), sum.TotalRequests)
	require.Equal(t, int64(300), sum.PromptTokens)
	require.Equal(t, int64(4500), sum.CostMicros)

	spend, err := db.Usage().SpendSince(ctx, ScopeAPIKey, "key1", now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(4500), spend)
}

func TestUsageRepo_SavingsByClient(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now()

	records := []UsageRecord{
		// claude-code: two requests with RTK savings, one also has caveman.
		{Client: "claude-code", SlimBytesSaved: 4000, SlimTokensSaved: 1000, SlimRules: "git-diff"},
		{Client: "claude-code", SlimBytesSaved: 2000, SlimTokensSaved: 500, SlimRules: "grep", CavemanActive: true},
		// codex: one request with RTK savings.
		{Client: "codex", SlimBytesSaved: 800, SlimTokensSaved: 200, SlimRules: "json"},
		// no client detected -> grouped under "unknown".
		{Client: "", SlimBytesSaved: 400, SlimTokensSaved: 100, SlimRules: "log"},
		// a client that produced no optimization at all.
		{Client: "curl-script"},
	}
	for i, rec := range records {
		rec.ID = "u" + time.Now().Format("150405.000000000") + string(rune('a'+i))
		rec.TenantID = DefaultTenantID
		rec.Provider = "openai"
		rec.Model = "gpt-4o"
		rec.CreatedAt = now
		require.NoError(t, db.Usage().Record(ctx, rec))
		time.Sleep(time.Millisecond)
	}

	savings, err := db.Usage().SavingsByClient(ctx, DefaultTenantID, now.Add(-time.Hour))
	require.NoError(t, err)

	byClient := map[string]ClientSavings{}
	for _, s := range savings {
		byClient[s.Client] = s
	}

	// claude-code aggregates both of its rows.
	cc := byClient["claude-code"]
	require.Equal(t, int64(2), cc.Requests)
	require.Equal(t, int64(6000), cc.SlimBytesSaved)
	require.Equal(t, int64(1500), cc.SlimTokensSaved)
	require.Equal(t, int64(1), cc.CavemanRequests)

	require.Equal(t, int64(200), byClient["codex"].SlimTokensSaved)

	// Empty client is reported under "unknown".
	require.Equal(t, int64(100), byClient["unknown"].SlimTokensSaved)

	// Ordered by tokens saved descending: claude-code first.
	require.NotEmpty(t, savings)
	require.Equal(t, "claude-code", savings[0].Client)
}

func TestSettingsRepo_GetSet(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	_, err := db.Settings().Get(ctx, "theme")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, db.Settings().Set(ctx, "theme", "dark"))
	v, err := db.Settings().Get(ctx, "theme")
	require.NoError(t, err)
	require.Equal(t, "dark", v)

	require.NoError(t, db.Settings().Set(ctx, "theme", "light"))
	v, err = db.Settings().Get(ctx, "theme")
	require.NoError(t, err)
	require.Equal(t, "light", v)
}

func TestRoutingRepo_AccountAffinity(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	affinity := AccountAffinity{
		ScopeKey:  "tenant/provider/model/thread",
		AccountID: "acc-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, db.Routing().SetAccountAffinity(ctx, affinity))

	got, err := db.Routing().GetAccountAffinity(ctx, affinity.ScopeKey)
	require.NoError(t, err)
	require.Equal(t, "acc-1", got.AccountID)
	require.True(t, got.ExpiresAt.After(time.Now()))

	affinity.AccountID = "acc-2"
	affinity.ExpiresAt = time.Now().Add(-time.Minute)
	require.NoError(t, db.Routing().SetAccountAffinity(ctx, affinity))
	deleted, err := db.Routing().ExpireAccountAffinities(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	got, err = db.Routing().GetAccountAffinity(ctx, affinity.ScopeKey)
	require.NoError(t, err)
	require.Empty(t, got.AccountID)
}

func TestAuditRepo_AppendAndList(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		require.NoError(t, db.Audit().Append(ctx, AuditEntry{
			ID:        "a" + time.Now().Format("150405.000000000"),
			TenantID:  DefaultTenantID,
			Actor:     "key1",
			Action:    "proxy.request",
			Detail:    "{}",
			CreatedAt: time.Now(),
		}))
		time.Sleep(time.Millisecond)
	}

	entries, err := db.Audit().List(ctx, DefaultTenantID, 10)
	require.NoError(t, err)
	require.Len(t, entries, 5)
	require.Equal(t, "proxy.request", entries[0].Action)
}
