package gateway

import (
	"testing"
	"time"
)

func TestSinceForPeriod(t *testing.T) {
	jakarta, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	for _, tc := range []struct {
		period string
		want   func(now time.Time) time.Time
	}{
		{"24h", func(now time.Time) time.Time { return now.Add(-24 * time.Hour) }},
		{"3d", func(now time.Time) time.Time { return now.In(jakarta).AddDate(0, 0, -3) }},
		{"7d", func(now time.Time) time.Time { return now.In(jakarta).AddDate(0, 0, -7) }},
		{"30d", func(now time.Time) time.Time { return now.In(jakarta).AddDate(0, 0, -30) }},
		{"bogus", func(now time.Time) time.Time { return now.In(jakarta).AddDate(0, 0, -30) }},
	} {
		t.Run(tc.period, func(t *testing.T) {
			before := time.Now()
			got := sinceForPeriod(tc.period, "Asia/Jakarta")
			after := time.Now()
			if got.Location() != time.UTC {
				t.Fatalf("location = %v, want UTC", got.Location())
			}
			lo, hi := tc.want(before), tc.want(after)
			if got.Before(lo.Add(-time.Second)) || got.After(hi.Add(time.Second)) {
				t.Fatalf("sinceForPeriod(%q) = %v, want within [%v, %v]", tc.period, got, lo, hi)
			}
		})
	}

	t.Run("today starts at local midnight", func(t *testing.T) {
		got := sinceForPeriod("today", "Asia/Jakarta").In(jakarta)
		if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
			t.Fatalf("today = %v, want local midnight", got)
		}
	})
}
