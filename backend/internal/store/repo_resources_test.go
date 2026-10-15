package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResourceBucketsAggregates(t *testing.T) {
	db := newTestDB(t)
	repo := db.Resources()
	ctx := context.Background()

	base := time.Now().Add(-10 * time.Minute).Truncate(time.Minute)
	const gib = 1 << 30
	rows := []struct {
		off              time.Duration
		hostCPU, procCPU float64
		memUsed          int64
		sent             int64
	}{
		{0, 20, 2, 4 * gib, 1000},
		{15 * time.Second, 60, 4, 6 * gib, 1500},
		{30 * time.Second, 40, 3, 5 * gib, 4000},
	}
	for _, r := range rows {
		require.NoError(t, repo.InsertResourceSample(ctx, ResourceSample{
			TenantID:          "default",
			CreatedAt:         base.Add(r.off),
			HostCPUPercent:    r.hostCPU,
			ProcCPUPercent:    r.procCPU,
			HostMemUsedBytes:  r.memUsed,
			HostMemTotalBytes: 8 * gib,
			HostNetSentBytes:  r.sent,
		}))
	}

	buckets, err := repo.ResourceBuckets(ctx, base.Add(-time.Minute), time.Minute)
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	b := buckets[0]
	require.Equal(t, int(base.Unix()), b.Bucket)
	require.Equal(t, 3, b.Samples)
	require.InDelta(t, 40, b.HostCPUAvg, 0.01)
	require.InDelta(t, 60, b.HostCPUMax, 0.01)
	require.InDelta(t, 3, b.ProcCPUAvg, 0.01)
	require.InDelta(t, 62.5, b.HostMemPctAvg, 0.01)
	require.InDelta(t, 75, b.HostMemPctMax, 0.01)
	require.Equal(t, int64(3000), b.NetSentDelta)
}
