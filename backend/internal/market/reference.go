package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OpenRouterModelsURL is OpenRouter's public model list. Prices there are USD
// per token, as decimal strings.
const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

// Reference is an official list price in USD per 1M tokens.
type Reference struct {
	InputPerM  float64
	OutputPerM float64
}

// Valid reports whether both sides are positive finite prices.
func (r Reference) Valid() bool {
	return validRate(r.InputPerM) && validRate(r.OutputPerM) && r.InputPerM > 0 && r.OutputPerM > 0
}

// ReferenceCache holds official list prices keyed by normalized model name.
// It is filled from OpenRouter's public model list and read when the public
// catalog computes a model's discount.
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

// Len reports how many models have a reference price.
func (c *ReferenceCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byModel)
}

// Lookup returns the reference for a model name, matched on the normalized
// name with or without a vendor prefix ("deepseek/deepseek-v4.1-flash" and
// "deepseek-v4.1-flash" both match).
func (c *ReferenceCache) Lookup(model string) (Reference, bool) {
	if c == nil {
		return Reference{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, key := range referenceKeys(model) {
		if r, ok := c.byModel[key]; ok {
			return r, true
		}
	}
	return Reference{}, false
}

// NormalizeModelName lowercases a model id and folds '.' and '_' into '-', so
// "claude-sonnet-4.5" and "claude_sonnet_4-5" compare equal.
func NormalizeModelName(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.NewReplacer(".", "-", "_", "-").Replace(m)
}

// referenceKeys lists the lookup keys for a model: the full normalized id and,
// when it has a vendor prefix, the bare name after the last '/'.
func referenceKeys(model string) []string {
	full := NormalizeModelName(model)
	if full == "" {
		return nil
	}
	keys := []string{full}
	if i := strings.LastIndex(full, "/"); i >= 0 && i+1 < len(full) {
		keys = append(keys, full[i+1:])
	}
	return keys
}

// ParseOpenRouter maps OpenRouter's model list into references keyed by both
// the full id ("vendor/model") and the bare model name. Variant ids
// ("...:free", "...:extended") and models without a fixed token price are
// skipped. A bare name shared by vendors at different prices is dropped as
// ambiguous; the full id still matches.
func ParseOpenRouter(r io.Reader) (map[string]Reference, error) {
	var body struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	out := map[string]Reference{}
	ambiguous := map[string]bool{}
	for _, m := range body.Data {
		if m.ID == "" || strings.Contains(m.ID, ":") {
			continue
		}
		in, errIn := strconv.ParseFloat(m.Pricing.Prompt, 64)
		outRate, errOut := strconv.ParseFloat(m.Pricing.Completion, 64)
		if errIn != nil || errOut != nil {
			continue
		}
		ref := Reference{InputPerM: in * 1e6, OutputPerM: outRate * 1e6}
		if !ref.Valid() {
			continue
		}
		keys := referenceKeys(m.ID)
		out[keys[0]] = ref
		if len(keys) < 2 || ambiguous[keys[1]] {
			continue
		}
		if prev, ok := out[keys[1]]; ok && prev != ref {
			delete(out, keys[1])
			ambiguous[keys[1]] = true
			continue
		}
		out[keys[1]] = ref
	}
	return out, nil
}

// FetchOpenRouter downloads and parses OpenRouter's public model list.
func FetchOpenRouter(ctx context.Context, url string) (map[string]Reference, error) {
	if url == "" {
		url = OpenRouterModelsURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter models status %d", resp.StatusCode)
	}
	return ParseOpenRouter(resp.Body)
}
