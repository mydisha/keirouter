package dispatch

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingHealth struct {
	marks atomic.Int64
}

func (h *countingHealth) IsUnhealthy(context.Context, string, string) (bool, error) {
	return false, nil
}
func (h *countingHealth) MarkHealthy(context.Context, string, string) error {
	h.marks.Add(1)
	return nil
}
func (h *countingHealth) UnhealthyAccounts(context.Context, []string, string) (map[string]bool, error) {
	return nil, nil
}

func newHealthyMarkDispatcher() (*Dispatcher, *countingHealth, *time.Time) {
	d := New(nil, nil, nil)
	h := &countingHealth{}
	d.SetHealthSource(h)
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return now }
	return d, h, &now
}

func TestNoteSuccessMarksHealthyOncePerInterval(t *testing.T) {
	d, h, now := newHealthyMarkDispatcher()
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { d.NoteSuccess(ctx, "openai", "acc-1", "gpt-4o") })
	}
	wg.Wait()
	if got := h.marks.Load(); got != 1 {
		t.Fatalf("MarkHealthy calls = %d, want 1 for a burst of successes", got)
	}

	d.NoteSuccess(ctx, "openai", "acc-1", "gpt-4o-mini")
	if got := h.marks.Load(); got != 2 {
		t.Fatalf("another model must be marked on its own; calls = %d", got)
	}

	*now = now.Add(markHealthyInterval)
	d.NoteSuccess(ctx, "openai", "acc-1", "gpt-4o")
	if got := h.marks.Load(); got != 3 {
		t.Fatalf("after the interval the pair is marked again; calls = %d", got)
	}

	d.NoteSuccess(ctx, "openai", "acc-1", "")
	if got := h.marks.Load(); got != 3 {
		t.Fatalf("a success without a model must not write; calls = %d", got)
	}
}

func TestForgetHealthyMarksForcesNextWrite(t *testing.T) {
	d, h, _ := newHealthyMarkDispatcher()
	ctx := context.Background()
	d.NoteSuccess(ctx, "openai", "acc-1", "a")
	d.NoteSuccess(ctx, "openai", "acc-1", "b")
	d.NoteSuccess(ctx, "openai", "acc-10", "a") // shares a prefix with acc-1
	if h.marks.Load() != 3 {
		t.Fatalf("setup marks = %d", h.marks.Load())
	}

	// The planner saw acc-1/a unhealthy: only that pair is re-marked.
	d.forgetHealthyMarks("acc-1", "a")
	d.NoteSuccess(ctx, "openai", "acc-1", "a")
	d.NoteSuccess(ctx, "openai", "acc-1", "b")
	if got := h.marks.Load(); got != 4 {
		t.Fatalf("calls = %d, want 4", got)
	}

	// A failure on acc-1 re-marks every model of acc-1 but not acc-10.
	d.forgetHealthyMarks("acc-1", "")
	d.NoteSuccess(ctx, "openai", "acc-1", "a")
	d.NoteSuccess(ctx, "openai", "acc-1", "b")
	d.NoteSuccess(ctx, "openai", "acc-10", "a")
	if got := h.marks.Load(); got != 6 {
		t.Fatalf("calls = %d, want 6", got)
	}
}

func TestHealthyMarksStayBounded(t *testing.T) {
	d, _, now := newHealthyMarkDispatcher()
	for i := range maxRecentFailureEntries + 50 {
		d.markHealthyDue(string(rune(i)) + "\x00m")
		*now = now.Add(time.Millisecond)
	}
	if n := len(d.healthyMarked); n > maxRecentFailureEntries {
		t.Fatalf("healthyMarked grew to %d entries", n)
	}
}
