package connectors

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// Vertex drives Google Cloud Vertex AI's Gemini models. It reuses the Gemini
// wire codec (Vertex speaks the same generateContent body) but differs in
// transport: authentication is either a Service-Account JSON (minted into a
// short-lived Bearer token via the JWT-bearer flow, using a project-scoped URL)
// or a raw API key (passed as ?key= on the global publishers endpoint).
type Vertex struct {
	id          string
	defaultBase string
	codec       transform.GeminiCodec
}

// NewVertex builds a Vertex connector.
func NewVertex(id, defaultBaseURL string) *Vertex {
	base := defaultBaseURL
	if base == "" {
		base = "https://aiplatform.googleapis.com"
	}
	return &Vertex{id: id, defaultBase: base}
}

func (c *Vertex) ID() string            { return c.id }
func (c *Vertex) Dialect() core.Dialect { return core.DialectVertex }

func (c *Vertex) baseURL(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return creds.BaseURL
	}
	return c.defaultBase
}

// resolve determines the auth mode (SA JSON vs raw key), minting a Bearer token
// when a service account is supplied. It returns the bearer token (empty for
// raw-key mode), the raw key (empty for SA mode), and the project id/location.
func (c *Vertex) resolve(ctx context.Context, creds core.Credentials) (bearerTok, rawKey, projectID, location string, err error) {
	location = creds.Extra["location"]
	if location == "" {
		location = "us-central1"
	}
	projectID = creds.Extra["project_id"]

	// An OAuth access token (from the dispatcher's refresher) takes precedence.
	if creds.AccessToken != "" {
		return creds.AccessToken, "", projectID, location, nil
	}

	if sa, ok := parseVertexSAJSON(creds.APIKey); ok {
		tok, mErr := mintVertexToken(ctx, sa)
		if mErr != nil {
			return "", "", "", "", mErr
		}
		if projectID == "" {
			projectID = sa.ProjectID
		}
		return tok, "", projectID, location, nil
	}

	// Raw API key.
	return "", creds.APIKey, projectID, location, nil
}

// buildURL constructs the Vertex endpoint, mirroring VertexExecutor.buildUrl.
func (c *Vertex) buildURL(base, model, action, bearerTok, rawKey, projectID, location string, stream bool) string {
	base = strings.TrimRight(base, "/")
	model = url.PathEscape(model)

	// SA JSON / OAuth bearer: project-scoped path (avoids RESOURCE_PROJECT_INVALID).
	if bearerTok != "" && projectID != "" {
		u := base + "/v1/projects/" + projectID + "/locations/" + location +
			"/publishers/google/models/" + model + ":" + action
		if stream {
			u += "?alt=sse"
		}
		return u
	}

	// Raw API key: global publishers endpoint with ?key= param.
	u := base + "/v1/publishers/google/models/" + model + ":" + action
	if stream {
		u += "?alt=sse"
	}
	if rawKey != "" {
		if stream {
			u += "&key=" + url.QueryEscape(rawKey)
		} else {
			u += "?key=" + url.QueryEscape(rawKey)
		}
	}
	return u
}

func (c *Vertex) headers(bearerTok string) map[string]string {
	h := map[string]string{}
	if bearerTok != "" {
		h["Authorization"] = bearer(bearerTok)
	}
	return h
}

// Validate resolves the credential (minting a token from a service-account JSON
// when supplied) and lists models to confirm it is accepted. A 401/403 means
// the key or service account is rejected.
func (c *Vertex) Validate(ctx context.Context, creds core.Credentials) error {
	if creds.APIKey == "" && creds.AccessToken == "" {
		return fmt.Errorf("validation failed for %s: no API key or access token", c.id)
	}
	bearerTok, rawKey, projectID, location, err := c.resolve(ctx, creds)
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}

	base := strings.TrimRight(c.baseURL(creds), "/")
	var listURL string
	if bearerTok != "" && projectID != "" {
		listURL = base + "/v1/projects/" + projectID + "/locations/" + location +
			"/publishers/google/models"
	} else {
		listURL = base + "/v1/publishers/google/models"
		if rawKey != "" {
			listURL += "?key=" + url.QueryEscape(rawKey)
		}
	}
	if _, err := doJSONMethod(ctx, http.MethodGet, c.id, "validate", listURL, nil, c.headers(bearerTok)); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}

// Chat performs a non-streaming Vertex generateContent call.
func (c *Vertex) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	req.Stream = false
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	bearerTok, rawKey, projectID, location, err := c.resolve(ctx, creds)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	u := c.buildURL(c.baseURL(creds), req.Model, "generateContent", bearerTok, rawKey, projectID, location, false)
	respBody, err := doJSON(ctx, c.id, req.Model, u, body, c.headers(bearerTok))
	if err != nil {
		return nil, err
	}

	resp, err := c.codec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// Stream performs a streaming Vertex streamGenerateContent call (SSE).
func (c *Vertex) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	body, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	bearerTok, rawKey, projectID, location, err := c.resolve(ctx, creds)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	u := c.buildURL(c.baseURL(creds), req.Model, "streamGenerateContent", bearerTok, rawKey, projectID, location, true)
	resp, err := openStream(ctx, c.id, req.Model, u, body, c.headers(bearerTok))
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		return c.codec.ParseStreamLine(payload, req.Model)
	})
	return out, nil
}
