package connectors

import (
	"context"
	"io"
	"net/http"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// Ollama drives Ollama's native /api/chat endpoint (both Ollama Cloud and a
// local daemon). Ollama differs from OpenAI in two ways the connector handles:
// generation knobs live under "options" (mapped by the codec), and streaming is
// newline-delimited JSON (NDJSON) rather than SSE — so each response line is a
// bare JSON object with no "data:" prefix.
type Ollama struct {
	id          string
	defaultBase string
	codec       transform.OllamaCodec
}

// NewOllama builds an Ollama connector for the given provider id and default
// base URL (e.g. https://ollama.com or http://localhost:11434).
func NewOllama(id, defaultBaseURL string) *Ollama {
	return &Ollama{id: id, defaultBase: defaultBaseURL}
}

func (c *Ollama) ID() string            { return c.id }
func (c *Ollama) Dialect() core.Dialect { return core.DialectOllama }

func (c *Ollama) baseURL(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return creds.BaseURL
	}
	return c.defaultBase
}

func (c *Ollama) headers(creds core.Credentials) map[string]string {
	h := map[string]string{}
	switch {
	case creds.AccessToken != "":
		h["Authorization"] = bearer(creds.AccessToken)
	case creds.APIKey != "":
		h["Authorization"] = bearer(creds.APIKey)
	}
	return mergeHeaders(h, creds.Headers)
}

// Validate probes Ollama's lightweight model-list endpoint. Local Ollama uses
// no auth; Ollama Cloud sends the configured bearer key.
func (c *Ollama) Validate(ctx context.Context, creds core.Credentials) error {
	_, err := doJSONMethod(ctx, http.MethodGet, c.id, "validate", joinURL(c.baseURL(creds), "api/tags"), nil, c.headers(creds))
	return err
}

// Chat performs a non-streaming /api/chat call.
func (c *Ollama) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	req.Stream = false
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	url := joinURL(c.baseURL(creds), "api/chat")
	respBody, err := doJSON(ctx, c.id, req.Model, url, body, c.headers(creds))
	if err != nil {
		return nil, err
	}

	resp, err := c.codec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// StreamRaw opens a streaming connection and returns the raw response body
// for zero-copy same-dialect piping. Ollama uses NDJSON, so the raw body
// is newline-delimited JSON objects.
func (c *Ollama) StreamRaw(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (io.ReadCloser, http.Header, error) {
	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	url := joinURL(c.baseURL(creds), "api/chat")
	resp, err := openStream(ctx, c.id, req.Model, url, body, c.headers(creds))
	if err != nil {
		return nil, nil, err
	}
	return resp.Body, resp.Header, nil
}

// Stream performs a streaming /api/chat call. Ollama emits NDJSON, so each
// scanned line is passed directly to the codec (no SSE "data:" framing).
func (c *Ollama) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	url := joinURL(c.baseURL(creds), "api/chat")
	resp, err := openStream(ctx, c.id, req.Model, url, body, c.headers(creds))
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	// NDJSON: every line is a complete JSON object for the codec.
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, ndjsonLines, func(payload []byte) ([]core.StreamChunk, error) {
		return c.codec.ParseStreamLine(payload, req.Model)
	})
	return out, nil
}
