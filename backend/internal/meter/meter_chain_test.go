package meter

import (
	"context"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/store"
)

type captureStore struct{ rows []store.UsageRecord }

func (c *captureStore) Record(_ context.Context, u store.UsageRecord) error {
	c.rows = append(c.rows, u)
	return nil
}

func (c *captureStore) RecordBatch(_ context.Context, rs []store.UsageRecord) error {
	c.rows = append(c.rows, rs...)
	return nil
}

func TestRecordPersistsChainRouting(t *testing.T) {
	cs := &captureStore{}
	m := New(cs, nil, nil)
	if _, err := m.Record(context.Background(), Event{
		TenantID: "default", Provider: "codex", Model: "gpt-5-codex",
		Status: "success", ChainID: "chain-1", FallbackCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if len(cs.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(cs.rows))
	}
	if got := cs.rows[0]; got.ChainID != "chain-1" || got.FallbackCount != 2 {
		t.Fatalf("chain routing not persisted: chain=%q fallbacks=%d", got.ChainID, got.FallbackCount)
	}
}
