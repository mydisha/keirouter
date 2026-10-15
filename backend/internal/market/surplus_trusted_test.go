package market

import (
	"math"
	"strings"
	"testing"
)

// Book prices are per 1M scaled by 1e6; direct list is $2/M in, $12/M out.
const trustedBookFixture = `{"model":"gpt-5.6-luna","offers":[
  {"provider_id":"openai","price_input_per_1m":2000,"price_output_per_1m":12000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":false,"healthy":false,"trusted":true},
  {"provider_id":"inferhub","price_input_per_1m":2100,"price_output_per_1m":12600,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":false},
  {"provider_id":"bedrock","price_input_per_1m":2495,"price_output_per_1m":14969,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
  {"provider_id":"bedrock","price_input_per_1m":2495,"price_output_per_1m":14969,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
  {"provider_id":"openai","price_input_per_1m":9000,"price_output_per_1m":54000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
  {"provider_id":"x","price_input_per_1m":9500,"price_output_per_1m":57000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":false,"trusted":true},
  {"provider_id":"openai","price_input_per_1m":12000,"price_output_per_1m":72000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true},
  {"provider_id":"openai","price_input_per_1m":20000,"price_output_per_1m":120000,"direct_input_per_1m":200000,"direct_output_per_1m":1200000,"available":true,"healthy":true,"trusted":true}
]}`

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestParseSurplusTrustedBookUsesOnlyTrustedAvailableHealthy(t *testing.T) {
	q, err := ParseSurplusTrustedBook(strings.NewReader(trustedBookFixture), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Found {
		t.Fatal("want found")
	}
	// Cheapest eligible is bedrock 2495/14969, not the unavailable openai 2000
	// nor the untrusted inferhub 2100.
	if !approx(q.BestIn, 0.002495) || !approx(q.BestOut, 0.014969) {
		t.Fatalf("best = (%v,%v)", q.BestIn, q.BestOut)
	}
	if q.Eligible != 5 {
		t.Fatalf("eligible = %d, want 5", q.Eligible)
	}
}

func TestParseSurplusTrustedBookDepthCapsAtThirdDistinctLevel(t *testing.T) {
	q, err := ParseSurplusTrustedBook(strings.NewReader(trustedBookFixture), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Distinct levels: 2495, 9000, 12000 (the unhealthy 9500 is excluded).
	// Level 3 = 12000/72000 => discount 94% => ceiling 94-2 = 92 (margin for
	// Surplus's estimated discount) => cap 8% of list.
	if q.MinDiscountPct != 92 {
		t.Fatalf("min discount = %d, want 92", q.MinDiscountPct)
	}
	if !approx(q.CapIn, 0.016) || !approx(q.CapOut, 0.096) {
		t.Fatalf("cap = (%v,%v), want (0.016,0.096)", q.CapIn, q.CapOut)
	}
}

func TestParseSurplusTrustedBookCapNeverBelowLevel(t *testing.T) {
	// Level 1 discount is 98.7525% → ceiling 98-2 = 96 → cap 4% of list, which must be
	// at least the level price itself so the fallback bill can never undercut.
	q, err := ParseSurplusTrustedBook(strings.NewReader(trustedBookFixture), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if q.MinDiscountPct != 96 {
		t.Fatalf("min discount = %d, want 96", q.MinDiscountPct)
	}
	if q.CapIn < q.BestIn || q.CapOut < q.BestOut {
		t.Fatalf("cap (%v,%v) below best (%v,%v)", q.CapIn, q.CapOut, q.BestIn, q.BestOut)
	}
}

func TestParseSurplusTrustedBookDepthBeyondLevelsUsesDeepest(t *testing.T) {
	q, err := ParseSurplusTrustedBook(strings.NewReader(trustedBookFixture), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Deepest eligible level = 20000/120000 => 90% discount.
	if q.MinDiscountPct != 88 {
		t.Fatalf("min discount = %d, want 88 (90 minus margin)", q.MinDiscountPct)
	}
}

func TestParseSurplusTrustedBookNoEligible(t *testing.T) {
	const fixture = `{"offers":[{"price_input_per_1m":1,"price_output_per_1m":2,"direct_input_per_1m":10,"direct_output_per_1m":20,"available":true,"healthy":true,"trusted":false}]}`
	q, err := ParseSurplusTrustedBook(strings.NewReader(fixture), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if q.Found {
		t.Fatal("untrusted-only book must not be found")
	}
}

func TestParseSurplusTrustedBookMissingDirectDisablesCap(t *testing.T) {
	const fixture = `{"offers":[{"provider_id":"google","price_input_per_1m":100,"price_output_per_1m":200,"available":true,"healthy":true,"trusted":true}]}`
	q, err := ParseSurplusTrustedBook(strings.NewReader(fixture), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Found {
		t.Fatal("want found")
	}
	// Without a list price no discount floor can be proven: no header, and the
	// fallback bill uses the deepest level's own price.
	if q.MinDiscountPct != 0 {
		t.Fatalf("min discount = %d, want 0", q.MinDiscountPct)
	}
	if !approx(q.CapIn, 0.0001) || !approx(q.CapOut, 0.0002) {
		t.Fatalf("cap = (%v,%v)", q.CapIn, q.CapOut)
	}
}

func TestTrustedModelCarriesQuote(t *testing.T) {
	q := TrustedQuote{Found: true, BestIn: 1, BestOut: 2, CapIn: 3, CapOut: 4, MinDiscountPct: 50}
	m := q.Model("gpt-5.6-luna")
	if m.Slug != "surplus-trusted:gpt-5.6-luna" || m.MinAskIn != 1 || m.MinAskOut != 2 ||
		m.FailoverIn != 3 || m.FailoverOut != 4 || m.MinDiscountPct != 50 {
		t.Fatalf("model = %+v", m)
	}
}

func TestParseSurplusTrustedBookProviderAllowList(t *testing.T) {
	q, err := ParseSurplusTrustedBook(strings.NewReader(trustedBookFixture), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Untrusted inferhub and the unhealthy "x" must never be allowed.
	if got := strings.Join(q.Providers, ","); got != "bedrock,openai" {
		t.Fatalf("providers = %q, want bedrock,openai", got)
	}
	if m := q.Model("gpt-5.6-luna"); strings.Join(m.Providers, ",") != "bedrock,openai" {
		t.Fatalf("model providers = %v", m.Providers)
	}
}

func TestParseSurplusTrustedBookSkipsOfferWithoutProviderID(t *testing.T) {
	const fixture = `{"offers":[{"price_input_per_1m":100,"price_output_per_1m":200,"direct_input_per_1m":1000,"direct_output_per_1m":2000,"available":true,"healthy":true,"trusted":true}]}`
	q, err := ParseSurplusTrustedBook(strings.NewReader(fixture), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if q.Found {
		t.Fatal("an offer with no provider id cannot be allow-listed and must be skipped")
	}
}

func TestParseSurplusTrustedBookFloorDropsShallowOffers(t *testing.T) {
	// List $1/M in, $4/M out. bedrock is 40% off on both rates; openai is 50%
	// off input but only 20% off output, so a min30 floor must drop it.
	const fixture = `{"offers":[
	  {"provider_id":"openai","price_input_per_1m":500000,"price_output_per_1m":3200000,"direct_input_per_1m":1000000,"direct_output_per_1m":4000000,"available":true,"healthy":true,"trusted":true},
	  {"provider_id":"bedrock","price_input_per_1m":600000,"price_output_per_1m":2400000,"direct_input_per_1m":1000000,"direct_output_per_1m":4000000,"available":true,"healthy":true,"trusted":true},
	  {"provider_id":"google","price_input_per_1m":700000,"price_output_per_1m":2800000,"direct_input_per_1m":1000000,"direct_output_per_1m":4000000,"available":true,"healthy":true,"trusted":true}
	]}`
	q, err := ParseSurplusTrustedBook(strings.NewReader(fixture), 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Found || q.Eligible != 2 {
		t.Fatalf("found=%v eligible=%d, want 2 (bedrock 40%%, google exactly 30%%)", q.Found, q.Eligible)
	}
	if !approx(q.BestIn, 0.6) || !approx(q.BestOut, 2.4) {
		t.Fatalf("best = (%v,%v), want bedrock (0.6,2.4)", q.BestIn, q.BestOut)
	}
	if got := strings.Join(q.Providers, ","); got != "bedrock,google" {
		t.Fatalf("providers = %q", got)
	}
	// Deepest level google is exactly 30% off → ceiling 30, cap = list × 0.7.
	if q.MinDiscountPct != 30 || !approx(q.CapIn, 0.7) || !approx(q.CapOut, 2.8) {
		t.Fatalf("ceiling=%d cap=(%v,%v)", q.MinDiscountPct, q.CapIn, q.CapOut)
	}
	if !approx(q.FloorCapIn, 0.7) || !approx(q.FloorCapOut, 2.8) {
		t.Fatalf("floor cap = (%v,%v), want (0.7,2.8)", q.FloorCapIn, q.FloorCapOut)
	}
}

func TestParseSurplusTrustedBookFloorNeedsListPrice(t *testing.T) {
	const fixture = `{"offers":[{"provider_id":"google","price_input_per_1m":100,"price_output_per_1m":200,"available":true,"healthy":true,"trusted":true}]}`
	q, err := ParseSurplusTrustedBook(strings.NewReader(fixture), 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	if q.Found {
		t.Fatal("an offer with no list price cannot prove a min30 discount")
	}
}

func TestMinDiscountFloor(t *testing.T) {
	cases := map[string]int{
		"https://api.surplusintelligence.ai/v1":         0,
		"https://api.surplusintelligence.ai/min30/v1":   30,
		"https://api.surplusintelligence.ai/min30/v1/":  30,
		"https://api.surplusintelligence.ai/min0/v1":    0,
		"https://api.surplusintelligence.ai/min100/v1":  100,
		"https://api.surplusintelligence.ai/min101/v1":  0,
		"https://api.surplusintelligence.ai/min030/v1":  0,
		"https://api.surplusintelligence.ai/min30.5/v1": 0,
		"https://api.surplusintelligence.ai/min-1/v1":   0,
		"https://api.surplusintelligence.ai/minimal/v1": 0,
		"https://example.com/anthropic/min45/v1":        45,
		"":                                              0,
	}
	for in, want := range cases {
		if got := MinDiscountFloor(in); got != want {
			t.Errorf("MinDiscountFloor(%q) = %d, want %d", in, got, want)
		}
	}
}
