package finance

import (
	"math"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func row() store.CostBasisRow {
	return store.CostBasisRow{Provider: "p", ChainID: "c1", Requests: 1, BilledNanos: 18e9, UpstreamNanos: 10e9, UnknownBilledNanos: 3e9}
}

func TestCostNanos_ExactAndEstimated(t *testing.T) {
	e, est := CostNanos(row(), Markups{Global: 50})
	if e != 10e9 || est != 2e9 {
		t.Fatalf("got %d %d", e, est)
	}
}

func TestCostNanos_ChainOverride(t *testing.T) {
	e, est := CostNanos(row(), Markups{Global: 50, ByChain: map[string]float64{"c1": 0}})
	if e != 10e9 || est != 3e9 {
		t.Fatalf("got %d %d", e, est)
	}
}

func TestBuildOverview_ProfitExample(t *testing.T) {
	rows := []store.CostBasisRow{{Provider: "p", Requests: 2, BilledNanos: 15e9, UpstreamNanos: 10e9}}
	o := BuildOverview(rows, Markups{}, store.PaidOrderTotals{Count: 1, AmountIDR: 16000, CreditMicros: 1e6}, 2e6, map[string]int64{"p": 2, "q": 1})
	if !near(o.ProfitUSD, 5, 1e-9) || !near(o.MarginPct, 33.33, 0.01) || o.Requests != 2 || o.InFlight != 3 {
		t.Fatalf("%+v", o)
	}
	if o.CreditInIDR != 16000 || o.CreditInUSD != 1 || o.PaidOrders != 1 || o.OutstandingCreditUSD != 2 {
		t.Fatalf("%+v", o)
	}
	if z := BuildOverview(nil, Markups{}, store.PaidOrderTotals{}, 0, nil); z.MarginPct != 0 {
		t.Fatal("margin")
	}
}

func acct() store.UpstreamAccount {
	return store.UpstreamAccount{ID: "a1", Status: "active", OpeningBalanceMicros: 100e6, LowBalanceMicros: 100e6, Providers: []string{"p"}}
}

func TestBalances_OpeningCutoff(t *testing.T) {
	bs, _ := Balances(BalanceInput{
		Accounts:     []store.UpstreamAccount{acct()},
		TopupMicros:  map[string]int64{"a1": 20e6},
		SinceOpening: []store.CostBasisRow{{Provider: "p", UpstreamNanos: 30e9, BilledNanos: 30e9}},
	})
	if !near(bs[0].BalanceUSD, 90, 1e-9) || bs[0].TopupsUSD != 20 || bs[0].SpentUSD != 30 {
		t.Fatalf("%+v", bs[0])
	}
}

func TestBalances_UnmappedBucket(t *testing.T) {
	rows := []store.CostBasisRow{
		{Provider: "p-x", Requests: 2, UpstreamNanos: 4e9},
		{Provider: "p-x", ChainID: "z", Requests: 1, UpstreamNanos: 1e9},
		{Provider: "p", Requests: 1, UpstreamNanos: 2e9},
	}
	bs, u := Balances(BalanceInput{Accounts: []store.UpstreamAccount{acct()}, Range: rows, InFlight: map[string]int64{"p": 3}})
	if len(u.Providers) != 1 || u.Providers[0] != "p-x" || u.Requests != 3 || !near(u.SpentRangeUSD, 5, 1e-9) {
		t.Fatalf("%+v", u)
	}
	if !near(bs[0].SpentRangeUSD, 2, 1e-9) || bs[0].InFlight != 3 {
		t.Fatalf("%+v", bs[0])
	}
}

func TestBalances_LowAndDaysLeft(t *testing.T) {
	in := BalanceInput{
		Accounts:     []store.UpstreamAccount{acct()},
		TopupMicros:  map[string]int64{"a1": 20e6},
		SinceOpening: []store.CostBasisRow{{Provider: "p", UpstreamNanos: 30e9}},
		Last7d:       []store.CostBasisRow{{Provider: "p", UpstreamNanos: 14e9}},
	}
	bs, _ := Balances(in)
	if !bs[0].Low || !near(bs[0].AvgDailyUSD, 2, 1e-9) || bs[0].DaysLeft == nil || !near(*bs[0].DaysLeft, 45, 1e-9) {
		t.Fatalf("%+v", bs[0])
	}
	in.Last7d = nil
	bs, _ = Balances(in)
	if bs[0].DaysLeft != nil || !bs[0].Low {
		t.Fatalf("%+v", bs[0])
	}
}

func TestRangeStart(t *testing.T) {
	loc := time.FixedZone("x", 7*3600)
	now := time.Date(2026, 10, 15, 13, 30, 0, 0, loc)
	got, err := RangeStart("today", now)
	if err != nil || !got.Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, loc)) {
		t.Fatal(got, err)
	}
	if got, _ := RangeStart("all", now); !got.IsZero() {
		t.Fatal("all")
	}
	if got, _ := RangeStart("7d", now); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatal("7d")
	}
	if _, err := RangeStart("bad", now); err == nil {
		t.Fatal("want error")
	}
}
