package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// GitHub Copilot client fingerprint.
const (
	copilotVSCodeVersion = "1.110.0"
	copilotChatVersion   = "0.38.0"
	copilotUserAgent     = "GitHubCopilotChat/0.38.0"
	copilotAPIVersion    = "2025-04-01"
)

// GitHubCopilot drives GitHub Copilot's OpenAI-compatible /chat/completions
// endpoint. It speaks the OpenAI dialect but requires the full Copilot editor
// fingerprint headers, and applies model-specific request transforms (gpt-5/o-
// series need max_completion_tokens; some models reject temperature/thinking).
// The copilot bearer token is supplied
// as the credential AccessToken (minted by the token refresher upstream).
type GitHubCopilot struct {
	id          string
	defaultBase string
	codec       transform.OpenAICodec
	// responsesCodec translates between the OpenAI Chat dialect and the
	// Responses dialect for codex-style models that Copilot only serves on
	// /responses (e.g. gpt-5-codex).
	responsesCodec transform.OpenAIResponsesCodec
	// codexModels caches models discovered to require the /responses endpoint,
	// so subsequent calls skip the /chat/completions probe. Guarded by mu.
	mu          sync.RWMutex
	codexModels map[string]bool
}

// NewGitHubCopilot builds a GitHub Copilot connector.
func NewGitHubCopilot(id, defaultBaseURL string) *GitHubCopilot {
	return &GitHubCopilot{id: id, defaultBase: defaultBaseURL, codexModels: map[string]bool{}}
}

func (c *GitHubCopilot) ID() string            { return c.id }
func (c *GitHubCopilot) Dialect() core.Dialect { return core.DialectOpenAI }

func (c *GitHubCopilot) baseURL(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return creds.BaseURL
	}
	return c.defaultBase
}

func (c *GitHubCopilot) endpoint(creds core.Credentials) string {
	base := c.baseURL(creds)
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return joinURL(base, "chat/completions")
}

// Validate confirms the Copilot bearer token is accepted by listing models. A
// 401/403 means the token is missing, expired, or revoked.
func (c *GitHubCopilot) Validate(ctx context.Context, creds core.Credentials) error {
	if creds.AccessToken == "" && creds.APIKey == "" {
		return fmt.Errorf("validation failed for %s: no access token", c.id)
	}
	base := strings.TrimSuffix(c.baseURL(creds), "/chat/completions")
	if _, err := doJSONMethod(ctx, http.MethodGet, c.id, "validate", joinURL(base, "models"), nil, c.headers(creds, false)); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}

func (c *GitHubCopilot) headers(creds core.Credentials, stream bool) map[string]string {
	token := creds.AccessToken
	if token == "" {
		token = creds.APIKey
	}
	accept := "application/json"
	if stream {
		accept = "text/event-stream"
	}
	h := map[string]string{
		"Authorization":                       bearer(token),
		"copilot-integration-id":              "vscode-chat",
		"editor-version":                      "vscode/" + copilotVSCodeVersion,
		"editor-plugin-version":               "copilot-chat/" + copilotChatVersion,
		"user-agent":                          copilotUserAgent,
		"openai-intent":                       "conversation-panel",
		"x-github-api-version":                copilotAPIVersion,
		"x-request-id":                        uuid.NewString(),
		"x-vscode-user-agent-library-version": "electron-fetch",
		"X-Initiator":                         "user",
		"Accept":                              accept,
	}
	return mergeHeaders(h, creds.Headers)
}

var (
	reMaxCompletionModels = regexp.MustCompile(`(?i)gpt-5|o[134]-`)
	reNoTemperature       = regexp.MustCompile(`(?i)gpt-5\.4`)
	reClaude              = regexp.MustCompile(`(?i)claude`)
)

// transformBody applies Copilot's model-specific request adjustments to the
// rendered OpenAI body, mirroring GithubExecutor.transformRequest.
func (c *GitHubCopilot) transformBody(model string, body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	changed := false

	// gpt-5 / o-series require max_completion_tokens instead of max_tokens.
	if reMaxCompletionModels.MatchString(model) {
		if v, ok := m["max_tokens"]; ok {
			m["max_completion_tokens"] = v
			delete(m, "max_tokens")
			changed = true
		}
	}
	// Some models (gpt-5.4) reject temperature.
	if reNoTemperature.MatchString(model) {
		if _, ok := m["temperature"]; ok {
			delete(m, "temperature")
			changed = true
		}
	}
	// Claude models on Copilot reject Claude-style thinking payloads.
	if reClaude.MatchString(model) {
		if _, ok := m["thinking"]; ok {
			delete(m, "thinking")
			changed = true
		}
	}
	// "none" reasoning_effort is unsupported by some models.
	if m["reasoning_effort"] == "none" {
		delete(m, "reasoning_effort")
		changed = true
	}

	if !changed {
		return body
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// Chat performs a non-streaming Copilot completion. Codex-style models that
// Copilot only exposes on /responses are routed there (directly when cached,
// or after a /chat/completions 400 that signals the model needs /responses).
func (c *GitHubCopilot) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	if c.knowsResponses(req.Model) && c.supportsResponsesEndpoint(req.Model) {
		return c.chatViaResponses(ctx, req, creds)
	}

	req.Stream = false
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	body = c.transformBody(req.Model, body)

	respBody, err := doJSON(ctx, c.id, req.Model, c.endpoint(creds), body, c.headers(creds, false))
	if err != nil {
		if c.supportsResponsesEndpoint(req.Model) && isResponsesEscalation(err) {
			c.markResponses(req.Model)
			return c.chatViaResponses(ctx, req, creds)
		}
		return nil, err
	}

	resp, err := c.codec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// Stream performs a streaming Copilot completion. Codex-style models cached as
// /responses-only are streamed through the Responses endpoint. A first-token
// 400 escalation is not possible mid-stream, so the cache (populated by Chat or
// a prior stream attempt) is what routes streams to /responses.
func (c *GitHubCopilot) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	if c.knowsResponses(req.Model) && c.supportsResponsesEndpoint(req.Model) {
		return c.streamViaResponses(ctx, req, creds, cfg)
	}

	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	body = c.transformBody(req.Model, body)

	resp, err := openStream(ctx, c.id, req.Model, c.endpoint(creds), body, c.headers(creds, true))
	if err != nil {
		// Escalate to /responses when the chat/completions endpoint reports the
		// model is only served there, then cache for subsequent calls.
		if c.supportsResponsesEndpoint(req.Model) && isResponsesEscalation(err) {
			c.markResponses(req.Model)
			return c.streamViaResponses(ctx, req, creds, cfg)
		}
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		return c.codec.ParseStreamLine(payload, req.Model)
	})
	return out, nil
}

// ---- /responses endpoint routing -------------------------------------------

// responsesURL returns the Copilot /responses endpoint, derived from the chat
// base by swapping the trailing /chat/completions (or appending /responses).
func (c *GitHubCopilot) responsesURL(creds core.Credentials) string {
	base := c.baseURL(creds)
	base = strings.TrimSuffix(base, "/chat/completions")
	return joinURL(base, "responses")
}

// supportsResponsesEndpoint reports whether a model can be served by Copilot's
// /responses endpoint. It only serves OpenAI (gpt/codex) models — Gemini and
// Claude reject with a 400 "does not support Responses API", so they must never
// be escalated there.
func (c *GitHubCopilot) supportsResponsesEndpoint(model string) bool {
	m := strings.ToLower(model)
	return !strings.Contains(m, "gemini") && !strings.Contains(m, "claude")
}

func (c *GitHubCopilot) knowsResponses(model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.codexModels[model]
}

func (c *GitHubCopilot) markResponses(model string) {
	c.mu.Lock()
	c.codexModels[model] = true
	c.mu.Unlock()
}

// isResponsesEscalation reports whether a /chat/completions error indicates the
// model must be served via /responses instead. Copilot returns a 400 whose body
// mentions the model is not accessible via /chat/completions or is unsupported.
func isResponsesEscalation(err error) bool {
	pe := core.AsProviderError(err)
	if pe.StatusCode != 400 {
		return false
	}
	msg := strings.ToLower(pe.Message)
	return strings.Contains(msg, "not accessible via the /chat/completions endpoint") ||
		strings.Contains(msg, "the requested model is not supported")
}

// chatViaResponses runs a non-streaming completion through the /responses
// endpoint, translating the request and response between dialects.
func (c *GitHubCopilot) chatViaResponses(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	req.Stream = false
	body, err := c.responsesCodec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	respBody, err := doJSON(ctx, c.id, req.Model, c.responsesURL(creds), body, c.headers(creds, false))
	if err != nil {
		return nil, err
	}

	resp, err := c.responsesCodec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// streamViaResponses streams a completion through the /responses endpoint,
// parsing the typed Responses SSE event stream into canonical chunks.
func (c *GitHubCopilot) streamViaResponses(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	body, err := c.responsesCodec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	resp, err := openStream(ctx, c.id, req.Model, c.responsesURL(creds), body, c.headers(creds, true))
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		return c.responsesCodec.ParseStreamLine(payload, req.Model)
	})
	return out, nil
}
