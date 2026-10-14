package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminChainMarkupOverrideTriState(t *testing.T) {
	s, db, cookie := newMarketPricingTestServer(t)
	s.chains = db.Chains()

	rec := marketRequest(t, s, cookie, http.MethodPost, "/api/chains",
		`{"name":"mk","steps":[{"provider":"openai","model":"gpt-4o"}],"markup_percent":25}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	markupOf := func() any {
		var list struct {
			Chains []map[string]any `json:"chains"`
		}
		require.NoError(t, json.Unmarshal(marketRequest(t, s, cookie, http.MethodGet, "/api/chains", "").Body.Bytes(), &list))
		return chainEntryByName(t, list.Chains, "mk")["markup_percent"]
	}
	require.Equal(t, 25.0, markupOf())

	// Absent field leaves the override untouched.
	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"strategy":"priority"}`).Code)
	require.Equal(t, 25.0, markupOf())

	// Explicit 0 disables markup for this chain only.
	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"markup_percent":0}`).Code)
	require.Equal(t, 0.0, markupOf())

	// null returns the chain to the global markup.
	require.Equal(t, http.StatusOK, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"markup_percent":null}`).Code)
	require.Nil(t, markupOf())

	require.Equal(t, http.StatusBadRequest, marketRequest(t, s, cookie, http.MethodPatch, "/api/chains/"+created.ID, `{"markup_percent":-1}`).Code)
	require.Equal(t, http.StatusBadRequest, marketRequest(t, s, cookie, http.MethodPost, "/api/chains",
		`{"name":"bad","steps":[{"provider":"openai","model":"gpt-4o"}],"markup_percent":5000}`).Code)
}
