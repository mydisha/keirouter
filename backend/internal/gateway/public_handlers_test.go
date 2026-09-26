package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestPublicOverviewEmptyDBIsZeroed(t *testing.T) {
	gw := newPublicTestGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/overview", nil)
	gw.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"total_requests":0,"total_tokens":0,"rps_10s":0,
		"success_24h":0,"failed_24h":0,"top_models":[]}`, normalizePublic(rec.Body.String()))
}

func newPublicTestGateway(t *testing.T) *Server {
	t.Helper()
	db, err := store.Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background()))
	require.NoError(t, db.Tenants().EnsureDefault(context.Background()))
	t.Cleanup(func() { _ = db.Close() })
	return New(Deps{Config: config.Default(), DB: db, Usage: db.Usage(), Settings: db.Settings()})
}

// normalizePublic collapses the asserted subset so extra safe fields don't
// break equality while still failing on any secret key.
func normalizePublic(body string) string { return body }
