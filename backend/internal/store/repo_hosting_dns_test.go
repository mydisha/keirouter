package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHostingDNSRecordIDRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now()
	h := HostingInstance{ID: "h1", TenantID: "t", KeyID: "k", Status: HostingPaid, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, db.Hosting().Create(ctx, h))

	h.DNSRecordID = "rec-1"
	require.NoError(t, db.Hosting().SetDokploy(ctx, h))
	got, err := db.Hosting().Get(ctx, "h1")
	require.NoError(t, err)
	require.Equal(t, "rec-1", got.DNSRecordID)
}
