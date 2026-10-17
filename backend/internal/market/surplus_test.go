package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const surplusFixture = `{"markets":[
  {"model":"deepseek-v4.1-flash","best_input_per_1m":270,"best_output_per_1m":1080,"best_cache_read_per_1m":5,"best_cache_write_per_1m":270},
  {"model":"no-best","direct_input_per_1m":300000},
  {"model":"zero-output","best_input_per_1m":100,"best_output_per_1m":0}
]}`

func TestParseSurplusMapsBestAsk(t *testing.T) {
	models, err := ParseSurplus(strings.NewReader(surplusFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1 (only the fully-priced one)", len(models))
	}
	m := models[0]
	if m.Slug != "surplus:deepseek-v4.1-flash" {
		t.Fatalf("slug = %q", m.Slug)
	}
	if m.MinAskIn != 0.00027 || m.MinAskOut != 0.00108 {
		t.Fatalf("rates = (%v,%v), want (0.00027,0.00108)", m.MinAskIn, m.MinAskOut)
	}
	if m.CacheRead != 0.000005 || m.CacheWrite != 0.00027 {
		t.Fatalf("cache = (%v,%v), want (0.000005,0.00027)", m.CacheRead, m.CacheWrite)
	}
}

func TestParseSurplusSkipsMissingBest(t *testing.T) {
	models, err := ParseSurplus(strings.NewReader(surplusFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.Slug == "surplus:no-best" || m.Slug == "surplus:zero-output" {
			t.Fatalf("model %q should be skipped", m.Slug)
		}
	}
}

func TestParseSurplusMissingCacheStaysZero(t *testing.T) {
	const fixture = `{"markets":[{"model":"nocache","best_input_per_1m":100,"best_output_per_1m":200}]}`
	models, err := ParseSurplus(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d, want 1", len(models))
	}
	if models[0].CacheRead != 0 || models[0].CacheWrite != 0 {
		t.Fatalf("cache = (%v,%v), want (0,0)", models[0].CacheRead, models[0].CacheWrite)
	}
}

func TestFetchSurplusNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if _, err := FetchSurplus(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestFetchSurplusParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(surplusFixture))
	}))
	defer srv.Close()
	models, err := FetchSurplus(context.Background(), srv.URL)
	if err != nil || len(models) != 1 {
		t.Fatalf("models=%d err=%v", len(models), err)
	}
}

func TestParseSurplusBookTwoCheapest(t *testing.T) {
	const book = `{"offers":[
	  {"trusted":false,"available":true,"price_input_per_1m":448,"price_output_per_1m":1792},
	  {"trusted":false,"available":true,"price_input_per_1m":450,"price_output_per_1m":1800},
	  {"trusted":false,"available":false,"price_input_per_1m":100,"price_output_per_1m":400},
	  {"trusted":true,"available":true,"price_input_per_1m":900,"price_output_per_1m":3600}
	]}`
	cheapIn, cheapOut, nextIn, nextOut, found, err := ParseSurplusBook(strings.NewReader(book))
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if cheapIn != 0.000448 || cheapOut != 0.001792 {
		t.Fatalf("cheap = (%v,%v)", cheapIn, cheapOut)
	}
	if nextIn != 0.000450 || nextOut != 0.001800 {
		t.Fatalf("next = (%v,%v), want second available offer", nextIn, nextOut)
	}
}

func TestParseSurplusBookNoAvailableOffers(t *testing.T) {
	const book = `{"offers":[{"trusted":false,"available":false,"price_input_per_1m":448,"price_output_per_1m":1792}]}`
	_, _, _, _, found, err := ParseSurplusBook(strings.NewReader(book))
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("expected found=false when no offer is available")
	}
}

func TestParseSurplusReasoningRate(t *testing.T) {
	const fixture = `{"markets":[
  {"model":"priced","best_input_per_1m":100,"best_output_per_1m":200,"best_reasoning_per_1m":500},
  {"model":"unpriced","best_input_per_1m":100,"best_output_per_1m":200,"best_reasoning_per_1m":null}
]}`
	models, err := ParseSurplus(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d, want 2", len(models))
	}
	if models[0].Reasoning != 0.0005 {
		t.Fatalf("reasoning = %v, want 0.0005", models[0].Reasoning)
	}
	if models[1].Reasoning != 0 {
		t.Fatalf("null reasoning = %v, want 0", models[1].Reasoning)
	}
	c := NewSnapshotCache()
	c.Replace(models)
	if r, _ := c.Rate("surplus:priced"); r.ReasoningPerM != 0.0005 {
		t.Fatalf("cache reasoning = %v, want 0.0005", r.ReasoningPerM)
	}
}
