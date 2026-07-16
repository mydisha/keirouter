package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// OpenAICompatible drives any endpoint that speaks the OpenAI Chat Completions
// API: OpenAI itself, plus GLM, MiniMax, DeepSeek, Groq, Together, and custom
// gateways. The provider id and default base URL are supplied at construction
// so one implementation backs many registered providers.
type OpenAICompatible struct {
	id          string
	defaultBase string
	codec       transform.OpenAICodec
}

// NewOpenAICompatible builds a connector for an OpenAI-compatible provider.
func NewOpenAICompatible(id, defaultBaseURL string) *OpenAICompatible {
	return &OpenAICompatible{id: id, defaultBase: defaultBaseURL}
}

func (c *OpenAICompatible) ID() string            { return c.id }
func (c *OpenAICompatible) Dialect() core.Dialect { return core.DialectOpenAI }

func (c *OpenAICompatible) baseURL(creds core.Credentials) string {
	u := c.defaultBase
	if creds.BaseURL != "" {
		u = creds.BaseURL
	}
	// Resolve template placeholders like {accountId} from creds.Extra.
	// Cloudflare Workers AI uses: /accounts/{accountId}/ai/v1/chat/completions
	for key, val := range creds.Extra {
		u = strings.ReplaceAll(u, "{"+key+"}", val)
	}
	return u
}

func (c *OpenAICompatible) headers(creds core.Credentials) map[string]string {
	h := map[string]string{}
	if c.id == "azure" {
		switch {
		case creds.AccessToken != "":
			h["Authorization"] = bearer(creds.AccessToken)
		case creds.APIKey != "":
			h["api-key"] = creds.APIKey
		}
		if org := creds.Extra["organization"]; org != "" {
			h["OpenAI-Organization"] = org
		}
		return mergeHeaders(h, creds.Headers)
	}

	// Cline requires a workos: prefix on the access token and custom headers.
	if c.id == "cline" {
		tok := creds.AccessToken
		if tok == "" {
			tok = creds.APIKey
		}
		if tok != "" && !strings.HasPrefix(tok, "workos:") {
			tok = "workos:" + tok
		}
		h["Authorization"] = bearer(tok)
		h["HTTP-Referer"] = "https://cline.bot"
		h["X-Title"] = "Cline"
		h["X-CLIENT-TYPE"] = "keirouter"
		h["X-PLATFORM"] = "unknown"
		h["X-IS-MULTIROOT"] = "false"
		return mergeHeaders(h, creds.Headers)
	}

	// CodeBuddy requires CLI headers on every request.
	if c.id == "codebuddy" {
		tok := creds.AccessToken
		if tok == "" {
			tok = creds.APIKey
		}
		if tok != "" {
			h["Authorization"] = bearer(tok)
		}
		h["User-Agent"] = "CLI/2.108.1 CodeBuddy/2.108.1"
		h["X-Product"] = "SaaS"
		h["X-IDE-Type"] = "CLI"
		h["X-IDE-Name"] = "CLI"
		h["X-Requested-With"] = "XMLHttpRequest"
		h["x-codebuddy-request"] = "1"
		return mergeHeaders(h, creds.Headers)
	}

	// AgentRouter restricts upstream access to a known set of CLI/IDE tools
	// (Claude Code CLI, Cline, Roo Code, Kilo Code, Qwen Code, OpenCode, etc.).
	// Impersonate Claude Code CLI so outbound requests pass the endpoint
	// allowlist. Auth mirrors the default Bearer path below.
	if c.id == "agentrouter" {
		switch {
		case creds.AccessToken != "":
			h["Authorization"] = bearer(creds.AccessToken)
		case creds.APIKey != "":
			h["Authorization"] = bearer(creds.APIKey)
		}
		h["User-Agent"] = "claude-cli/1.0.95 (external, cli)"
		h["x-app"] = "cli"
		h["anthropic-version"] = "2023-06-01"
		h["anthropic-beta"] = "fine-grained-tool-streaming-2025-05-14"
		return mergeHeaders(h, creds.Headers)
	}

	// Kimchi requires a custom User-Agent.
	if c.id == "kimchi" {
		tok := creds.AccessToken
		if tok == "" {
			tok = creds.APIKey
		}
		if tok != "" {
			h["Authorization"] = bearer(tok)
		}
		h["User-Agent"] = "kimchi/0.1.50"
		return mergeHeaders(h, creds.Headers)
	}

	switch {
	case creds.AccessToken != "":
		h["Authorization"] = bearer(creds.AccessToken)
	case creds.APIKey != "":
		h["Authorization"] = bearer(creds.APIKey)
	}
	return mergeHeaders(h, creds.Headers)
}

func (c *OpenAICompatible) chatCompletionsURL(creds core.Credentials, model string) string {
	if c.id == "azure" {
		endpoint := strings.TrimRight(creds.Extra["azure_endpoint"], "/")
		if endpoint == "" {
			endpoint = strings.TrimRight(creds.BaseURL, "/")
		}
		deployment := creds.Extra["deployment"]
		if deployment == "" {
			deployment = model
		}
		if deployment == "" {
			deployment = "gpt-4"
		}
		apiVersion := creds.Extra["api_version"]
		if apiVersion == "" {
			apiVersion = "2024-10-01-preview"
		}
		return endpoint + "/openai/deployments/" + url.PathEscape(deployment) +
			"/chat/completions?api-version=" + url.QueryEscape(apiVersion)
	}
	return joinURL(c.baseURL(creds), "chat/completions")
}

// providerRequiresStreaming reports whether the given provider only accepts
// streaming requests. Some OpenAI-compatible providers reject non-streaming
// requests with "Stream must be set to true" (HTTP 400). For these providers,
// the Chat method must use the Stream method internally and drain the chunks
// into a single response.
func providerRequiresStreaming(providerID string) bool {
	switch providerID {
	// Add providers here that require streaming-only requests.
	// These providers return 400 "Stream must be set to true" when
	// a non-streaming request is sent.
	case "xiaomi-mimo", "xiaomi-tokenplan", "mimo-free", "codebuddy":
		return true
	default:
		return false
	}
}

// isStreamRequiredError reports whether the error indicates the upstream
// provider rejected a non-streaming request with "Stream must be set to true".
// This is used for auto-retry with streaming when an unknown (custom) OpenAI-
// compatible provider requires streaming.
func isStreamRequiredError(err error) bool {
	if err == nil {
		return false
	}
	pe := core.AsProviderError(err)
	if pe == nil || pe.StatusCode != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(pe.Message)
	return strings.Contains(msg, "stream must be set to true") ||
		strings.Contains(msg, "streaming is required") ||
		strings.Contains(msg, "stream parameter is required")
}

// drainStreamToResponse consumes a stream channel and folds the chunks into a
// single ChatResponse. Used by Chat when the provider requires streaming.
func drainStreamToResponse(stream <-chan core.StreamChunk, model string) (*core.ChatResponse, error) {
	msg := core.Message{Role: core.RoleAssistant}
	var text, thinking string
	toolCalls := map[string]*core.ToolCall{}
	var toolOrder []string
	finish := core.FinishStop
	var usage core.Usage

	for ch := range stream {
		switch ch.Type {
		case core.ChunkText:
			text += ch.Delta
		case core.ChunkThinking:
			thinking += ch.Delta
		case core.ChunkToolCall:
			if ch.ToolCall != nil {
				existing, ok := toolCalls[ch.ToolCall.ID]
				if !ok {
					tc := *ch.ToolCall
					toolCalls[ch.ToolCall.ID] = &tc
					toolOrder = append(toolOrder, ch.ToolCall.ID)
				} else if len(ch.ToolCall.Arguments) > 0 {
					existing.Arguments = append(existing.Arguments, ch.ToolCall.Arguments...)
				}
				finish = core.FinishToolCalls
			}
		case core.ChunkFinish:
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
		case core.ChunkUsage:
			if ch.Usage != nil {
				usage = *ch.Usage
			}
		case core.ChunkError:
			if ch.Err != nil {
				return nil, ch.Err
			}
		}
	}

	if thinking != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: thinking})
	}
	if text != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: text})
	}
	for _, id := range toolOrder {
		tc := toolCalls[id]
		if len(tc.Arguments) == 0 {
			tc.Arguments = json.RawMessage("{}")
		}
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: tc})
	}

	return &core.ChatResponse{Model: model, Message: msg, FinishReason: finish, Usage: usage}, nil
}

func (c *OpenAICompatible) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	// Some OpenAI-compatible providers only accept streaming requests and reject
	// non-streaming requests with 400 "Stream must be set to true". For known
	// stream-only providers, use the Stream method directly. For unknown
	// providers (including custom OpenAI-compatible ones), the initial non-
	// streaming call may fail with this error — we detect it and retry with
	// streaming transparently.
	if providerRequiresStreaming(c.id) {
		stream, err := c.Stream(ctx, req, creds, core.StreamConfig{})
		if err != nil {
			return nil, err
		}
		return drainStreamToResponse(stream, req.Model)
	}

	req.Stream = false
	body, err := c.codec.RenderRequestForProvider(req, c.id)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	url := c.chatCompletionsURL(creds, req.Model)

	// Use streaming JSON decode when the codec supports it — avoids buffering
	// the entire response body into a []byte before parsing.
	if sc, ok := interface{}(c.codec).(transform.StreamingResponseCodec); ok {
		_, respBody, decErr := doJSONDecode(ctx, c.id, req.Model, url, body, c.headers(creds))
		if decErr != nil {
			// Auto-retry with streaming if the provider requires it.
			if isStreamRequiredError(decErr) {
				stream, sErr := c.Stream(ctx, req, creds, core.StreamConfig{})
				if sErr != nil {
					return nil, sErr
				}
				return drainStreamToResponse(stream, req.Model)
			}
			return nil, decErr
		}
		defer respBody.Close()
		resp, perr := sc.ParseResponseFrom(respBody, req.Model)
		if perr != nil {
			return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: perr.Error(), Cause: perr}
		}
		return resp, nil
	}

	// Fallback: buffer the entire response body.
	respBody, err := doJSON(ctx, c.id, req.Model, url, body, c.headers(creds))
	if err != nil {
		// Auto-retry with streaming if the provider requires it.
		if isStreamRequiredError(err) {
			stream, sErr := c.Stream(ctx, req, creds, core.StreamConfig{})
			if sErr != nil {
				return nil, sErr
			}
			return drainStreamToResponse(stream, req.Model)
		}
		return nil, err
	}

	resp, err := c.codec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// Validate probes the upstream /models endpoint to confirm the credentials are
// accepted. Returns nil on success.
func (c *OpenAICompatible) Validate(ctx context.Context, creds core.Credentials) error {
	if c.id == "azure" {
		body := []byte(`{"messages":[{"role":"user","content":"ping"}],"max_tokens":1}`)
		err := validateProbe(ctx, c.id, c.chatCompletionsURL(creds, "validate"), body, c.headers(creds))
		if err != nil {
			return fmt.Errorf("validation failed for %s: %w", c.id, err)
		}
		return nil
	}

	url := joinURL(c.baseURL(creds), "models")
	_, err := doJSONMethod(ctx, http.MethodGet, c.id, "validate", url, nil, c.headers(creds))
	// An HTML response means the base URL points at a web frontend, not the API
	// (e.g. a custom base URL missing the "/v1" path). Fail hard rather than
	// falling through to the chat probe, which is skipped for custom providers
	// and would otherwise report a false-positive success.
	if isNonJSONResponseError(err) {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	if err == nil {
		// GET /models reached the upstream. For no-auth accounts (e.g. a local
		// gateway) reachability is all we can verify. For strict providers the
		// /models endpoint itself requires the key, so a 200 proves it is valid.
		// For any other keyed account a 200 is NOT proof — many OpenAI-compatible
		// providers list models without checking auth — so confirm the key with
		// an authenticated chat probe before reporting success.
		hasKey := strings.TrimSpace(creds.APIKey) != "" || strings.TrimSpace(creds.AccessToken) != ""
		if !hasKey || strictModelsValidation(c.id) {
			return nil
		}
		if perr := c.chatAuthProbe(ctx, creds); perr != nil {
			return fmt.Errorf("validation failed for %s: %w", c.id, perr)
		}
		return nil
	}
	if c.id == "xai" {
		pe := core.AsProviderError(err)
		if pe.StatusCode == http.StatusForbidden {
			return nil
		}
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	if strictModelsValidation(c.id) {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	if validationAuthError(err) || !validationReachedUpstream(err) {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}

	// Many OpenAI-compatible providers either omit /models or reject unknown
	// probe models with 400/404 while still accepting the credential. Fall back
	// to a minimal chat request and treat any non-auth HTTP response as proof
	// that the connection reached the provider.
	if err := c.chatAuthProbe(ctx, creds); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}

// chatAuthProbe issues a minimal, near-zero-cost chat request (max_tokens=1) to
// confirm the credential is actually accepted by the upstream. It relies on
// validateProbe semantics: only an auth failure (401/403) or a transport error
// that never reached the provider counts as a failure. A non-auth HTTP response
// (e.g. a 400/404 for an unknown probe model) still proves the key was accepted.
func (c *OpenAICompatible) chatAuthProbe(ctx context.Context, creds core.Credentials) error {
	probeModel := firstCatalogModel(c.id)
	// Custom/dynamic providers may have no catalog models until the user imports
	// them. Sending a synthetic "test" model id to such endpoints triggers
	// upstream403 model_not_allowed, which is misclassified as an auth failure.
	// When no real model is known, skip the chat probe for custom providers and
	// rely on the GET /models probe above. Built-in providers keep the "test"
	// fallback so a bad key is still rejected when /models is publicly readable.
	if probeModel == "" {
		if IsCustomProviderID(c.id) {
			return nil
		}
		probeModel = "test"
	}
	body, _ := json.Marshal(map[string]any{
		"model": probeModel,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
		"max_tokens": 1,
		"stream":     false,
	})
	return validateProbe(ctx, c.id, c.chatCompletionsURL(creds, probeModel), body, c.headers(creds))
}

func validateProbe(ctx context.Context, provider, endpoint string, body []byte, headers map[string]string) error {
	_, err := doJSON(ctx, provider, "validate", endpoint, body, headers)
	if err == nil {
		return nil
	}
	if isNonJSONResponseError(err) || validationAuthError(err) || !validationReachedUpstream(err) {
		return err
	}
	return nil
}

func validationAuthError(err error) bool {
	return core.AsProviderError(err).Kind == core.ErrAuth
}

func validationReachedUpstream(err error) bool {
	return core.AsProviderError(err).StatusCode > 0
}

func firstCatalogModel(provider string) string {
	for _, m := range ModelsForProvider(provider) {
		if m.Kind == core.ServiceLLM {
			return m.ID
		}
	}
	return ""
}

func strictModelsValidation(provider string) bool {
	switch provider {
	case "openai", "openrouter", "vercel-ai-gateway",
		"deepseek", "groq", "mistral", "perplexity", "together",
		"fireworks", "cerebras", "cohere", "nebius", "siliconflow",
		"hyperbolic", "chutes", "nvidia", "xiaomi-mimo", "xiaomi-tokenplan":
		return true
	default:
		return false
	}
}

// OpenAICompatibleModelSource implements LiveModelSource by fetching the
// upstream GET /models endpoint — the standard discovery API for OpenAI-
// compatible providers. This auto-discovers models at runtime using the
// connected account's credentials.
type OpenAICompatibleModelSource struct {
	provider    string
	defaultBase string
}

// ListModels fetches GET /models from the upstream and returns ModelSpecs.
func (s *OpenAICompatibleModelSource) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	base := s.defaultBase
	if creds.BaseURL != "" {
		base = creds.BaseURL
	}
	// Resolve template placeholders (e.g. cloudflare {accountId}).
	for key, val := range creds.Extra {
		base = strings.ReplaceAll(base, "{"+key+"}", val)
	}

	url := joinURL(base, "models")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("GET /models returned %d: %s", resp.StatusCode, truncateError(body))
	}

	// Parse the standard OpenAI models response shape.
	var envelope struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode /models response: %w", err)
	}

	out := make([]ModelSpec, 0, len(envelope.Data))
	for _, entry := range envelope.Data {
		if entry.ID == "" {
			continue
		}
		out = append(out, ModelSpec{
			ID:   entry.ID,
			Name: entry.ID, // best-effort; static catalog may have a better name
			Kind: core.ServiceLLM,
		})
	}
	return out, nil
}

// StreamRaw opens a streaming SSE connection and returns the raw response body
// for zero-copy same-dialect piping. The caller must close body when done.
func (c *OpenAICompatible) StreamRaw(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (io.ReadCloser, http.Header, error) {
	req.Stream = true
	body, err := c.codec.RenderRequestForProvider(req, c.id)
	if err != nil {
		return nil, nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	url := c.chatCompletionsURL(creds, req.Model)
	resp, err := openStream(ctx, c.id, req.Model, url, body, c.headers(creds))
	if err != nil {
		return nil, nil, err
	}
	return resp.Body, resp.Header, nil
}

// Stream performs a streaming completion, emitting canonical chunks.
func (c *OpenAICompatible) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	body, err := c.codec.RenderRequestForProvider(req, c.id)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	url := c.chatCompletionsURL(creds, req.Model)
	resp, err := openStream(ctx, c.id, req.Model, url, body, c.headers(creds))
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()

		ttft := newTTFTTracker(cfg)

		scanner := sseScanner(resp.Body)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			payload, ok := parseSSEData(scanner.Text())
			if !ok {
				continue
			}
			chunks, perr := c.codec.ParseStreamLine([]byte(payload), req.Model)
			if perr != nil {
				// Skip a single malformed chunk rather than aborting the stream.
				continue
			}
			for _, ch := range chunks {
				ttft.maybeReport(ch)
				select {
				case out <- ch:
				case <-ctx.Done():
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- core.StreamChunk{
				Type: core.ChunkError,
				Err:  &core.ProviderError{Kind: core.ErrTimeout, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err},
			}
		}
	}()
	return out, nil
}
