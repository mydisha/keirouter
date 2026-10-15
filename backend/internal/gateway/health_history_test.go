package gateway

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestSnapshotStatusAndIssue(t *testing.T) {
	cases := []struct {
		name  string
		sum   store.SnapshotSummary
		want  string
		issue string
	}{
		{"no traffic", store.SnapshotSummary{}, "unknown", ""},
		{"clean", store.SnapshotSummary{Requests: 100, Successes: 99, Failures: 1, Timeouts: 1, WorstStatusRank: 1}, "healthy", "timeout"},
		{"throttled", store.SnapshotSummary{Requests: 100, Successes: 98, Failures: 2, RateLimited: 2, WorstStatusRank: 1}, "degraded", "rate_limited"},
		{"failing", store.SnapshotSummary{Requests: 100, Successes: 60, Failures: 40, Provider5xx: 30, Timeouts: 10}, "unhealthy", "provider_5xx"},
	}
	for _, tc := range cases {
		got, _ := snapshotStatus(tc.sum)
		if got != tc.want {
			t.Errorf("%s: status %q, want %q", tc.name, got, tc.want)
		}
		if issue := snapshotIssue(tc.sum); issue != tc.issue {
			t.Errorf("%s: issue %q, want %q", tc.name, issue, tc.issue)
		}
	}
}

func TestHealthRangeIsHistorical(t *testing.T) {
	s := &Server{}
	for raw, want := range map[string]bool{"": false, "5m": false, "15m": false, "1h": true, "24h": true, "7d": true} {
		if got := s.healthRangeIsHistorical(raw); got != want {
			t.Errorf("healthRangeIsHistorical(%q) = %v, want %v", raw, got, want)
		}
	}
}
