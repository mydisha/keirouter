package connectors

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// CommandCode drives the Command Code /alpha/generate endpoint. The base URL
// points directly at the generate endpoint, so the connector POSTs to it as-is.
// The request is a wrapped generate envelope (built by the codec) and the
// response is an AI SDK v5 NDJSON event stream (bare JSON objects per line, no
// SSE "data:" framing), so each line is handed straight to the codec.
type CommandCode struct {
	id          string
	defaultBase string
	codec       transform.CommandCodeCodec
}

const (
	commandCodeVersion       = "0.25.7"
	commandCodeCLIEnv        = "cli"
	commandCodeVersionHeader = "x-command-code-version"
	commandCodeCLIEnvHeader  = "x-cli-environment"
	commandCodeSessionHeader = "x-session-id"
)

// NewCommandCode builds a Command Code connector.
func NewCommandCode(id, defaultBaseURL string) *CommandCode {
	return &CommandCode{id: id, defaultBase: defaultBaseURL}
}

func (c *CommandCode) ID() string            { return c.id }
func (c *CommandCode) Dialect() core.Dialect { return core.DialectCommandCode }

func (c *CommandCode) baseURL(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return creds.BaseURL
	}
	return c.defaultBase
}

func (c *CommandCode) headers(creds core.Credentials) map[string]string {
	h := map[string]string{
		commandCodeVersionHeader: commandCodeVersion,
		commandCodeCLIEnvHeader:  commandCodeCLIEnv,
		commandCodeSessionHeader: uuid.NewString(),
	}
	switch {
	case creds.AccessToken != "":
		h["Authorization"] = bearer(creds.AccessToken)
	case creds.APIKey != "":
		h["Authorization"] = bearer(creds.APIKey)
	}
	return mergeHeaders(h, creds.Headers)
}

// Validate confirms a credential is valid by sending a minimal generate probe
// to the upstream endpoint. The probe uses the cheapest model with max_tokens=1
// so quota consumption is negligible.
func (c *CommandCode) Validate(ctx context.Context, creds core.Credentials) error {
	if creds.AccessToken == "" && creds.APIKey == "" {
		return fmt.Errorf("validation failed for %s: no access token or API key", c.id)
	}

	maxTokens := 1
	req := &core.ChatRequest{
		Model:     "deepseek/deepseek-v4-flash",
		Stream:    true,
		MaxTokens: &maxTokens,
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "ping"}}},
		},
	}
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}

	_, err = doJSON(ctx, c.id, "validate", c.baseURL(creds), body, c.headers(creds))
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}

// Chat performs a unary client call by folding the upstream event stream into a
// single canonical response.
func (c *CommandCode) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	respBody, err := doJSON(ctx, c.id, req.Model, c.baseURL(creds), body, c.headers(creds))
	if err != nil {
		return nil, err
	}

	resp, err := c.codec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// Stream performs a streaming generate call. Command Code emits NDJSON, so each
// scanned line is passed directly to the codec (no SSE "data:" framing).
func (c *CommandCode) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	resp, err := openStream(ctx, c.id, req.Model, c.baseURL(creds), body, c.headers(creds))
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	// NDJSON: pass each raw line straight to the codec (it tolerates an
	// optional "data:" prefix).
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, ndjsonLines, func(payload []byte) ([]core.StreamChunk, error) {
		return c.codec.ParseStreamLine(payload, req.Model)
	})
	return out, nil
}
