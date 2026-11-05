package meter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// modelListingStore adds the distinct-model pre-pass to backfillStore and
// counts how often the full row listing is read.
type modelListingStore struct {
	backfillStore
	models   []store.UnpricedModel
	listErr  error
	rowReads int
}

func (s *modelListingStore) ListUnpricedModels(context.Context) ([]store.UnpricedModel, error) {
	return s.models, s.listErr
}

func (s *modelListingStore) ListUnpriced(ctx context.Context, after time.Time, id string, limit int) ([]store.UnpricedUsageRecord, error) {
	s.rowReads++
	return s.backfillStore.ListUnpriced(ctx, after, id, limit)
}

func TestBackfillUnpricedSkipsRowScanWhenNoModelIsPriceable(t *testing.T) {
	repo := &modelListingStore{
		backfillStore: backfillStore{updates: map[string]store.UsagePricingUpdate{}, rows: []store.UnpricedUsageRecord{
			{ID: "a", Provider: "custom-openai-mock", Model: "mock-model", PromptTokens: 10, CreatedAt: time.Now()},
		}},
		models: []store.UnpricedModel{{Provider: "custom-openai-mock", Model: "mock-model"}, {Provider: "lm-studio", Model: "qwën-本地"}},
	}
	m := New(repo, nil, map[string]Price{"openai/gpt-4.1-mini": {InputPerM: 1}})

	updated, err := m.BackfillUnpriced(context.Background())
	if err != nil || updated != 0 {
		t.Fatalf("BackfillUnpriced() = %d, %v; want 0, nil", updated, err)
	}
	if repo.rowReads != 0 {
		t.Fatalf("row listing read %d times, want 0 when no unpriced model has a price", repo.rowReads)
	}
}

func TestBackfillUnpricedScansWhenSomeModelIsPriceable(t *testing.T) {
	now := time.Now().UTC()
	repo := &modelListingStore{
		backfillStore: backfillStore{updates: map[string]store.UsagePricingUpdate{}, rows: []store.UnpricedUsageRecord{
			{ID: "a", Provider: "custom-openai-mock", Model: "mock-model", PromptTokens: 10, CreatedAt: now},
			{ID: "b", Provider: "openai", Model: "gpt-4.1-mini", PromptTokens: 1_000_000, CreatedAt: now.Add(time.Second)},
		}},
		models: []store.UnpricedModel{{Provider: "custom-openai-mock", Model: "mock-model"}, {Provider: "openai", Model: "gpt-4.1-mini"}},
	}
	m := New(repo, nil, map[string]Price{"openai/gpt-4.1-mini": {InputPerM: 1}})

	updated, err := m.BackfillUnpriced(context.Background())
	if err != nil || updated != 1 {
		t.Fatalf("BackfillUnpriced() = %d, %v; want 1, nil", updated, err)
	}
	if _, ok := repo.updates["b"]; !ok {
		t.Fatalf("priceable row b was not backfilled: %+v", repo.updates)
	}
}

func TestBackfillUnpricedModelListErrorIsReturned(t *testing.T) {
	boom := errors.New("db down")
	repo := &modelListingStore{backfillStore: backfillStore{updates: map[string]store.UsagePricingUpdate{}}, listErr: boom}
	m := New(repo, nil, nil)
	if _, err := m.BackfillUnpriced(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
