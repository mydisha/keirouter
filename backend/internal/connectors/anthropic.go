package connectors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// anthropicVersion is the API version header Anthropic requires.
const anthropicVersion = "2023-06-01"

// Anthropic drives the native Anthropic Messages API (/v1/messages). It also
// backs Anthropic-compatible gateways via a custom base URL.
type Anthropic struct {
	id          string
	defaultBase string
	codec       transform.AnthropicCodec
}

// NewAnthropic builds an Anthropic connector.
func NewAnthropic(id, defaultBaseURL string) *Anthropic {
	return &Anthropic{id: id, defaultBase: defaultBaseURL}
}

func (c *Anthropic) ID() string            { return c.id }
func (c *Anthropic) Dialect() core.Dialect { return core.DialectAnthropic }

func (c *Anthropic) baseURL(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return creds.BaseURL
	}
	return c.defaultBase
}

func (c *Anthropic) headers(creds core.Credentials, model string) map[string]string {
	return c.headersFor(creds, model, nil)
}

// headersFor builds the upstream headers, merging the client's anthropic-beta
// flags (context-1m, interleaved-thinking, output-128k, ...) with whatever the
// credential type requires. API-key users otherwise lose the features their
// client relies on because the gateway re-renders the request.
func (c *Anthropic) headersFor(creds core.Credentials, model string, clientBetas []string) map[string]string {
	h := c.baseHeaders(creds, model)
	if len(clientBetas) > 0 {
		h["anthropic-beta"] = mergeBetaFlags(h["anthropic-beta"], clientBetas)
	}
	return h
}

// mergeBetaFlags unions comma-separated beta flags, preserving order and
// dropping duplicates.
func mergeBetaFlags(existing string, extra []string) string {
	seen := make(map[string]struct{}, len(extra)+4)
	var out []string
	add := func(flag string) {
		flag = strings.TrimSpace(flag)
		if flag == "" {
			return
		}
		if _, dup := seen[flag]; dup {
			return
		}
		seen[flag] = struct{}{}
		out = append(out, flag)
	}
	for _, f := range strings.Split(existing, ",") {
		add(f)
	}
	for _, f := range extra {
		add(f)
	}
	return strings.Join(out, ",")
}

func (c *Anthropic) baseHeaders(creds core.Credentials, model string) map[string]string {
	h := map[string]string{"anthropic-version": anthropicVersion}
	// Anthropic uses x-api-key for keys and Authorization: Bearer for OAuth.
	switch {
	case creds.AccessToken != "":
		h["Authorization"] = bearer(creds.AccessToken)
		// Claude subscription OAuth tokens require the full Claude Code CLI
		// fingerprint, or Anthropic rejects/flags them. Merge the spoof headers.
		if isClaudeOAuthToken(creds.AccessToken) {
			h = mergeHeaders(h, claudeCLISpoofHeaders(model))
		}
	case creds.APIKey != "":
		h["x-api-key"] = creds.APIKey
	}
	return mergeHeaders(h, creds.Headers)
}

// Chat performs a non-streaming completion.
func (c *Anthropic) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	req.Stream = false
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	// Cloak the request to look like Claude Code when using a subscription OAuth
	// token. toolNameMap reverses the "_ide" tool renaming on the response.
	body, toolNameMap := applyClaudeCloaking(body, creds.AccessToken)

	url := joinURL(c.baseURL(creds), "messages")
	respBody, err := doJSON(ctx, c.id, req.Model, url, body, c.headersFor(creds, req.Model, req.Metadata.ClientBetas))
	if err != nil {
		return nil, err
	}

	respBody = decloakClaudeToolNames(respBody, toolNameMap)
	resp, err := c.codec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// Validate probes the upstream to confirm the credentials are accepted. It
// tries GET /models first; if that returns a non-auth error (e.g. 404), it
// falls back to a minimal POST /messages probe.
func (c *Anthropic) Validate(ctx context.Context, creds core.Credentials) error {
	base := strings.TrimRight(c.baseURL(creds), "/")
	// Strip /messages suffix if present.
	base = strings.TrimSuffix(base, "/messages")

	// Try GET /models first (cheap, no token cost).
	modelsURL := base + "/models"
	_, err := doJSONMethod(ctx, http.MethodGet, c.id, "validate", modelsURL, nil, c.headers(creds, ""))
	// An HTML response means the base URL points at a web frontend, not the API
	// (e.g. a custom base URL missing the "/v1" path). Fail hard rather than
	// falling through to the messages probe, which is skipped for custom
	// providers and would otherwise report a false-positive success.
	if isNonJSONResponseError(err) {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	if err == nil {
		// The official Anthropic API auth-protects GET /models, so a 200 proves
		// the key. Anthropic-compatible third-party gateways may list models
		// without auth, so confirm those with a minimal messages probe.
		hasKey := strings.TrimSpace(creds.APIKey) != "" || strings.TrimSpace(creds.AccessToken) != ""
		if c.id == "anthropic" || !hasKey {
			return nil
		}
		if perr := c.messagesAuthProbe(ctx, creds); perr != nil {
			return fmt.Errorf("validation failed for %s: %w", c.id, perr)
		}
		return nil
	}
	// If it's an auth error, the key is bad.
	pe := core.AsProviderError(err)
	if pe.Kind == core.ErrAuth {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	// Otherwise /models may not exist (404 etc); fall back to a minimal chat probe.
	if perr := c.messagesAuthProbe(ctx, creds); perr != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, perr)
	}
	return nil
}

// messagesAuthProbe issues a minimal POST /messages request (max_tokens=1) to
// confirm the credential is accepted. Only an auth failure (401/403) or a
// transport error that never reached the provider counts as a failure; a
// non-auth HTTP response (e.g. an unknown probe model) still proves the key was
// accepted.
func (c *Anthropic) messagesAuthProbe(ctx context.Context, creds core.Credentials) error {
	// Custom anthropic-compatible gateways may not expose the canonical Claude
	// model id; probing with it can trip a 403 model_not_allowed that gets
	// misclassified as an auth failure. When no real model is registered for
	// this provider, skip the chat probe — the GET /models probe in Validate
	// already confirmed the key works.
	probeModel := firstCatalogModel(c.id)
	if probeModel == "" {
		if IsCustomProviderID(c.id) {
			return nil
		}
		probeModel = "claude-sonnet-4-20250514"
	}
	chatURL := joinURL(c.baseURL(creds), "messages")
	probeBody := []byte(`{"model":"` + probeModel + `","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)
	_, err := doJSON(ctx, c.id, "validate", chatURL, probeBody, c.headers(creds, probeModel))
	if err == nil {
		return nil
	}
	if isNonJSONResponseError(err) || validationAuthError(err) || !validationReachedUpstream(err) {
		return err
	}
	return nil
}

// StreamRaw opens a streaming SSE connection and returns the raw response body
// for zero-copy same-dialect piping. NOTE: when Claude cloaking is active
// (OAuth tokens), the caller should NOT use direct pipe because tool names
// need decloaking on the response path.
func (c *Anthropic) StreamRaw(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (io.ReadCloser, http.Header, error) {
	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	body, _ = applyClaudeCloaking(body, creds.AccessToken)

	url := joinURL(c.baseURL(creds), "messages")
	resp, err := openStream(ctx, c.id, req.Model, url, body, c.headersFor(creds, req.Model, req.Metadata.ClientBetas))
	if err != nil {
		return nil, nil, err
	}
	return resp.Body, resp.Header, nil
}

// Stream performs a streaming completion. Anthropic emits named SSE events; the
// codec maps each event's data payload to canonical chunks.
func (c *Anthropic) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	body, toolNameMap := applyClaudeCloaking(body, creds.AccessToken)

	url := joinURL(c.baseURL(creds), "messages")
	resp, err := openStream(ctx, c.id, req.Model, url, body, c.headersFor(creds, req.Model, req.Metadata.ClientBetas))
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		chunks, err := c.codec.ParseStreamLine(payload, req.Model)
		if err != nil || len(toolNameMap) == 0 {
			return chunks, err
		}
		for i, ch := range chunks {
			// Decloak streamed tool-call names ("foo_ide" → "foo").
			if ch.Type == core.ChunkToolCall && ch.ToolCall != nil {
				if orig, ok := toolNameMap[ch.ToolCall.Name]; ok {
					chunks[i].ToolCall.Name = orig
				}
			}
		}
		return chunks, nil
	})
	return out, nil
}
