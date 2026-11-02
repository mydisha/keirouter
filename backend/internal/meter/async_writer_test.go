package meter

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// recordingStore captures every row the async writer persists.
type recordingStore struct {
	mu      sync.Mutex
	rows    []store.UsageRecord
	single  int
	batches int
	block   chan struct{} // when set, RecordBatch waits on it
}

func (s *recordingStore) Record(_ context.Context, u store.UsageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.single++
	s.rows = append(s.rows, u)
	return nil
}

func (s *recordingStore) RecordBatch(_ context.Context, rs []store.UsageRecord) error {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches++
	s.rows = append(s.rows, rs...)
	return nil
}

func (s *recordingStore) ids() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.rows))
	for _, r := range s.rows {
		out[r.ID] = r.PromptTokens
	}
	return out
}

func TestAsyncWriterPersistsEveryRecordIntact(t *testing.T) {
	st := &recordingStore{}
	w := NewAsyncWriter(st, AsyncConfig{BatchSize: 7, FlushInterval: time.Hour, QueueSize: 1000})
	w.Start(context.Background())

	var wg sync.WaitGroup
	for i := range 250 {
		wg.Go(func() {
			// Each record carries its own payload, so a queue that aliased
			// one record across sends would show up as wrong token counts.
			_ = w.Record(context.Background(), store.UsageRecord{ID: fmt.Sprintf("r-%d", i), Model: "mödel", PromptTokens: i})
		})
	}
	wg.Wait()
	w.Close(5 * time.Second)

	got := st.ids()
	if len(got) != 250 {
		t.Fatalf("persisted %d records, want 250", len(got))
	}
	for i := range 250 {
		if tokens, ok := got[fmt.Sprintf("r-%d", i)]; !ok || tokens != i {
			t.Fatalf("record r-%d = %d (present=%v)", i, tokens, ok)
		}
	}
	if st.batches == 0 || st.single != 0 {
		t.Fatalf("batches=%d single=%d, want batched writes only", st.batches, st.single)
	}
}

func TestAsyncWriterFullQueuePolicies(t *testing.T) {
	for _, policy := range []string{"sync", "drop"} {
		t.Run(policy, func(t *testing.T) {
			st := &recordingStore{block: make(chan struct{})}
			w := NewAsyncWriter(st, AsyncConfig{BatchSize: 1, FlushInterval: time.Hour, QueueSize: 1, FullQueuePolicy: policy})
			w.Start(context.Background())

			// The first record is taken by the loop and blocks in RecordBatch;
			// the second fills the queue; the third finds it full.
			_ = w.Record(context.Background(), store.UsageRecord{ID: "a"})
			deadline := time.Now().Add(2 * time.Second)
			for len(w.ch) != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			_ = w.Record(context.Background(), store.UsageRecord{ID: "b"})
			_ = w.Record(context.Background(), store.UsageRecord{ID: "c"})

			stats := w.Stats()
			if policy == "sync" && (stats.SyncFallback != 1 || st.single != 1) {
				t.Fatalf("sync policy: stats=%+v single=%d", stats, st.single)
			}
			if policy == "drop" && stats.Dropped != 1 {
				t.Fatalf("drop policy: stats=%+v", stats)
			}
			close(st.block)
			w.Close(5 * time.Second)
		})
	}
}
