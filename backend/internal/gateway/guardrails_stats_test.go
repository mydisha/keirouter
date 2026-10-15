package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestAdminGuardrailStats(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Tenants().EnsureDefault(ctx))
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC().Add(-time.Minute)
	for i, action := range []string{"allow", "block", "mask", ""} {
		status := "success"
		if action == "block" {
			status = "blocked"
		}
		require.NoError(t, db.Usage().Record(ctx, store.UsageRecord{
			ID: string(rune('a' + i)), RequestID: string(rune('a' + i)), TenantID: store.DefaultTenantID,
			Provider: "openai", Model: "gpt-4o", Status: status, GuardrailAction: action, CreatedAt: now,
		}))
	}

	// Retention of 7 days makes a 30d window reach past the kept audit logs.
	s := &Server{db: db, insightsCache: newTTLCache(time.Second), guardrailRetention: 7}
	rec := httptest.NewRecorder()
	s.adminGuardrailStats(rec, httptest.NewRequest(http.MethodGet, "/api/guardrails/stats?period=30d", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Totals struct {
			Requests  int64   `json:"requests"`
			Evaluated int64   `json:"evaluated"`
			Blocked   int64   `json:"blocked"`
			Masked    int64   `json:"masked"`
			BlockRate float64 `json:"block_rate"`
		} `json:"totals"`
		DataQuality struct {
			LogsTruncated bool `json:"logs_truncated"`
		} `json:"data_quality"`
		Timeline []map[string]any `json:"timeline"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.EqualValues(t, 4, body.Totals.Requests)
	require.EqualValues(t, 3, body.Totals.Evaluated)
	require.EqualValues(t, 1, body.Totals.Blocked)
	require.EqualValues(t, 1, body.Totals.Masked)
	require.InDelta(t, 0.25, body.Totals.BlockRate, 1e-9)
	require.True(t, body.DataQuality.LogsTruncated)
	require.NotEmpty(t, body.Timeline)
}
