package connectors

import (
	"context"
	"encoding/json"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// oaiEmbeddingRequest is the OpenAI embeddings wire request.
type oaiEmbeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// oaiEmbeddingResponse is the OpenAI embeddings wire response.
type oaiEmbeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embeddings produces vector embeddings via the OpenAI-compatible endpoint.
// Implementing this makes OpenAICompatible satisfy core.MediaConnector.
func (c *OpenAICompatible) Embeddings(ctx context.Context, req *core.EmbeddingRequest, creds core.Credentials) (*core.EmbeddingResponse, error) {
	body, err := json.Marshal(oaiEmbeddingRequest{
		Model:      req.Model,
		Input:      req.Input,
		Dimensions: req.Dimensions,
	})
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	headers, err := c.authHeaders(ctx, creds)
	if err != nil {
		return nil, err
	}
	respBody, err := doJSON(ctx, c.id, req.Model, c.endpointURL(creds, "embeddings"), body, headers)
	if err != nil {
		return nil, err
	}

	var raw oaiEmbeddingResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: "parse embeddings: " + err.Error(), Cause: err}
	}

	model := raw.Model
	if model == "" {
		model = req.Model
	}
	out := &core.EmbeddingResponse{
		Model:   model,
		Vectors: make([][]float32, len(raw.Data)),
		Usage: core.Usage{
			PromptTokens: raw.Usage.PromptTokens,
			TotalTokens:  raw.Usage.TotalTokens,
		},
	}
	// Preserve input order via the index field.
	for _, d := range raw.Data {
		if d.Index >= 0 && d.Index < len(out.Vectors) {
			out.Vectors[d.Index] = d.Embedding
		}
	}
	return out, nil
}
