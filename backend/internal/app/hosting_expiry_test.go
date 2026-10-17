package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestHostingExpiryPass(t *testing.T) {
	ctx := context.Background()
	db := newSyncTestDB(t)
	a := newSyncTestApp(t, db)
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	a.financeBox = sealer

	var mu sync.Mutex
	calls := map[string]int{}
	dk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer dk.Close()
	require.NoError(t, hosting.Save(ctx, db.Settings().Set, sealer, hosting.Settings{DokployURL: dk.URL, APIKey: "k"}))

	now := time.Now()
	mk1 := func(id string, st store.HostingStatus, until time.Time, lastErr string, updated time.Time) {
		require.NoError(t, db.Hosting().Create(ctx, store.HostingInstance{
			ID: id, TenantID: "t", KeyID: "k", Status: st, DokployAppID: "app-" + id,
			PaidUntil: until, LastError: lastErr, CreatedAt: updated, UpdatedAt: updated,
		}))
	}
	mk1("fresh", store.HostingActive, now.Add(-time.Hour), "", now)                    // -> expired, still running
	mk1("past", store.HostingExpired, now.AddDate(0, 0, -4), "", now)                  // -> stopped
	mk1("renewed", store.HostingStopped, now.AddDate(0, 0, 20), autoStopReason, now)   // -> started
	mk1("manual", store.HostingStopped, now.AddDate(0, 0, 20), "", now)                // admin stop: untouched
	mk1("stuck", store.HostingProvisioning, time.Time{}, "", now.Add(-10*time.Minute)) // -> failed

	a.hostingExpiryPass(ctx, now)

	want := map[string]store.HostingStatus{
		"fresh": store.HostingExpired, "past": store.HostingStopped, "renewed": store.HostingActive,
		"manual": store.HostingStopped, "stuck": store.HostingFailed,
	}
	for id, st := range want {
		h, err := db.Hosting().Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, st, h.Status, id)
	}
	require.Equal(t, 1, calls["/api/application.stop"])
	require.Equal(t, 1, calls["/api/application.start"])
}
