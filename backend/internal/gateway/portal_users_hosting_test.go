package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// The Portal users list shows each user's Dokploy applications: instances
// that exist in Dokploy and are not deleted.
func TestPortalUsersListHostingApps(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	ctx := t.Context()
	u, err := s.db.PortalUsers().GetBySub(ctx, "gsub")
	require.NoError(t, err)
	now := time.Now()
	mk := func(id, sub, appID string, st store.HostingStatus) {
		require.NoError(t, s.db.Hosting().Create(ctx, store.HostingInstance{
			ID: id, TenantID: adminTenant, KeyID: u.KeyID, Status: st, Subdomain: sub,
			FQDN: sub + ".host.example.com", DokployAppID: appID, CreatedAt: now, UpdatedAt: now,
		}))
	}
	mk("i1", "abcd1234", "app-1", store.HostingActive)
	mk("i2", "efgh5678", "app-2", store.HostingDeleted)
	mk("i3", "", "", store.HostingPaid)

	rec := httptest.NewRecorder()
	s.adminListPortalUsers(rec, httptest.NewRequest(http.MethodGet, "/api/portal-users", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Users []struct {
			GoogleSub   string           `json:"google_sub"`
			HostingApps []map[string]any `json:"hosting_apps"`
		} `json:"users"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.NotEmpty(t, out.Users)
	for _, pu := range out.Users {
		if pu.GoogleSub != "gsub" {
			continue
		}
		require.Len(t, pu.HostingApps, 1)
		require.Equal(t, "9router-abcd1234", pu.HostingApps[0]["name"])
		require.Equal(t, "abcd1234.host.example.com", pu.HostingApps[0]["fqdn"])
		require.Equal(t, "active", pu.HostingApps[0]["status"])
		return
	}
	t.Fatal("portal user gsub not listed")
}
