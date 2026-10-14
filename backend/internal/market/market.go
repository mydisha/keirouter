package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const APIURL = "https://inferhub.dev/api/market"

type Model struct {
	Slug      string  `json:"slug"`
	Family    string  `json:"family"`
	MinAskIn  float64 `json:"minAskIn"`
	MinAskOut float64 `json:"minAskOut"`
	MaxAskIn  float64 `json:"maxAskIn"`
	MaxAskOut float64 `json:"maxAskOut"`
	LastRate  float64 `json:"lastRate"`
	// CacheRead/CacheWrite are published cache rates when a source provides
	// them (Surplus). Zero means "not published": callers derive from input.
	CacheRead  float64 `json:"-"`
	CacheWrite float64 `json:"-"`
	// FailoverIn/FailoverOut are the next-best available ask for a source whose
	// cheapest offer may go dark (Surplus). Zero means "not known": the safety
	// margin then cannot floor against them.
	FailoverIn  float64 `json:"-"`
	FailoverOut float64 `json:"-"`
	// MinDiscountPct is the X-Min-Discount ceiling for surplus-trusted slugs
	// (zero = no ceiling).
	MinDiscountPct int `json:"-"`
}

func ParseSnapshot(r io.Reader) ([]Model, error) {
	var body struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	return body.Models, nil
}

// Fetch downloads the market snapshot in one request.
func Fetch(ctx context.Context, url string) ([]Model, error) {
	if url == "" {
		url = APIURL
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
		return nil, fmt.Errorf("market API status %d", resp.StatusCode)
	}
	return ParseSnapshot(resp.Body)
}
