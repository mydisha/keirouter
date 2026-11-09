package store

import (
	"context"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/config"
)

// BenchmarkSQLitePool_ConcurrentReads measures short point reads under more
// concurrency than the idle cap, where connections beyond it are closed on
// release and reopened on demand. "idle=8" is the previous pool behaviour.
func BenchmarkSQLitePool_ConcurrentReads(b *testing.B) {
	for _, bc := range []struct {
		name    string
		maxIdle int
	}{
		{"idle=2_default", 0},
		{"idle=8_previous", 8},
	} {
		b.Run(bc.name, func(b *testing.B) {
			ctx := context.Background()
			db, err := Open(ctx, config.DatabaseConfig{
				Driver: "sqlite", DSN: b.TempDir() + "/bench.db", MaxIdleConns: bc.maxIdle,
			}, b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(ctx); err != nil {
				b.Fatal(err)
			}
			if err := db.Settings().Set(ctx, "bench", "v"); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetParallelism(25)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var v string
				for pb.Next() {
					if err := db.SQL().QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", "bench").Scan(&v); err != nil {
						b.Error(err)
						return
					}
				}
			})
			b.StopTimer()
			b.ReportMetric(float64(db.SQL().Stats().MaxIdleClosed), "idle-closed")
		})
	}
}
