package budget

import (
	"testing"
	"time"
)

func TestPeriodEnd(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"daily":   time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		"weekly":  time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		"monthly": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		"total":   {},
	}
	for period, want := range cases {
		if got := PeriodEnd(period, start); !got.Equal(want) {
			t.Errorf("PeriodEnd(%q) = %v, want %v", period, got, want)
		}
	}
}
