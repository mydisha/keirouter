package toxicity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openaiEngine calls OpenAI's /v1/moderations endpoint. The endpoint accepts
// any text input and returns per-category booleans + scores. We map those
// categories to our internal names and rescale 0.0–1.0 → 0–100.
//
// OpenAI moderation is free for OpenAI-account holders, supports many
// languages including Indonesian, and is the highest-recall option of the
// supported engines.
type openaiEngine struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string // default "https://api.openai.com/v1"
	model      string // default "omni-moderation-latest"
}

// OpenAIConfig configures the OpenAI moderation engine.
type OpenAIConfig struct {
	APIKey  string        // Bearer token. Required.
	BaseURL string        // OpenAI-compatible base. Default: https://api.openai.com/v1
	Model   string        // Default: omni-moderation-latest
	Timeout time.Duration // HTTP timeout. Default: 5s
}

func newOpenAIEngine(cfg OpenAIConfig) *openaiEngine {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "omni-moderation-latest"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &openaiEngine{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		apiKey:     cfg.APIKey,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		model:      cfg.Model,
	}
}

// Score implements engine.
func (e *openaiEngine) Score(ctx context.Context, text string, allowed map[string]bool) ([]CategoryScore, error) {
	if e.apiKey == "" {
		// No credentials wired — fail soft. Caller will see nil hits.
		return nil, nil
	}
	body, err := json.Marshal(moderationRequest{Input: text, Model: e.model})
	if err != nil {
		return nil, fmt.Errorf("toxicity/openai: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/moderations", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("toxicity/openai: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("toxicity/openai: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("toxicity/openai: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("toxicity/openai: status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var parsed moderationResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("toxicity/openai: parse: %w", err)
	}
	if len(parsed.Results) == 0 {
		return nil, nil
	}
	return mapModerationScores(parsed.Results[0], allowed), nil
}

// moderationRequest mirrors the public OpenAI moderation API contract.
type moderationRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type moderationResponse struct {
	Results []moderationResult `json:"results"`
}

// moderationResult uses map types so we can iterate categories without
// hard-coding every name OpenAI may add over time.
type moderationResult struct {
	Categories     map[string]bool    `json:"categories"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

// openaiCategoryMap collapses OpenAI's granular category names into our
// internal set. Subcategories (e.g. hate/threatening) fold into the parent.
var openaiCategoryMap = map[string]string{
	"hate":                   "hate",
	"hate/threatening":       "hate",
	"harassment":             "harassment",
	"harassment/threatening": "harassment",
	"violence":               "violence",
	"violence/graphic":       "violence",
	"sexual":                 "sexual",
	"sexual/minors":          "sexual",
	"self-harm":              "self_harm",
	"self-harm/intent":       "self_harm",
	"self-harm/instructions": "self_harm",
	"illicit/violent":        "violence",
}

// mapModerationScores keeps the *highest* normalized score across OpenAI
// subcategories that fold to the same internal category.
func mapModerationScores(r moderationResult, allowed map[string]bool) []CategoryScore {
	bucket := map[string]int{}
	for raw, score := range r.CategoryScores {
		internal, ok := openaiCategoryMap[raw]
		if !ok {
			continue
		}
		if allowed != nil && !allowed[internal] {
			continue
		}
		s := int(score*100 + 0.5)
		if s > bucket[internal] {
			bucket[internal] = s
		}
	}
	out := make([]CategoryScore, 0, len(bucket))
	for cat, s := range bucket {
		if s <= 0 {
			continue
		}
		out = append(out, CategoryScore{Category: cat, Score: s})
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
