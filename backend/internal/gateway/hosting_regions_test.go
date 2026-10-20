package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// regionTestServer has Indonesia (one node, one slot) and Europe (one node,
// unlimited) on sale.
func regionTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, tok, _ := hostingTestServer(t)
	st, err := hosting.Load(t.Context(), s.settingsGet(), s.financeBox())
	require.NoError(t, err)
	st.Regions = []hosting.Region{
		{ID: "id", Name: "Indonesia", PriceIDR: 20000, Enabled: true},
		{ID: "eu", Name: "Europe", PriceIDR: 10000, Enabled: true},
	}
	st.Nodes = []hosting.Node{
		{ID: "jkt", Name: "Jakarta", PublicIP: "203.0.113.10", Capacity: 1, Enabled: true, Region: "id"},
		{ID: "fra", Name: "Frankfurt", PublicIP: "203.0.113.20", Enabled: true, Region: "eu"},
	}
	require.NoError(t, hosting.Save(t.Context(), s.settingsSet(), s.financeBox(), st))
	return s, tok
}

func regionOrder(region string) string {
	return `{"accept_terms":true,"terms_version":"` + hosting.TermsVersion + `","region":"` + region + `"}`
}

func publicHostingJSON(t *testing.T, s *Server) (string, map[string]map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.publicHosting(rec, httptest.NewRequest(http.MethodGet, "/v1/public/hosting", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Regions []map[string]any `json:"regions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	byID := map[string]map[string]any{}
	for _, r := range out.Regions {
		byID[r["id"].(string)] = r
	}
	return rec.Body.String(), byID
}

func TestHostingRegions_PriceStockAndSoldOut(t *testing.T) {
	s, tok := regionTestServer(t)
	ctx := t.Context()

	body, regions := publicHostingJSON(t, s)
	require.NotContains(t, body, "jkt.example.com", "node domains stay private")
	require.NotContains(t, body, "Jakarta", "node names stay private")
	require.EqualValues(t, 20000, regions["id"]["price_idr"])
	require.EqualValues(t, 1, regions["id"]["stock"])
	require.EqualValues(t, -1, regions["eu"]["stock"])
	require.Equal(t, false, regions["id"]["sold_out"])

	// Europe is priced per region.
	rec := postHostingOrder(t, s, tok, regionOrder("eu"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var eu map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &eu))
	require.EqualValues(t, 10000, eu["amount_idr"])

	// Indonesia: the only slot is taken once someone pays.
	rec = postHostingOrder(t, s, tok, regionOrder("id"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.EqualValues(t, 20000, out["amount_idr"])
	require.NotEqual(t, eu["instance_id"], out["instance_id"], "each region gets its own instance")
	order, err := s.db.PaymentOrders().Get(ctx, out["order_id"].(string))
	require.NoError(t, err)
	inst, err := s.db.Hosting().Get(ctx, order.HostingInstanceID)
	require.NoError(t, err)
	require.Equal(t, "id", inst.Region)
	_, err = s.markHostingOrderPaid(ctx, order, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)

	_, regions = publicHostingJSON(t, s)
	require.EqualValues(t, 0, regions["id"]["stock"])
	require.Equal(t, true, regions["id"]["sold_out"])

	tok2 := addPortalUser(t, s, "second")
	rec = postHostingOrder(t, s, tok2, regionOrder("id"))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "region_sold_out")
	require.Equal(t, http.StatusBadRequest, postHostingOrder(t, s, tok2, regionOrder("mars")).Code)

	// A paid instance can still be renewed while its region is sold out.
	rec = postHostingOrder(t, s, tok, `{"accept_terms":true,"terms_version":"`+hosting.TermsVersion+`","instance_id":"`+inst.ID+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestHostingRegions_ProvisionStaysInRegionAndOvercommits(t *testing.T) {
	s, _ := regionTestServer(t)
	ctx := t.Context()
	mk := func(id string) {
		require.NoError(t, s.db.Hosting().Create(ctx, store.HostingInstance{
			ID: id, TenantID: adminTenant, KeyID: "k-" + id, Region: "id", Status: store.HostingPaid, PrepaidDays: 30,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}))
	}
	mk("a")
	mk("b")
	prov := func(id, body string) *httptest.ResponseRecorder {
		return adminHostingCall(s, http.MethodPost, "/x", id, body, s.adminProvisionHosting)
	}

	rec := prov("a", `{"image":"luqmenul/9router-go:latest"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	a, err := s.db.Hosting().Get(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "jkt", a.NodeID, "placed in its own region, not on the unlimited Europe node")

	// Jakarta is now full: refused, unless forced.
	rec = prov("b", `{"image":"luqmenul/9router-go:latest"}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	rec = prov("b", `{"image":"luqmenul/9router-go:latest","node_id":"jkt"}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "overcommit")
	rec = prov("b", `{"image":"luqmenul/9router-go:latest","overcommit":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	b, err := s.db.Hosting().Get(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, "jkt", b.NodeID)
	require.Equal(t, store.HostingActive, b.Status)
}
