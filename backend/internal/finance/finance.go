// Package finance holds pure revenue, cost and upstream balance math for the
// admin finance dashboard. Nanos are 1e-9 USD, micros are 1e-6 USD.
package finance

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

const (
	nanosPerUSD  = 1e9
	microsPerUSD = 1e6
)

// Markups carries the global markup percent and per-chain overrides.
type Markups struct {
	Global  float64
	ByChain map[string]float64
}

func (m Markups) forChain(id string) float64 {
	if v, ok := m.ByChain[id]; ok {
		return v
	}
	return m.Global
}

// CostNanos splits a row's upstream cost into the exact part and an estimate
// for requests whose upstream cost was not recorded.
func CostNanos(row store.CostBasisRow, m Markups) (exact, estimated int64) {
	exact = row.UpstreamNanos
	if row.UnknownBilledNanos > 0 {
		estimated = int64(math.Round(float64(row.UnknownBilledNanos) / (1 + m.forChain(row.ChainID)/100)))
	}
	return exact, estimated
}

func usd(nanos int64) float64 { return float64(nanos) / nanosPerUSD }

type Overview struct {
	RevenueUSD, CostExactUSD, CostEstimatedUSD, ProfitUSD, MarginPct float64
	Requests                                                         int64
	CreditInIDR                                                      int64
	CreditInUSD                                                      float64
	PaidOrders                                                       int64
	OutstandingCreditUSD                                             float64
	InFlight                                                         int64
}

func BuildOverview(rows []store.CostBasisRow, m Markups, paid store.PaidOrderTotals, outstandingMicros int64, inflight map[string]int64) Overview {
	var billed, exact, est, reqs int64
	for _, r := range rows {
		e, s := CostNanos(r, m)
		billed += r.BilledNanos
		exact += e
		est += s
		reqs += r.Requests
	}
	o := Overview{
		RevenueUSD:           usd(billed),
		CostExactUSD:         usd(exact),
		CostEstimatedUSD:     usd(est),
		ProfitUSD:            usd(billed - exact - est),
		Requests:             reqs,
		CreditInIDR:          paid.AmountIDR,
		CreditInUSD:          float64(paid.CreditMicros) / microsPerUSD,
		PaidOrders:           paid.Count,
		OutstandingCreditUSD: float64(outstandingMicros) / microsPerUSD,
	}
	if billed > 0 {
		o.MarginPct = o.ProfitUSD / o.RevenueUSD * 100
	}
	for _, n := range inflight {
		o.InFlight += n
	}
	return o
}

type AccountBalance struct {
	Account                                            store.UpstreamAccount
	BalanceUSD, TopupsUSD, SpentUSD, SpentEstimatedUSD float64
	SpentRangeUSD                                      float64
	AvgDailyUSD                                        float64
	DaysLeft                                           *float64
	InFlight                                           int64
	Low                                                bool
}

type UnmappedBucket struct {
	Providers     []string
	SpentRangeUSD float64
	Requests      int64
}

type BalanceInput struct {
	Accounts      []store.UpstreamAccount
	TopupMicros   map[string]int64
	SinceOpening  []store.CostBasisRow
	Range, Last7d []store.CostBasisRow
	Markups       Markups
	InFlight      map[string]int64
}

// spendByProvider returns exact and estimated nanos per provider.
func spendByProvider(rows []store.CostBasisRow, m Markups) (exact, est map[string]int64) {
	exact, est = map[string]int64{}, map[string]int64{}
	for _, r := range rows {
		e, s := CostNanos(r, m)
		exact[r.Provider] += e
		est[r.Provider] += s
	}
	return exact, est
}

func sumProviders(by map[string]int64, providers []string) int64 {
	var t int64
	for _, p := range providers {
		t += by[p]
	}
	return t
}

func Balances(in BalanceInput) ([]AccountBalance, UnmappedBucket) {
	mapped := map[string]bool{}
	for _, a := range in.Accounts {
		for _, p := range a.Providers {
			mapped[p] = true
		}
	}
	openE, openS := spendByProvider(in.SinceOpening, in.Markups)
	rangeE, rangeS := spendByProvider(in.Range, in.Markups)
	weekE, weekS := spendByProvider(in.Last7d, in.Markups)

	out := make([]AccountBalance, 0, len(in.Accounts))
	for _, a := range in.Accounts {
		exact := sumProviders(openE, a.Providers)
		est := sumProviders(openS, a.Providers)
		topups := in.TopupMicros[a.ID]
		b := AccountBalance{
			Account:           a,
			BalanceUSD:        float64(a.OpeningBalanceMicros+topups)/microsPerUSD - usd(exact+est),
			TopupsUSD:         float64(topups) / microsPerUSD,
			SpentUSD:          usd(exact + est),
			SpentEstimatedUSD: usd(est),
			SpentRangeUSD:     usd(sumProviders(rangeE, a.Providers) + sumProviders(rangeS, a.Providers)),
			AvgDailyUSD:       usd(sumProviders(weekE, a.Providers)+sumProviders(weekS, a.Providers)) / 7,
			InFlight:          sumProviders(in.InFlight, a.Providers),
		}
		b.Low = a.LowBalanceMicros > 0 && b.BalanceUSD < float64(a.LowBalanceMicros)/microsPerUSD
		if b.AvgDailyUSD > 0 {
			d := b.BalanceUSD / b.AvgDailyUSD
			b.DaysLeft = &d
		}
		out = append(out, b)
	}

	var u UnmappedBucket
	seen := map[string]bool{}
	for _, r := range in.Range {
		if mapped[r.Provider] {
			continue
		}
		e, s := CostNanos(r, in.Markups)
		u.SpentRangeUSD += usd(e + s)
		u.Requests += r.Requests
		if !seen[r.Provider] {
			seen[r.Provider] = true
			u.Providers = append(u.Providers, r.Provider)
		}
	}
	sort.Strings(u.Providers)
	return out, u
}

// RangeStart returns the inclusive lower bound for a named window.
func RangeStart(name string, now time.Time) (time.Time, error) {
	switch name {
	case "today":
		y, mo, d := now.Date()
		return time.Date(y, mo, d, 0, 0, 0, 0, now.Location()), nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), nil
	case "all":
		return time.Time{}, nil
	}
	return time.Time{}, fmt.Errorf("unknown range %q", name)
}
