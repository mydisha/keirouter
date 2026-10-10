package meter

import (
	"context"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestResolvePriceCanonicalMatchUsesFingerprintIndex(t *testing.T) {
	m := New(nil, nil, map[string]Price{
		"openai/gpt-4.1-mini": {InputPerM: 0.4, OutputPerM: 1.6, Source: "official"},
	})

	got := m.ResolvePrice("some-reseller", "GPT_4.1 mini")
	if got.MatchKind != "canonical_model" || got.Key != "openai/gpt-4.1-mini" {
		t.Fatalf("ResolvePrice() = %+v, want canonical match on openai/gpt-4.1-mini", got)
	}
}

func TestResolvePriceCanonicalConflictStaysMissing(t *testing.T) {
	m := New(nil, nil, map[string]Price{
		"a/model-x": {InputPerM: 1, OutputPerM: 2},
		"b/model.x": {InputPerM: 3, OutputPerM: 4},
	})

	if got := m.ResolvePrice("other", "modelx"); got.Status != "missing" {
		t.Fatalf("ResolvePrice() status = %q, want missing when catalog entries disagree", got.Status)
	}
}

func TestReplacePricesRebuildsFingerprintIndex(t *testing.T) {
	m := New(nil, nil, nil)
	if got := m.ResolvePrice("other", "claude-sonnet-4.5"); got.Status != "missing" {
		t.Fatalf("before ReplacePrices status = %q, want missing", got.Status)
	}

	m.ReplacePrices(nil, map[string]Price{
		"anthropic/claude-sonnet-4-5": {InputPerM: 3, OutputPerM: 15},
	})
	if got := m.ResolvePrice("other", "claude-sonnet-4.5"); got.Key != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("after ReplacePrices = %+v, want canonical match", got)
	}
}

// backfillStore serves one page of unpriced rows and records updates.
type backfillStore struct {
	rows    []store.UnpricedUsageRecord
	served  bool
	updates map[string]store.UsagePricingUpdate
}

func (b *backfillStore) Record(context.Context, store.UsageRecord) error        { return nil }
func (b *backfillStore) RecordBatch(context.Context, []store.UsageRecord) error { return nil }

func (b *backfillStore) ListUnpriced(context.Context, time.Time, string, int) ([]store.UnpricedUsageRecord, error) {
	if b.served {
		return nil, nil
	}
	b.served = true
	return b.rows, nil
}

func (b *backfillStore) UpdateUsagePricing(_ context.Context, id string, u store.UsagePricingUpdate) error {
	b.updates[id] = u
	return nil
}

func TestBackfillUnpricedSkipsUnpriceableModelsAndPricesTheRest(t *testing.T) {
	now := time.Now().UTC()
	repo := &backfillStore{updates: map[string]store.UsagePricingUpdate{}}
	for i, model := range []string{"subscription-only", "gpt-4.1-mini", "subscription-only", "subscription-only"} {
		repo.rows = append(repo.rows, store.UnpricedUsageRecord{
			ID: string(rune('a' + i)), Provider: "openai", Model: model,
			PromptTokens: 1_000_000, CompletionTokens: 1_000_000, CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	m := New(repo, nil, map[string]Price{
		"openai/gpt-4.1-mini": {InputPerM: 0.4, OutputPerM: 1.6},
	})

	updated, err := m.BackfillUnpriced(context.Background())
	if err != nil {
		t.Fatalf("BackfillUnpriced() error = %v", err)
	}
	if updated != 1 || len(repo.updates) != 1 {
		t.Fatalf("updated = %d (%d writes), want exactly the one priceable row", updated, len(repo.updates))
	}
	if u, ok := repo.updates["b"]; !ok || u.CostNanos != 2_000_000_000 || !u.PricingBackfilled {
		t.Fatalf("row b update = %+v (ok=%v), want backfilled cost of $2.00", u, ok)
	}
}

func TestBackfillUnpricedStopsWhenContextCancelled(t *testing.T) {
	repo := &backfillStore{updates: map[string]store.UsagePricingUpdate{}, rows: []store.UnpricedUsageRecord{
		{ID: "a", Provider: "openai", Model: "gpt-4.1-mini", PromptTokens: 10, CreatedAt: time.Now()},
	}}
	m := New(repo, nil, map[string]Price{"openai/gpt-4.1-mini": {InputPerM: 1}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := m.BackfillUnpriced(ctx); err == nil {
		t.Fatal("BackfillUnpriced() error = nil, want context cancellation")
	}
	if len(repo.updates) != 0 {
		t.Fatalf("writes after cancel = %d, want 0", len(repo.updates))
	}
}
