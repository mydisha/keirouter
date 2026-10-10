package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Cloudflare Workers AI exposes two API surfaces under one account:
//
//   - the OpenAI-compatible inference endpoint
//     /accounts/{accountId}/ai/v1/{chat/completions,embeddings}
//   - the Cloudflare-native management/inference endpoints
//     /accounts/{accountId}/ai/run/{model} and /accounts/{accountId}/ai/models/search
//
// Chat traffic uses the first; model discovery must use the second. The
// catalogue base URL ends in /ai/v1, so the account root is derived by
// stripping that suffix (LiteLLM does the same rewrite in reverse for the
// legacy /ai/run base).

const (
	cloudflareV1Suffix  = "/ai/v1"
	cloudflareRunSuffix = "/ai/run"
	// cloudflareModelsPerPage is the page size for models/search; the API
	// paginates with page/per_page.
	cloudflareModelsPerPage = 100
	cloudflareModelsMaxPage = 20
)

// cloudflareAccountRoot returns ".../accounts/{id}" for either API surface.
func cloudflareAccountRoot(base string) string {
	base = strings.TrimRight(base, "/")
	for _, suffix := range []string{cloudflareV1Suffix, cloudflareRunSuffix, "/ai"} {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return base
}

// NormalizeCloudflareBaseURL rewrites a Workers AI base URL to the
// OpenAI-compatible /ai/v1 surface. Users copy the legacy /ai/run URL from
// older docs, which does not serve chat/completions; LiteLLM rewrites it the
// same way.
func NormalizeCloudflareBaseURL(base string) string {
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, cloudflareRunSuffix) {
		return strings.TrimSuffix(trimmed, cloudflareRunSuffix) + cloudflareV1Suffix
	}
	// AI Gateway: https://gateway.ai.cloudflare.com/v1/{account}/{gateway}/workers-ai
	if strings.Contains(trimmed, "gateway.ai.cloudflare.com") && strings.HasSuffix(trimmed, "/workers-ai") {
		return trimmed + "/v1"
	}
	return base
}

// CanonicalCloudflareModel gives bare model names the "@cf/" namespace the
// API requires ("meta/llama-3.3-70b-instruct-fp8-fast" → "@cf/meta/...").
// Names that already carry a namespace (@cf/, @hf/) are untouched.
func CanonicalCloudflareModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.HasPrefix(model, "@") {
		return model
	}
	return "@cf/" + model
}

// CloudflareModelSource implements LiveModelSource for Cloudflare Workers AI.
// The models/search endpoint returns Cloudflare's envelope
// ({"success":true,"result":[...]}) whose entries carry the model id in
// "name", the modality in "task.name" and feature flags in "properties".
type CloudflareModelSource struct {
	defaultBase string
}

// cloudflareModelEntry is one models/search result.
type cloudflareModelEntry struct {
	ID          string `json:"id"`   // opaque UUID, not the model id
	Name        string `json:"name"` // "@cf/meta/llama-3.3-70b-instruct-fp8-fast"
	Description string `json:"description"`
	Task        struct {
		Name string `json:"name"` // "Text Generation", "Text-to-Image", ...
	} `json:"task"`
	Properties []struct {
		PropertyID string          `json:"property_id"`
		Value      json.RawMessage `json:"value"`
	} `json:"properties"`
}

// ListModels fetches available models from the Cloudflare Workers AI API via
// GET /accounts/{accountId}/ai/models/search, authenticated with the bearer
// token, following page/per_page pagination.
func (s *CloudflareModelSource) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	base := s.defaultBase
	if creds.BaseURL != "" {
		base = creds.BaseURL
	}
	// Resolve {accountId} placeholder from creds.Extra.
	base = resolveURLPlaceholders(base, creds.Extra)
	// If the base URL still contains an unresolved placeholder, we can't
	// discover models — the account ID is required.
	if strings.Contains(base, "{") {
		return nil, fmt.Errorf("cloudflare: account ID not available for model discovery")
	}
	if strings.Contains(base, "gateway.ai.cloudflare.com") {
		return nil, fmt.Errorf("cloudflare: model discovery is not available through AI Gateway; use the api.cloudflare.com account URL")
	}

	searchURL := joinURL(cloudflareAccountRoot(base), "ai/models/search")
	var out []ModelSpec
	for page := 1; page <= cloudflareModelsMaxPage; page++ {
		entries, err := s.fetchPage(ctx, searchURL, creds, page)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if spec, ok := cloudflareModelSpec(entry); ok {
				out = append(out, spec)
			}
		}
		if len(entries) < cloudflareModelsPerPage {
			break
		}
	}
	return out, nil
}

func (s *CloudflareModelSource) fetchPage(ctx context.Context, searchURL string, creds core.Credentials, page int) ([]cloudflareModelEntry, error) {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(cloudflareModelsPerPage))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	switch {
	case creds.AccessToken != "":
		req.Header.Set("Authorization", bearer(creds.AccessToken))
	case creds.APIKey != "":
		req.Header.Set("Authorization", bearer(creds.APIKey))
	}
	req.Header.Set("Accept", "application/json")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read cloudflare models response: %w", err)
	}
	var raw struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result []cloudflareModelEntry `json:"result"`
	}
	if resp.StatusCode >= 400 || len(body) == 0 {
		if json.Unmarshal(body, &raw) == nil && len(raw.Errors) > 0 {
			return nil, fmt.Errorf("cloudflare: %s (code %d, HTTP %d)", raw.Errors[0].Message, raw.Errors[0].Code, resp.StatusCode)
		}
		return nil, fmt.Errorf("GET /ai/models/search returned %d: %s", resp.StatusCode, truncateError(body))
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode cloudflare models response: %w", err)
	}
	if !raw.Success && len(raw.Errors) > 0 {
		return nil, fmt.Errorf("cloudflare: %s", raw.Errors[0].Message)
	}
	return raw.Result, nil
}

// cloudflareModelSpec maps a models/search entry to a ModelSpec, dropping
// tasks KeiRouter cannot route (classification, translation, ...).
func cloudflareModelSpec(entry cloudflareModelEntry) (ModelSpec, bool) {
	id := entry.Name
	if !strings.HasPrefix(id, "@") {
		// Older payloads carried the model id in "id".
		if strings.HasPrefix(entry.ID, "@") {
			id = entry.ID
		} else {
			return ModelSpec{}, false
		}
	}
	spec := ModelSpec{ID: id, Name: id, Kind: core.ServiceLLM}
	switch strings.ToLower(entry.Task.Name) {
	case "text generation", "image-to-text", "":
		spec.Kind = core.ServiceLLM
	case "text-to-image", "image-to-image":
		spec.Kind = core.ServiceImage
	case "text embeddings":
		spec.Kind = core.ServiceEmbedding
	case "automatic speech recognition":
		spec.Kind = core.ServiceSTT
	case "text-to-speech":
		spec.Kind = core.ServiceTTS
	default:
		return ModelSpec{}, false
	}
	// Image models can also be recognised by their vendor path.
	if spec.Kind == core.ServiceLLM && (strings.HasPrefix(id, "@cf/black-forest-labs/") || strings.HasPrefix(id, "@cf/stabilityai/")) {
		spec.Kind = core.ServiceImage
	}
	return spec, true
}
