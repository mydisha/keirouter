package inflight

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTracker_IncDone(t *testing.T) {
	tr := New()
	done1 := tr.Inc("a")
	_ = tr.Inc("a")
	done1()
	require.Equal(t, map[string]int64{"a": 1}, tr.Snapshot())
}

func TestTracker_DoneIdempotent(t *testing.T) {
	tr := New()
	done := tr.Inc("a")
	done()
	done()
	require.Equal(t, map[string]int64{}, tr.Snapshot())
}

func TestTracker_NilSafe(t *testing.T) {
	var tr *Tracker
	require.NotPanics(t, func() { tr.Inc("a")() })
	require.Empty(t, tr.Snapshot())
}

func TestTracker_SnapshotIsCopy(t *testing.T) {
	tr := New()
	_ = tr.Inc("a")
	snap := tr.Snapshot()
	snap["a"] = 99
	require.Equal(t, map[string]int64{"a": 1}, tr.Snapshot())
}
