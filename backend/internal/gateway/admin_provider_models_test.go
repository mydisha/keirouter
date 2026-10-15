package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/connectors"
)

// The batch endpoint is built on providerModelInfos; the per-provider endpoint
// must keep returning exactly what it did.
func TestProviderModelInfosMatchesSingleEndpoint(t *testing.T) {
	s, _ := newCustomProviderTestServer(t)
	s.liveModels = newLiveModelCache()

	var providerID string
	for _, p := range connectors.Catalog() {
		if !p.Hidden && connectors.GetLiveModelSource(p.ID) == nil && len(connectors.ModelsForProvider(p.ID)) > 0 {
			providerID = p.ID
			break
		}
	}
	require.NotEmpty(t, providerID, "need a static-catalog provider")

	rec := httptest.NewRecorder()
	s.adminProviderModels(rec, withChiID(http.MethodGet, "/providers/"+providerID+"/models", providerID))
	require.Equal(t, http.StatusOK, rec.Code)
	var single struct {
		Models []providerModelInfo `json:"models"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &single))

	infos, pending := s.providerModelInfos(context.Background(), providerID, "", false, batchLiveWait)
	require.False(t, pending)
	require.Equal(t, single.Models, infos)
	require.Len(t, infos, len(connectors.ModelsForProvider(providerID)))
}

func TestAdminProviderModelsRejectsUnknownKind(t *testing.T) {
	s, _ := newCustomProviderTestServer(t)
	rec := httptest.NewRecorder()
	s.adminAllProviderModels(rec, httptest.NewRequest(http.MethodGet, "/providers/models?kind=bogus", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
