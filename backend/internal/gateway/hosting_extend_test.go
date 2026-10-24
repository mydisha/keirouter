package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestHostingExtend_AddsDaysPerStatus(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id string, st store.HostingStatus, until time.Time, provisioned bool) {
		h := store.HostingInstance{ID: id, TenantID: adminTenant, KeyID: "k", Status: st,
			PaidUntil: until, CreatedAt: now, UpdatedAt: now}
		if provisioned {
			h.ProvisionedAt = now.AddDate(0, 0, -40)
		} else {
			h.PrepaidDays = 30
		}
		require.NoError(t, s.db.Hosting().Create(ctx, h))
	}
	mk("active", store.HostingActive, now.AddDate(0, 0, 5), true)
	mk("expired", store.HostingExpired, now.Add(-time.Hour), true)
	mk("paid", store.HostingPaid, time.Time{}, false)
	mk("unpaid", store.HostingAwaitingPayment, time.Time{}, false)

	rec := adminHostingCall(s, http.MethodPost, "/x", "",
		`{"ids":["active","expired","paid","unpaid","missing","active"],"days":3}`, s.adminExtendHosting)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Updated []map[string]any `json:"updated"`
		Skipped []struct{ ID, Reason string }
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Updated, 3, "a repeated id is extended once")
	require.Len(t, out.Skipped, 2)

	get := func(id string) store.HostingInstance {
		h, err := s.db.Hosting().Get(ctx, id)
		require.NoError(t, err)
		return h
	}
	require.True(t, get("active").PaidUntil.Equal(now.AddDate(0, 0, 8)))
	exp := get("expired")
	require.Equal(t, store.HostingActive, exp.Status, "an end date back in the future reactivates it")
	require.True(t, exp.PaidUntil.Equal(now.Add(-time.Hour).AddDate(0, 0, 3)))
	paid := get("paid")
	require.Equal(t, 33, paid.PrepaidDays, "unprovisioned days start at provisioning")
	require.True(t, paid.PaidUntil.IsZero())
	require.Equal(t, 30, get("unpaid").PrepaidDays, "an unpaid order gets nothing")
}

func TestHostingExtend_RejectsBadInput(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	for _, body := range []string{`{"ids":["a"],"days":0}`, `{"ids":["a"],"days":366}`, `{"ids":[],"days":1}`} {
		rec := adminHostingCall(s, http.MethodPost, "/x", "", body, s.adminExtendHosting)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
}
