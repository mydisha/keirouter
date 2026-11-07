package identity

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func benchDB(b *testing.B) *store.DB {
	b.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	if err := db.Tenants().EnsureDefault(ctx); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}

func benchService(db *store.DB) *Service {
	return New(db.APIKeys(), crypto.NewKeyLookup(bytes.Repeat([]byte{1}, crypto.KeySize)))
}

// BenchmarkAuthenticateCold measures the first request of a key with an empty
// auth cache, as after a restart or cache eviction.
func BenchmarkAuthenticateCold(b *testing.B) {
	db := benchDB(b)
	ctx := context.Background()
	issued, err := benchService(db).Create(ctx, store.DefaultTenantID, "", "bench")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := benchService(db).Authenticate(ctx, issued.Plaintext); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAuthenticateColdDistinctKeys measures a burst of 32 distinct keys
// arriving at once on an empty cache, the case a restart under load produces.
func BenchmarkAuthenticateColdDistinctKeys(b *testing.B) {
	db := benchDB(b)
	ctx := context.Background()
	var keys []string
	for range 32 {
		issued, err := benchService(db).Create(ctx, store.DefaultTenantID, "", "bench")
		if err != nil {
			b.Fatal(err)
		}
		keys = append(keys, issued.Plaintext)
	}
	b.ReportAllocs()
	for b.Loop() {
		s := benchService(db)
		var wg sync.WaitGroup
		for _, k := range keys {
			wg.Go(func() {
				if _, err := s.Authenticate(ctx, k); err != nil {
					b.Error(err)
				}
			})
		}
		wg.Wait()
	}
}

// BenchmarkAuthenticateCached measures the steady state of an active key.
func BenchmarkAuthenticateCached(b *testing.B) {
	db := benchDB(b)
	ctx := context.Background()
	s := benchService(db)
	issued, err := s.Create(ctx, store.DefaultTenantID, "", "bench")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, issued.Plaintext); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Authenticate(ctx, issued.Plaintext); err != nil {
			b.Fatal(err)
		}
	}
}
