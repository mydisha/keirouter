package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// newTopupPostgresServer builds a Server backed by the real Postgres test DSN.
// SQLite cannot exercise concurrent writers, so the race tests require this.
func newTopupPostgresServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("KEIROUTER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KEIROUTER_TEST_POSTGRES_DSN not set; skipping concurrency test")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "postgres", DSN: dsn}, "")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })
	return &Server{db: db, identity: identity.New(db.APIKeys()), budgets: db.Budgets(), usage: db.Usage(), log: slog.Default()}
}

// callTopup invokes the handler once in its own request context.
func callTopup(s *Server, keyID, body string) int {
	r := httptest.NewRequest(http.MethodPost, "/keys/"+keyID+"/topup", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", keyID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.adminTopupKey(w, r)
	return w.Code
}

func TestTopupRace_DistinctKeysAllApplyExactlyOnce(t *testing.T) {
	s := newTopupPostgresServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "race-distinct")
	require.NoError(t, err)
	keyID := issued.Record.ID
	budgetID := "b-race-1-" + keyID
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: budgetID, TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: keyID,
		LimitMicros: 0, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	const n = 25
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = callTopup(s, keyID, fmt.Sprintf(`{"amount_usd":1,"idempotency_key":"race-%d"}`, i))
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		require.Equal(t, http.StatusCreated, code, "top-up %d", i)
	}

	b, err := s.budgets.Get(ctx, budgetID)
	require.NoError(t, err)
	require.Equal(t, int64(n)*1_000_000, b.LimitMicros, "limit must equal n * amount")

	topups, err := s.db.Topups().ListByKey(ctx, keyID)
	require.NoError(t, err)
	require.Len(t, topups, n)

	// snapshots must form a gapless chain: sorted by before, each after == next before
	byBefore := make(map[int64]int64, n)
	for _, tp := range topups {
		require.Equal(t, tp.LimitBeforeMicros+tp.AmountMicros, tp.LimitAfterMicros)
		byBefore[tp.LimitBeforeMicros] = tp.LimitAfterMicros
	}
	prev := int64(0)
	for i := 0; i < n; i++ {
		next, ok := byBefore[prev]
		require.True(t, ok, "missing snapshot starting at %d", prev)
		prev = next
	}
	require.Equal(t, int64(n)*1_000_000, prev)
}

func TestTopupRace_SameIdempotencyKeyCreditsOnce(t *testing.T) {
	s := newTopupPostgresServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "race-same-idem")
	require.NoError(t, err)
	keyID := issued.Record.ID
	budgetID := "b-race-2-" + keyID
	require.NoError(t, s.budgets.Create(ctx, store.Budget{
		ID: budgetID, TenantID: adminTenant, ScopeKind: store.ScopeAPIKey, ScopeID: keyID,
		LimitMicros: 0, Period: "total", AlertPct: 80, HardCutoff: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	const n = 25
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = callTopup(s, keyID, `{"amount_usd":3,"idempotency_key":"same-idem"}`)
		}(i)
	}
	wg.Wait()

	b, err := s.budgets.Get(ctx, budgetID)
	require.NoError(t, err)
	require.Equal(t, int64(3_000_000), b.LimitMicros, "limit must be credited exactly once")

	topups, err := s.db.Topups().ListByKey(ctx, keyID)
	require.NoError(t, err)
	require.Len(t, topups, 1, "only one top-up row may exist")

	// exactly one request creates (201); the rest are replays or conflict-free 200
	created := 0
	for _, c := range codes {
		require.Contains(t, []int{http.StatusCreated, http.StatusOK}, c)
		if c == http.StatusCreated {
			created++
		}
	}
	require.Equal(t, 1, created)
}

func TestTopupRace_NoBudgetCreatesExactlyOne(t *testing.T) {
	s := newTopupPostgresServer(t)
	ctx := context.Background()
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "race-no-budget")
	require.NoError(t, err)
	keyID := issued.Record.ID

	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			callTopup(s, keyID, fmt.Sprintf(`{"amount_usd":1,"idempotency_key":"nb-%d"}`, i))
		}(i)
	}
	wg.Wait()

	bs, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, keyID)
	require.NoError(t, err)
	require.Len(t, bs, 1, "concurrent top-ups must not create duplicate budgets")
	require.Equal(t, int64(n)*1_000_000, bs[0].LimitMicros)

	var count int
	require.NoError(t, s.db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM key_topups WHERE key_id = $1", keyID).Scan(&count))
	require.Equal(t, n, count)
}
