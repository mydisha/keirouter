package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// InvokeModel paths: Claude through its native Messages body (model ids
// prefixed "invoke/"), and the embedding models, which have no Converse form.

// bedrockAnthropicVersion is the Messages API version Bedrock expects in the
// InvokeModel body (in place of the anthropic-version header).
const bedrockAnthropicVersion = "bedrock-2023-05-31"

func (c *Bedrock) requireInvokeAnthropic(modelID string) error {
	if bedrockFamilyOf(modelID).anthropic {
		return nil
	}
	return bedrockBadRequest(c.id, bedrockInvokePrefix+modelID,
		"the invoke/ model prefix is only supported for Anthropic Claude models; use the plain model id for the Converse API")
}

// renderInvokeAnthropic renders req as an Anthropic Messages body for
// InvokeModel: the codec output minus model/stream, plus anthropic_version
// and the accepted beta flags. Remote attachments are inlined because
// Bedrock does not fetch URL sources.
func (c *Bedrock) renderInvokeAnthropic(ctx context.Context, req *core.ChatRequest, modelID string) ([]byte, error) {
	clone := *req
	clone.Model = bedrockClaudeName(modelID)
	msgs, err := c.inlineRemoteMedia(ctx, req.Messages, req.Model)
	if err != nil {
		return nil, err
	}
	clone.Messages = msgs
	raw, err := c.codec.RenderRequest(&clone)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	delete(body, "model")
	delete(body, "stream")
	body["anthropic_version"] = json.RawMessage(strconv.Quote(bedrockAnthropicVersion))
	if betas := bedrockAnthropicBetas(req.Metadata.ClientBetas); len(betas) > 0 {
		body["anthropic_beta"], _ = json.Marshal(betas)
	}
	return json.Marshal(body)
}

// inlineRemoteMedia returns msgs with http(s) attachments replaced by inline
// base64 data. Messages without remote media are shared, not copied, and the
// caller's request is never mutated.
func (c *Bedrock) inlineRemoteMedia(ctx context.Context, msgs []core.Message, model string) ([]core.Message, error) {
	remote := func(m *core.MediaPayload) bool {
		return m != nil && m.Data == "" && (strings.HasPrefix(m.URL, "http://") || strings.HasPrefix(m.URL, "https://"))
	}
	inline := func(m *core.MediaPayload) (*core.MediaPayload, error) {
		bm, err := resolveBedrockMedia(ctx, c.id, model, m)
		if err != nil {
			return nil, err
		}
		return &core.MediaPayload{MIMEType: bm.MIME, Data: bm.Data}, nil
	}
	var out []core.Message
	for mi, msg := range msgs {
		var content []core.ContentPart
		for pi, p := range msg.Content {
			np := p
			changed := false
			if remote(p.Media) {
				media, err := inline(p.Media)
				if err != nil {
					return nil, err
				}
				np.Media, changed = media, true
			}
			if p.ToolResult != nil {
				var parts []core.ContentPart
				for ri, rp := range p.ToolResult.Parts {
					if !remote(rp.Media) {
						continue
					}
					if parts == nil {
						parts = append([]core.ContentPart(nil), p.ToolResult.Parts...)
					}
					media, err := inline(rp.Media)
					if err != nil {
						return nil, err
					}
					parts[ri].Media = media
				}
				if parts != nil {
					tr := *p.ToolResult
					tr.Parts = parts
					np.ToolResult, changed = &tr, true
				}
			}
			if !changed {
				continue
			}
			if content == nil {
				content = append([]core.ContentPart(nil), msg.Content...)
			}
			content[pi] = np
		}
		if content == nil {
			continue
		}
		if out == nil {
			out = append([]core.Message(nil), msgs...)
		}
		out[mi].Content = content
	}
	if out == nil {
		return msgs, nil
	}
	return out, nil
}

func (c *Bedrock) invokeChat(ctx context.Context, req *core.ChatRequest, creds core.Credentials, modelID string) (*core.ChatResponse, error) {
	if err := c.requireInvokeAnthropic(modelID); err != nil {
		return nil, err
	}
	body, err := c.renderInvokeAnthropic(ctx, req, modelID)
	if err != nil {
		return nil, err
	}
	endpoint, err := c.modelURL(creds, modelID, "invoke")
	if err != nil {
		return nil, err
	}
	respBody, _, err := c.call(ctx, creds, http.MethodPost, endpoint, body, req.Model)
	if err != nil {
		return nil, err
	}
	resp, err := c.codec.ParseResponse(respBody, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	resp.Model = req.Model
	return resp, nil
}

func (c *Bedrock) invokeStream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig, modelID string) (<-chan core.StreamChunk, error) {
	if err := c.requireInvokeAnthropic(modelID); err != nil {
		return nil, err
	}
	body, err := c.renderInvokeAnthropic(ctx, req, modelID)
	if err != nil {
		return nil, err
	}
	endpoint, err := c.modelURL(creds, modelID, "invoke-with-response-stream")
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, creds, http.MethodPost, endpoint, body, bedrockEventStream, req.Model)
	if err != nil {
		return nil, err
	}
	out := make(chan core.StreamChunk, 16)
	go c.pumpEventStream(ctx, req.Model, resp.Body, out, cfg, &invokeStreamState{conn: c, model: req.Model})
	return out, nil
}

// invokeStreamState decodes InvokeModelWithResponseStream "chunk" events,
// whose base64 bytes are Anthropic stream events, through the Anthropic
// codec. The finish chunk is held back until the stream ends so the usage
// reported by the closing events precedes it.
type invokeStreamState struct {
	conn    *Bedrock
	model   string
	held    *core.StreamChunk
	stopped bool
}

func (s *invokeStreamState) frame(f *eventStreamFrame) ([]core.StreamChunk, error) {
	if f.headers[":event-type"] != "chunk" {
		return nil, nil
	}
	var ev struct {
		Bytes string `json:"bytes"`
	}
	if err := json.Unmarshal(f.payload, &ev); err != nil || ev.Bytes == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(ev.Bytes)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest,
			Message: "bedrock: malformed stream chunk: " + err.Error(), Cause: err}
	}
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &head) == nil && head.Type == "message_stop" {
		s.stopped = true
	}
	chunks, err := s.conn.codec.ParseStreamLine(raw, s.model)
	if err != nil {
		return nil, nil // an unparseable event is skipped, like the SSE path
	}
	kept := chunks[:0]
	for _, ch := range chunks {
		if ch.Type == core.ChunkFinish {
			held := ch
			s.held = &held
			continue
		}
		kept = append(kept, ch)
	}
	return kept, nil
}

func (s *invokeStreamState) finish() ([]core.StreamChunk, error) {
	if s.held == nil && !s.stopped {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest,
			Message: "bedrock stream ended before message_stop"}
	}
	if s.held == nil {
		return []core.StreamChunk{{Type: core.ChunkFinish, FinishReason: core.FinishStop}}, nil
	}
	return []core.StreamChunk{*s.held}, nil
}

// ---- embeddings -------------------------------------------------------------

const (
	// bedrockCohereBatch is the most texts one Cohere embed call accepts.
	bedrockCohereBatch = 96
	// bedrockTitanParallel bounds concurrent single-text Titan calls.
	bedrockTitanParallel = 4
)

// Embeddings serves Amazon Titan Text Embeddings (one text per call) and
// Cohere Embed (batched) through InvokeModel.
func (c *Bedrock) Embeddings(ctx context.Context, req *core.EmbeddingRequest, creds core.Credentials) (*core.EmbeddingResponse, error) {
	modelID, _ := splitInvokeModel(req.Model)
	base := bedrockBaseModel(modelID)
	switch {
	case strings.Contains(base, "amazon.titan-embed-text") || strings.Contains(base, "amazon.titan-embed-g1-text"):
		return c.titanEmbeddings(ctx, req, creds, modelID, strings.Contains(base, "-v2"))
	case strings.Contains(base, "cohere.embed"):
		return c.cohereEmbeddings(ctx, req, creds, modelID)
	}
	return nil, &core.ProviderError{Kind: core.ErrCapability, Scope: core.FailureScopeModel, Provider: c.id, Model: req.Model,
		Message: "unsupported Bedrock embedding model (supported: Amazon Titan Text Embeddings, Cohere Embed)"}
}

func (c *Bedrock) titanEmbeddings(ctx context.Context, req *core.EmbeddingRequest, creds core.Credentials, modelID string, v2 bool) (*core.EmbeddingResponse, error) {
	endpoint, err := c.modelURL(creds, modelID, "invoke")
	if err != nil {
		return nil, err
	}
	out := &core.EmbeddingResponse{Model: req.Model, Vectors: make([][]float32, len(req.Input))}
	tokens := make([]int, len(req.Input))
	errs := make([]error, len(req.Input))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, bedrockTitanParallel)
	var wg sync.WaitGroup
	var waitErr error
dispatch:
	for i, text := range req.Input {
		body := map[string]any{"inputText": text}
		if v2 {
			body["normalize"] = true
			if req.Dimensions > 0 {
				body["dimensions"] = req.Dimensions
			}
		}
		payload, _ := json.Marshal(body)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			waitErr = ctx.Err()
			break dispatch
		}
		wg.Add(1)
		go func(i int, payload []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			raw, _, err := c.call(ctx, creds, http.MethodPost, endpoint, payload, req.Model)
			if err != nil {
				errs[i] = err
				cancel()
				return
			}
			var resp struct {
				Embedding           []float32 `json:"embedding"`
				InputTextTokenCount int       `json:"inputTextTokenCount"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				errs[i] = &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: c.id, Model: req.Model,
					Message: "parse embeddings: " + err.Error(), Cause: err}
				cancel()
				return
			}
			out.Vectors[i], tokens[i] = resp.Embedding, resp.InputTextTokenCount
		}(i, payload)
	}
	wg.Wait()
	// Prefer the root cause over the cancellations it triggered in siblings.
	var firstErr error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if !core.IsClientDisconnect(err) {
			return nil, err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = waitErr
	}
	if firstErr != nil {
		return nil, firstErr
	}
	for _, n := range tokens {
		out.Usage.PromptTokens += n
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens
	out.Usage.Source = core.UsageSourceProvider
	return out, nil
}

func (c *Bedrock) cohereEmbeddings(ctx context.Context, req *core.EmbeddingRequest, creds core.Credentials, modelID string) (*core.EmbeddingResponse, error) {
	endpoint, err := c.modelURL(creds, modelID, "invoke")
	if err != nil {
		return nil, err
	}
	v4 := strings.Contains(bedrockBaseModel(modelID), "embed-v4")
	out := &core.EmbeddingResponse{Model: req.Model, Vectors: make([][]float32, 0, len(req.Input))}
	for start := 0; start < len(req.Input); start += bedrockCohereBatch {
		end := min(start+bedrockCohereBatch, len(req.Input))
		body := map[string]any{"texts": req.Input[start:end], "input_type": "search_document", "truncate": "END"}
		if v4 {
			body["embedding_types"] = []string{"float"}
			if req.Dimensions > 0 {
				body["output_dimension"] = req.Dimensions
			}
		}
		payload, _ := json.Marshal(body)
		raw, headers, err := c.call(ctx, creds, http.MethodPost, endpoint, payload, req.Model)
		if err != nil {
			return nil, err
		}
		vecs, err := parseCohereEmbeddings(raw)
		if err != nil || len(vecs) != end-start {
			msg := "parse embeddings: unexpected vector count"
			if err != nil {
				msg = "parse embeddings: " + err.Error()
			}
			return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: c.id, Model: req.Model, Message: msg, Cause: err}
		}
		out.Vectors = append(out.Vectors, vecs...)
		if n, err := strconv.Atoi(headers.Get("X-Amzn-Bedrock-Input-Token-Count")); err == nil {
			out.Usage.PromptTokens += n
		}
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens
	out.Usage.Source = core.UsageSourceProvider
	return out, nil
}

// parseCohereEmbeddings accepts both response shapes: a bare list of vectors
// (embed v3 without embedding_types) and {"float": [...]} (typed).
func parseCohereEmbeddings(raw []byte) ([][]float32, error) {
	var resp struct {
		Embeddings json.RawMessage `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	var vecs [][]float32
	if err := json.Unmarshal(resp.Embeddings, &vecs); err == nil {
		return vecs, nil
	}
	var typed struct {
		Float [][]float32 `json:"float"`
	}
	if err := json.Unmarshal(resp.Embeddings, &typed); err != nil {
		return nil, err
	}
	return typed.Float, nil
}
