package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"time"
)

const (
	// SurplusAPIURL is the public marketplace best-ask feed. Values in the
	// response are per 1M tokens scaled by 1e6 (300000 => $0.30/M).
	SurplusAPIURL = "https://api.surplusintelligence.ai/api/markets"
	SurplusPrefix = "surplus:"
	// surplusUnitScale converts the feed's scaled per-1M values to USD.
	surplusUnitScale = 1e6
)

type surplusEnvelope struct {
	Markets []struct {
		Model               string   `json:"model"`
		BestInputPer1M      *float64 `json:"best_input_per_1m"`
		BestOutputPer1M     *float64 `json:"best_output_per_1m"`
		BestCacheReadPer1M  *float64 `json:"best_cache_read_per_1m"`
		BestCacheWritePer1M *float64 `json:"best_cache_write_per_1m"`
		BestReasoningPer1M  *float64 `json:"best_reasoning_per_1m"`
		DirectInputPer1M    *float64 `json:"direct_input_per_1m"`
		DirectOutputPer1M   *float64 `json:"direct_output_per_1m"`
	} `json:"markets"`
}

// ParseSurplus maps the /api/markets best-ask feed into Market models keyed
// "surplus:<model>". The feed reports the cheapest currently-available offer
// per model; a model with no best ask is skipped.
func ParseSurplus(r io.Reader) ([]Model, error) {
	var body surplusEnvelope
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(body.Markets))
	for _, m := range body.Markets {
		if m.Model == "" || m.BestInputPer1M == nil || m.BestOutputPer1M == nil {
			continue
		}
		in := *m.BestInputPer1M / surplusUnitScale
		outRate := *m.BestOutputPer1M / surplusUnitScale
		if !validRate(in) || !validRate(outRate) || outRate <= 0 {
			continue
		}
		model := Model{
			Slug:      SurplusPrefix + m.Model,
			MinAskIn:  in,
			MinAskOut: outRate,
		}
		if m.BestCacheReadPer1M != nil {
			model.CacheRead = positiveOrZero(*m.BestCacheReadPer1M / surplusUnitScale)
		}
		if m.BestCacheWritePer1M != nil {
			model.CacheWrite = positiveOrZero(*m.BestCacheWritePer1M / surplusUnitScale)
		}
		if m.BestReasoningPer1M != nil {
			model.Reasoning = positiveOrZero(*m.BestReasoningPer1M / surplusUnitScale)
		}
		if m.DirectInputPer1M != nil && m.DirectOutputPer1M != nil {
			model.DirectIn = positiveOrZero(*m.DirectInputPer1M / surplusUnitScale)
			model.DirectOut = positiveOrZero(*m.DirectOutputPer1M / surplusUnitScale)
		}
		out = append(out, model)
	}
	return out, nil
}

func positiveOrZero(v float64) float64 {
	if !validRate(v) {
		return 0
	}
	return math.Max(0, v)
}

// FetchSurplus downloads and parses the Surplus best-ask feed.
func FetchSurplus(ctx context.Context, url string) ([]Model, error) {
	if url == "" {
		url = SurplusAPIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("surplus API status %d", resp.StatusCode)
	}
	return ParseSurplus(resp.Body)
}

type surplusBookEnvelope struct {
	Offers []struct {
		Trusted          bool     `json:"trusted"`
		Available        *bool    `json:"available"`
		PriceInputPer1M  *float64 `json:"price_input_per_1m"`
		PriceOutputPer1M *float64 `json:"price_output_per_1m"`
	} `json:"offers"`
}

// ParseSurplusBook reads a single-model order book (GET /api/markets/:model)
// and returns the cheapest and second-cheapest available ask, in USD/M. It
// returns found=false when no available offer has both prices.
func ParseSurplusBook(r io.Reader) (cheapIn, cheapOut, nextIn, nextOut float64, found bool, err error) {
	var body surplusBookEnvelope
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return 0, 0, 0, 0, false, err
	}
	type ask struct{ in, out float64 }
	var asks []ask
	for _, o := range body.Offers {
		if o.Available != nil && !*o.Available {
			continue
		}
		if o.PriceInputPer1M == nil || o.PriceOutputPer1M == nil {
			continue
		}
		in := *o.PriceInputPer1M / surplusUnitScale
		out := *o.PriceOutputPer1M / surplusUnitScale
		if !validRate(in) || !validRate(out) || out <= 0 {
			continue
		}
		asks = append(asks, ask{in, out})
	}
	if len(asks) == 0 {
		return 0, 0, 0, 0, false, nil
	}
	sort.Slice(asks, func(i, j int) bool {
		if asks[i].out != asks[j].out {
			return asks[i].out < asks[j].out
		}
		return asks[i].in < asks[j].in
	})
	cheap := asks[0]
	next := cheap
	for _, a := range asks[1:] {
		if a.out > cheap.out || a.in > cheap.in {
			next = a
			break
		}
	}
	return cheap.in, cheap.out, next.in, next.out, true, nil
}

// FetchSurplusBook downloads one model's order book and extracts the two
// cheapest available asks. bookURL is the model's order-book URL.
func FetchSurplusBook(ctx context.Context, bookURL string) (cheapIn, cheapOut, nextIn, nextOut float64, found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bookURL, nil)
	if err != nil {
		return 0, 0, 0, 0, false, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, 0, 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, 0, 0, false, fmt.Errorf("surplus book status %d", resp.StatusCode)
	}
	return ParseSurplusBook(resp.Body)
}
