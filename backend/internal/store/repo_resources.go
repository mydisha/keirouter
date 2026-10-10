package store

import (
	"context"
	"fmt"
	"time"
)

// ResourceRepo persists resource monitor samples.
type ResourceRepo struct{ db *DB }

// Resources returns the resource sample repository.
func (db *DB) Resources() *ResourceRepo { return &ResourceRepo{db: db} }

// InsertResourceSample writes a single resource sample row.
func (r *ResourceRepo) InsertResourceSample(ctx context.Context, s ResourceSample) error {
	q := r.db.rebind(`INSERT INTO resource_samples
		(tenant_id, created_at,
		 goroutines, heap_alloc_bytes, heap_sys_bytes, gc_pause_ns, next_gc_bytes, num_gc,
		 proc_cpu_percent, proc_rss_bytes, proc_threads, proc_open_fds,
		 host_cpu_percent, host_mem_used_bytes, host_mem_total_bytes,
		 host_disk_used_bytes, host_disk_total_bytes,
		 host_net_sent_bytes, host_net_recv_bytes,
		 host_load1, host_load5, host_load15,
		 inflight_requests)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	_, err := r.db.sql.ExecContext(ctx, q,
		s.TenantID, formatTime(s.CreatedAt),
		s.Goroutines, s.HeapAllocBytes, s.HeapSysBytes, s.GCPauseNS, s.NextGCBytes, s.NumGC,
		s.ProcCPUPercent, s.ProcRSSBytes, s.ProcThreads, s.ProcOpenFDs,
		s.HostCPUPercent, s.HostMemUsedBytes, s.HostMemTotalBytes,
		s.HostDiskUsedBytes, s.HostDiskTotalBytes,
		s.HostNetSentBytes, s.HostNetRecvBytes,
		s.HostLoad1, s.HostLoad5, s.HostLoad15,
		s.InflightRequests)
	if err != nil {
		return fmt.Errorf("store: insert resource sample: %w", err)
	}
	return nil
}

// ResourceBuckets returns time-bucketed resource samples for the given time range.
// Each bucket spans the given interval (e.g. 5 minutes). Used by the frontend
// history chart to show long-term trends without loading every raw sample.
func (r *ResourceRepo) ResourceBuckets(ctx context.Context, since time.Time, interval time.Duration) ([]ResourceBucket, error) {
	bucketSecs := int64(interval.Seconds())
	epochCreated := r.db.epochExpr("created_at")
	q := r.db.rebind(fmt.Sprintf(`
		SELECT
			(CAST(%s AS BIGINT) / ? * ?) AS bucket,
			AVG(proc_cpu_percent), MAX(proc_cpu_percent),
			AVG(host_cpu_percent), MAX(host_cpu_percent),
			AVG(CAST(proc_rss_bytes AS REAL)) / 1048576.0, MAX(proc_rss_bytes),
			AVG(CAST(host_mem_used_bytes AS REAL)) / 1048576.0, MAX(host_mem_used_bytes),
			AVG(CAST(goroutines AS REAL)), MAX(goroutines),
			AVG(CAST(heap_alloc_bytes AS REAL)) / 1048576.0, MAX(heap_alloc_bytes),
			0, 0,
			AVG(CAST(gc_pause_ns AS REAL)) / 1e6, MAX(gc_pause_ns),
			AVG(CAST(inflight_requests AS REAL)), MAX(inflight_requests),
			COUNT(*)
		FROM resource_samples
		WHERE created_at >= ?
		GROUP BY bucket
		ORDER BY bucket ASC`, epochCreated))

	rows, err := r.db.sql.QueryContext(ctx, q, bucketSecs, bucketSecs, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("store: resource buckets: %w", err)
	}
	defer rows.Close()

	var out []ResourceBucket
	for rows.Next() {
		var (
			b     ResourceBucket
			_     int64 // bucket timestamp (unused, derived from Bucket)
			count int
		)
		if err := rows.Scan(
			&b.Bucket,
			&b.ProcCPUAvg, &b.ProcCPUMax,
			&b.HostCPUAvg, &b.HostCPUMax,
			&b.ProcRSSAvg, &b.ProcRSSMax,
			&b.HostMemUsedAvg, &b.HostMemUsedMax,
			&b.GoroutinesAvg, &b.GoroutinesMax,
			&b.HeapAllocAvg, &b.HeapAllocMax,
			&b.NetSentDelta, &b.NetRecvDelta,
			&b.GCPauseAvg, &b.GCPauseMax,
			&b.InflightAvg, &b.InflightMax,
			&count,
		); err != nil {
			return nil, fmt.Errorf("store: scan resource bucket: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PruneResourceSamples deletes samples older than the given age. Called
// periodically to enforce the 7-day retention window.
func (r *ResourceRepo) PruneResourceSamples(ctx context.Context, maxAge time.Duration) error {
	cutoff := formatTime(time.Now().Add(-maxAge))
	q := r.db.rebind(`DELETE FROM resource_samples WHERE created_at < ?`)
	_, err := r.db.sql.ExecContext(ctx, q, cutoff)
	if err != nil {
		return fmt.Errorf("store: prune resource samples: %w", err)
	}
	return nil
}
