package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ModelsDevURL is models.dev's public catalog: every provider with its models,
// per-1M prices and token limits.
const ModelsDevURL = "https://models.dev/api.json"

// Reference is a model's official list price (USD per 1M tokens) and context
// window from models.dev.
type Reference struct {
	InputPerM  float64
	OutputPerM float64
	// ContextTokens is the context window; 0 when unknown.
	ContextTokens int
	// Provider is the model vendor whose own listing set the price (e.g.
	// "deepseek"); empty when the price is the most common one across
	// providers because the vendor does not list the model.
	Provider string
}

// Valid reports whether both price sides are positive finite values.
func (r Reference) Valid() bool {
	return validRate(r.InputPerM) && validRate(r.OutputPerM) && r.InputPerM > 0 && r.OutputPerM > 0
}

// ReferenceCache holds models.dev references keyed by normalized model name.
type ReferenceCache struct {
	mu      sync.RWMutex
	byModel map[string]Reference
}

func NewReferenceCache() *ReferenceCache {
	return &ReferenceCache{byModel: map[string]Reference{}}
}

// Replace installs a new reference set.
func (c *ReferenceCache) Replace(refs map[string]Reference) {
	c.mu.Lock()
	c.byModel = refs
	c.mu.Unlock()
}

// Lookup returns the reference for a model name. Vendor prefixes are ignored
// ("deepseek/deepseek-v4.1-flash" matches "deepseek-v4.1-flash").
func (c *ReferenceCache) Lookup(model string) (Reference, bool) {
	if c == nil {
		return Reference{}, false
	}
	key := referenceKey(model)
	if key == "" {
		return Reference{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.byModel[key]
	return r, ok
}

// NormalizeModelName lowercases a model id and folds '.' and '_' into '-', so
// "gemini-3.6-flash" and "gemini-3-6-flash" compare equal.
func NormalizeModelName(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.NewReplacer(".", "-", "_", "-").Replace(m)
}

// referenceKey is the normalized model name after the last '/'.
func referenceKey(model string) string {
	m := NormalizeModelName(model)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return m
}

type modelsDevEntry struct {
	provider   string
	firstParty bool // listed by the model's own vendor
	exact      bool // the entry's own id (not only its canonical id) matches
	in, out    float64
	context    int
}

func (e modelsDevEntry) priced() bool {
	return (Reference{InputPerM: e.in, OutputPerM: e.out}).Valid()
}

// ParseModelsDev builds one reference per model name from models.dev. An
// entry counts as the vendor's own listing when its provider id equals the
// vendor of its canonical_model_id ("deepseek" for "deepseek/..."). Per name:
//   - price and context come from the vendor's own listing, preferring an
//     entry whose id is the name itself, then the cheapest;
//   - without a vendor listing, the price is the one most providers charge
//     (ties go to the cheaper) and the context the most common one.
//
// Variant ids ("...:free", "...@eu") are skipped.
func ParseModelsDev(r io.Reader) (map[string]Reference, error) {
	var body map[string]struct {
		Models map[string]struct {
			ID        string `json:"id"`
			Canonical string `json:"canonical_model_id"`
			Cost      *struct {
				Input  float64 `json:"input"`
				Output float64 `json:"output"`
			} `json:"cost"`
			Limit struct {
				Context int `json:"context"`
			} `json:"limit"`
		} `json:"models"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	byName := map[string][]modelsDevEntry{}
	for providerID, p := range body {
		for key, m := range p.Models {
			id := m.ID
			if id == "" {
				id = key
			}
			if strings.ContainsAny(id, ":@") {
				continue
			}
			e := modelsDevEntry{provider: providerID, context: max(m.Limit.Context, 0)}
			if m.Cost != nil {
				e.in, e.out = m.Cost.Input, m.Cost.Output
			}
			vendor, _, hasVendor := strings.Cut(m.Canonical, "/")
			e.firstParty = hasVendor && vendor == providerID
			own := referenceKey(id)
			if own != "" {
				exact := e
				exact.exact = true
				byName[own] = append(byName[own], exact)
			}
			if canonical := referenceKey(m.Canonical); canonical != "" && canonical != own {
				byName[canonical] = append(byName[canonical], e)
			}
		}
	}
	out := make(map[string]Reference, len(byName))
	for name, entries := range byName {
		if ref, ok := pickReference(entries); ok {
			out[name] = ref
		}
	}
	return out, nil
}

// pickReference chooses one reference from all entries sharing a model name.
func pickReference(entries []modelsDevEntry) (Reference, bool) {
	var vendor []modelsDevEntry
	for _, e := range entries {
		if e.firstParty && e.priced() {
			vendor = append(vendor, e)
		}
	}
	if len(vendor) > 0 {
		best := vendor[0]
		for _, e := range vendor[1:] {
			if e.exact != best.exact {
				if e.exact {
					best = e
				}
				continue
			}
			if e.in+e.out < best.in+best.out {
				best = e
			}
		}
		ctx := best.context
		if ctx == 0 {
			ctx = mostCommonContext(entries)
		}
		return Reference{InputPerM: best.in, OutputPerM: best.out, ContextTokens: ctx, Provider: best.provider}, true
	}

	type pair struct{ in, out float64 }
	counts := map[pair]int{}
	var bestPair pair
	bestCount := 0
	for _, e := range entries {
		if !e.priced() {
			continue
		}
		p := pair{e.in, e.out}
		counts[p]++
		n := counts[p]
		if n > bestCount || (n == bestCount && p.in+p.out < bestPair.in+bestPair.out) {
			bestPair, bestCount = p, n
		}
	}
	ref := Reference{InputPerM: bestPair.in, OutputPerM: bestPair.out, ContextTokens: mostCommonContext(entries)}
	if bestCount == 0 && ref.ContextTokens == 0 {
		return Reference{}, false
	}
	return ref, true
}

// mostCommonContext returns the context window most entries report, ties to
// the smaller; 0 when none report one.
func mostCommonContext(entries []modelsDevEntry) int {
	counts := map[int]int{}
	best, bestCount := 0, 0
	for _, e := range entries {
		if e.context <= 0 {
			continue
		}
		counts[e.context]++
		n := counts[e.context]
		if n > bestCount || (n == bestCount && e.context < best) {
			best, bestCount = e.context, n
		}
	}
	return best
}

// FetchModelsDev downloads and parses the models.dev catalog.
func FetchModelsDev(ctx context.Context, url string) (map[string]Reference, error) {
	if url == "" {
		url = ModelsDevURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev status %d", resp.StatusCode)
	}
	return ParseModelsDev(resp.Body)
}
