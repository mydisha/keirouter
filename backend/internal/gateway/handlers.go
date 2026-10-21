package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/limits"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

func (s *Server) requestBodyLimit() int64 {
	if s.cfg.Server.MaxRequestBodyBytes > 0 {
		return s.cfg.Server.MaxRequestBodyBytes
	}
	return config.DefaultMaxRequestBodyBytes
}

// readRequestBody enforces the configured request-body limit and translates a
// size violation into a precise 413 response instead of an ambiguous 400.
func (s *Server) readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	limit := s.requestBodyLimit()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err == nil {
		return body, true
	}

	tr := s.traceOf(r)
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		message := fmt.Sprintf("request body exceeds maximum allowed size of %s", humanBytes(int(limit)))
		tr.reject(http.StatusRequestEntityTooLarge, "Rejected · request body too large", message,
			statusHint(http.StatusRequestEntityTooLarge))
		writeError(w, http.StatusRequestEntityTooLarge, message)
		return nil, false
	}

	if isClientDisconnect(err) || errors.Is(err, io.ErrUnexpectedEOF) {
		tr.reject(499, "Client disconnected while sending the request", err.Error(),
			"The connection closed before the full body arrived.")
	} else {
		tr.reject(http.StatusBadRequest, "Rejected · failed to read request body", err.Error(), "")
	}
	writeError(w, http.StatusBadRequest, "failed to read request body")
	return nil, false
}

// handleOpenAIChat serves /v1/chat/completions in the OpenAI dialect.
func (s *Server) handleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	s.handleChat(w, r, core.DialectOpenAI)
}

// handleAnthropicMessages serves /v1/messages in the Anthropic dialect.
func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	s.handleChat(w, r, core.DialectAnthropic)
}

// handleAnthropicCountTokens serves /v1/messages/count_tokens. Anthropic
// clients (notably Claude Code) call this before each /v1/messages turn to size
// the context window. We do not forward it upstream — most OpenAI-dialect
// providers (e.g. Xiaomi MiMo) have no equivalent endpoint and would return 405
// — so we parse the request locally and return a heuristic estimate in the
// Anthropic response shape: {"input_tokens": N}. The estimate uses the common
// ~4 chars/token rule, which is accurate enough for client-side budgeting.
func (s *Server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readRequestBody(w, r)
	if !ok {
		return
	}

	codec, err := s.codecs.Codec(core.DialectAnthropic)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unsupported dialect")
		return
	}
	req, err := codec.ParseRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	resp := struct {
		InputTokens int `json:"input_tokens"`
	}{InputTokens: estimateInputTokens(req)}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// estimateInputTokens approximates the prompt token count for a request using
// the ~4 chars/token heuristic over system text, message content, tool-call
// arguments, and tool results.
func estimateInputTokens(req *core.ChatRequest) int {
	return core.EstimatePromptTokens(req)
}

// handleOpenAIResponses serves /v1/responses in the OpenAI Responses dialect
// (Codex and Responses-native clients).
func (s *Server) handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	s.handleChat(w, r, core.DialectOpenAIResponses)
}

// handleChat is the shared chat handler parameterized by the client dialect.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request, dialect core.Dialect) {
	key, _ := authedKey(r.Context())
	tenantID := tenantOf(key)
	client := detectClient(r)
	tr := s.traceOf(r)

	codec, err := s.codecs.Codec(dialect)
	if err != nil {
		tr.reject(http.StatusInternalServerError, fmt.Sprintf("Unsupported API dialect: %s", dialect), "", "")
		writeError(w, http.StatusInternalServerError, "unsupported dialect")
		return
	}

	body, ok := s.readRequestBody(w, r)
	if !ok {
		return
	}
	bodySize := len(body)

	req, err := codec.ParseRequest(body)
	if err != nil {
		tr.reject(http.StatusBadRequest, "Rejected · request body is not valid "+string(dialect)+" JSON", err.Error(),
			"The client sent a body KeiRouter could not parse for this endpoint. Check that the client uses the matching API format.")
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	tr.setRequest(req.Model, req.Stream)

	// Attach routing metadata.
	req.Metadata = core.RequestMetadata{
		ClientKind:    client,
		SourceDialect: dialect,
		APIKeyID:      key.ID,
		TenantID:      tenantID,
		ProjectID:     key.ProjectID,
		RequestID:     chimiddleware.GetReqID(r.Context()),
		ClientBetas:   parseAnthropicBetaHeader(r.Header.Get("anthropic-beta")),
	}

	mode := "standard"
	if req.Stream {
		mode = "streaming"
	}
	tr.step(
		fmt.Sprintf("Received · %s · %d message%s · %s · %s", req.Model, len(req.Messages), plural(len(req.Messages)), mode, humanBytes(bodySize)),
		fmt.Sprintf("Client: %s\nAPI format: %s\nMessages: %d\nTools: %d\nBody: %s\nTenant: %s",
			client, dialect, len(req.Messages), len(req.Tools), humanBytes(bodySize), tenantID))

	resolved, err := s.resolveTargets(r.Context(), tenantID, req.Model)
	if err != nil {
		var bad badModelError
		if errors.As(err, &bad) {
			status := bad.HTTPStatus()
			title := fmt.Sprintf("Rejected · unknown model %q", req.Model)
			hint := "No provider model, alias or chain has this name. Check the client's model setting against the Models page."
			if status == http.StatusServiceUnavailable {
				title = fmt.Sprintf("Rejected · model %q is sold out", req.Model)
				hint = "No market offer currently clears this chain's price floor. It becomes available again once one does."
			}
			tr.reject(status, title, bad.Error(), hint)
			writeError(w, status, bad.Error())
			return
		}
		tr.reject(http.StatusInternalServerError, fmt.Sprintf("Failed to resolve model %q", req.Model), err.Error(), "")
		writeError(w, http.StatusInternalServerError, "failed to resolve model")
		return
	}

	// Surface the first resolved provider into the routing metadata so the
	// guardrails resolver can apply provider-scoped policies. The first
	// target is the primary; fallback targets may differ but policy lookups
	// happen once per request, before dispatch.
	if len(resolved.Targets) > 0 {
		req.Metadata.Provider = resolved.Targets[0].Provider
	}
	req.Metadata.ChainID = resolved.PlanOpts.ChainID

	// Enforce per-key model access restrictions. Filter resolved targets to
	// only include models the key is allowed to access.
	if len(resolved.Targets) > 0 {
		filtered, ferr := s.filterAllowedTargets(r.Context(), key.ID, key.PlanID, req.Model, resolved.PlanOpts.ChainID != "", resolved.Targets)
		if ferr != nil {
			tr.reject(http.StatusInternalServerError, "Model access check failed", ferr.Error(), "")
			writeError(w, http.StatusInternalServerError, "model access check failed")
			return
		}
		if len(filtered) == 0 {
			tr.reject(http.StatusForbidden,
				fmt.Sprintf("Rejected · key %q may not use %q", key.Name, req.Model), "",
				"The key's model restrictions exclude every target of this model. Allow the model on the Keys page or use another key.")
			writeError(w, http.StatusForbidden, "access denied: this API key is not permitted to use model "+req.Model)
			return
		}
		resolved.Targets = filtered
	}

	tr.routed(resolved.Targets)
	affinityKey := requestAffinityKey(r, req)
	req.Metadata.ContextAffinityKey = affinityKey
	body = nil // release body for GC — no longer needed

	effectiveLimits, err := s.effectiveLimits(r.Context(), key)
	if err != nil {
		tr.reject(http.StatusInternalServerError, "Failed to resolve rate limits", err.Error(), "")
		writeError(w, http.StatusInternalServerError, "limit resolution failed")
		return
	}

	opts := pipeline.Options{
		Targets:  resolved.Targets,
		PlanOpts: s.endpointPlanOptions(r.Context(), resolved.PlanOpts, resolved.Targets, affinityKey),
		Slimmer:  s.slimmerConfig(),
		Terse:    s.terseConfig(),
		Caveman:  s.cavemanConfig(),
		Headroom: s.headroomConfig(),
		Ponytail: s.ponytailConfig(),
		Skills:   s.skillsConfig(r, key, req.Model, resolved.Targets),
		Limits:   effectiveLimits,
	}

	if req.Stream {
		s.streamChat(w, r, codec, req, opts, s.bansosNoticeText(r.Context(), key.ID))
		return
	}
	s.unaryChat(w, r, codec, req, opts, s.bansosNoticeText(r.Context(), key.ID))
}

func (s *Server) effectiveLimits(ctx context.Context, key store.APIKey) (limits.EffectiveLimits, error) {
	if key.PlanID != "" {
		plan, err := s.db.Plans().Get(ctx, key.PlanID)
		if err != nil {
			return limits.EffectiveLimits{}, err
		}
		return limits.EffectiveLimits{
			RPM:         plan.RPMLimit,
			TPM:         plan.TPMLimit,
			Concurrency: plan.ConcurrencyLimit,
		}, nil
	}
	return limits.EffectiveLimits{
		RPM:         s.cfg.Limits.DefaultRPM,
		TPM:         s.cfg.Limits.DefaultTPM,
		Concurrency: s.cfg.Limits.DefaultConcurrency,
	}, nil
}

// unaryChat runs a non-streaming request and renders the response.
func (s *Server) unaryChat(w http.ResponseWriter, r *http.Request, codec transform.Codec, req *core.ChatRequest, opts pipeline.Options, notice string) {
	tr := s.traceOf(r)
	tr.setRequest(req.Model, false)
	result, err := s.pipeline.Chat(r.Context(), req, opts)
	if err != nil {
		tr.complete(outcome{fallbacks: req.Metadata.FallbackCount, err: err})
		s.writeProviderErrorAs(w, req.Metadata.SourceDialect, err)
		return
	}

	if notice != "" {
		appendNoticePart(result.Response, notice)
	}
	out, err := codec.RenderResponse(result.Response)
	if err != nil {
		tr.complete(outcome{provider: result.Provider, model: result.Model,
			err: &core.ProviderError{Kind: core.ErrInternal, Message: "failed to render provider response", Cause: err}})
		writeError(w, http.StatusInternalServerError, "failed to render response")
		return
	}
	tr.complete(outcome{
		provider:   result.Provider,
		model:      result.Model,
		account:    result.AccountID,
		tokens:     result.Response.Usage.PromptTokens + result.Response.Usage.CompletionTokens,
		costMicros: result.CostMicros,
		cacheHit:   result.CacheHit,
		fallbacks:  req.Metadata.FallbackCount,
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-KeiRouter-Provider", result.Provider)
	w.Header().Set("X-KeiRouter-Model", result.Model)
	if result.CacheHit {
		w.Header().Set("X-KeiRouter-Cache", "hit")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out) // out is already a []byte from RenderResponse
}

// streamChat runs a streaming request and relays SSE events in the client's
// dialect, honoring client disconnects and the configured stall timeout.
func (s *Server) streamChat(w http.ResponseWriter, r *http.Request, codec transform.Codec, req *core.ChatRequest, opts pipeline.Options, notice string) {
	tr := s.traceOf(r)
	tr.setRequest(req.Model, true)
	streamCodec, ok := codec.(transform.StreamCodec)
	if !ok {
		writeError(w, http.StatusInternalServerError, "dialect does not support streaming")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported by server")
		return
	}

	result, err := s.pipeline.Stream(r.Context(), req, opts)
	if err != nil {
		tr.complete(outcome{fallbacks: req.Metadata.FallbackCount, err: err})
		s.writeProviderError(w, err)
		return
	}
	streamPath := "translated"
	if result.DirectBody != nil {
		streamPath = "zero-copy passthrough"
	}
	tr.step(
		fmt.Sprintf("Streaming from %s/%s · first response after %s", result.Provider, result.Model, humanDuration(tr.elapsedMs())),
		fmt.Sprintf("Provider: %s\nModel: %s\nAccount: %s\nPath: %s", result.Provider, result.Model, result.AccountID, streamPath))

	if req.Metadata.SourceDialect == core.DialectOllama {
		w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-KeiRouter-Provider", result.Provider)
	w.Header().Set("X-KeiRouter-Model", result.Model)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Heartbeats keep intermediate proxies from dropping the client connection
	// as idle while the upstream model is silent (long thinking phases produce
	// no SSE output). NDJSON has no comment syntax, so Ollama clients are
	// exempt.
	heartbeatInterval := s.cfg.Server.StreamHeartbeatInterval
	if req.Metadata.SourceDialect == core.DialectOllama {
		heartbeatInterval = 0
	}

	// Direct stream path: frame SSE events without decoding normal payloads. This
	// preserves the low-overhead path while replacing late in-band provider
	// errors before they can reach the client.
	if result.DirectBody != nil {
		defer result.DirectBody.Close()
		dst := io.Writer(w)
		frameFlush := flusher.Flush
		var hw *heartbeatWriter
		if heartbeatInterval > 0 {
			hw = newHeartbeatWriter(w, flusher.Flush, heartbeatInterval)
			defer hw.stop()
			dst = hw
			frameFlush = nil // the heartbeat writer flushes after every frame
		}
		var injector *noticeInjectingWriter
		if notice != "" {
			injector = &noticeInjectingWriter{dst: dst, dialect: req.Metadata.SourceDialect, notice: notice}
			dst = injector
		}
		n, cpErr := copySanitizedStream(dst, result.DirectBody, req.Metadata.SourceDialect, frameFlush)
		if injector != nil && cpErr == nil {
			if ferr := injector.FlushFrame(); ferr != nil {
				cpErr = ferr
			}
		}
		if hw != nil {
			hw.stop()
		}
		if cpErr != nil && !isClientDisconnect(cpErr) {
			s.log.Warn("direct stream error", "bytes", n, "err", cpErr)
			// The upstream read failed (stall, reset, truncated body). Tell the
			// client in its own dialect instead of silently closing the
			// stream, which strict clients treat as "retry the whole request".
			var readErr *streamReadError
			if errors.As(cpErr, &readErr) {
				_, _ = dst.Write(streamErrorEvent(req.Metadata.SourceDialect, sanitizeUpstreamError(readErr.err)))
			}
		}
		flusher.Flush()
		// Complete direct-stream accounting exactly once. Client disconnects do
		// not indicate provider failure; late provider/stall errors do and must
		// update cooldowns and health telemetry.
		completionErr := cpErr
		if isClientDisconnect(cpErr) {
			_ = result.DirectBody.Close()
			completionErr = context.Canceled
		}
		if result.DirectCompleteFunc != nil {
			result.DirectCompleteFunc(completionErr)
		}
		o := outcome{provider: result.Provider, model: result.Model, account: result.AccountID,
			bytes: n, fallbacks: req.Metadata.FallbackCount, err: cpErr}
		if result.DirectStats != nil {
			o.tokens = result.DirectStats.Tokens
			o.costMicros = result.DirectStats.CostMicros
		}
		tr.complete(o)
		return
	}

	// Wrap the response writer in a bufio.Writer to batch small SSE writes
	// into fewer syscalls. The pool avoids allocating a new writer per request.
	bw := core.SSEWriterPool.Get().(*bufio.Writer)
	defer func() {
		bw.Reset(nil)
		core.SSEWriterPool.Put(bw)
	}()
	bw.Reset(w)

	state := &transform.StreamState{Model: result.Model}
	transform.ResetStreamState(state)
	var totalTokens int
	var chunkCount int
	// clientGone is set on the first failed write so the rest of the stream
	// is drained quietly instead of logging once per chunk.
	var clientGone error

	// ToolArgSanitizer buffers streaming tool call arguments and emits
	// sanitized JSON when each tool call completes. This fixes malformed
	// arguments from non-Anthropic models (e.g., Read.limit as string).
	// Tool-call args from fragmenting upstreams (Kiro, Cursor, CommandCode)
	// arrive split across frames and must be reassembled into one complete JSON
	// object before rendering, regardless of tool name or client dialect.
	// Streaming raw fragments and relying on the client to reassemble breaks
	// clients like Cline ("missing required parameter"). The sanitizer passes
	// text/thinking through immediately, so this only buffers the (small,
	// non-actionable) tool-arg fragments — live text streaming is unaffected.
	sanitizer := transform.NewToolArgSanitizer()

	// ThinkTagState strips <think>...</think> tags from streaming content.

	// Some models (MiMo, QwQ) embed reasoning as XML tags in the content
	// field instead of using a structured reasoning_content field.
	thinkFilter := &transform.ThinkTagState{}
	renderChunk := func(cleaned core.StreamChunk) {
		// Route thinking chunks through the filter; tool calls and others
		// pass through directly.
		if cleaned.Type == core.ChunkText {
			for _, fc := range thinkFilter.ProcessFeed(cleaned.Delta) {
				if fc.Type == core.ChunkThinking {
					// Thinking content is consumed internally — not sent to client.
					continue
				}
				events, rerr := streamCodec.RenderStreamChunk(fc, state)
				if rerr != nil {
					s.log.Warn("failed to render stream chunk", "err", rerr)
					return
				}
				for _, ev := range events {
					if _, werr := bw.Write(ev); werr != nil {
						if clientGone == nil {
							clientGone = &streamWriteError{err: werr}
						}
						return
					}
				}
			}
			return
		}
		events, rerr := streamCodec.RenderStreamChunk(cleaned, state)
		if rerr != nil {
			s.log.Warn("failed to render stream chunk", "err", rerr)
			return
		}
		for _, ev := range events {
			if _, werr := bw.Write(ev); werr != nil {
				if clientGone == nil {
					clientGone = &streamWriteError{err: werr}
				}
				return
			}
		}
	}
	// flushToClient pushes buffered events to the socket. It is called after
	// each chunk only when no further chunk is already queued, so a burst of
	// deltas costs one write+flush syscall pair instead of one per delta,
	// while a lone delta still reaches the client immediately.
	flushToClient := func() {
		bw.Flush()
		flusher.Flush()
	}

	var heartbeatC <-chan time.Time
	if heartbeatInterval > 0 {
		heartbeatTicker := time.NewTicker(heartbeatInterval)
		defer heartbeatTicker.Stop()
		heartbeatC = heartbeatTicker.C
	}
	lastActivity := time.Now()

	var streamErr error
	var deferredFinish *core.StreamChunk
streamLoop:
	for {
		select {
		case chunk, ok := <-result.Chunks:
			if !ok {
				break streamLoop
			}
			lastActivity = time.Now()
			if chunk.Type == core.ChunkError {
				streamErr = chunk.Err
				if streamErr == nil {
					streamErr = &core.ProviderError{Kind: core.ErrUpstream, Message: "provider stream failed"}
				}
				s.log.Warn("stream error", "err", streamErr)
				_, _ = bw.Write(streamErrorEvent(req.Metadata.SourceDialect, sanitizeUpstreamError(streamErr)))
				// Emit terminal events so strict clients (Claude Code, Cline) see a
				// well-formed stream end instead of a truncated connection. Without
				// message_stop/[DONE], the client may treat the stream as incomplete
				// and retry the request — causing duplicate responses on the user side.
				for _, ev := range streamCodec.RenderStreamDone(state) {
					_, _ = bw.Write(ev)
				}
				_ = bw.Flush()
				flusher.Flush()
				break streamLoop
			}
			if chunk.Type == core.ChunkUsage && chunk.Usage != nil {
				totalTokens = chunk.Usage.PromptTokens + chunk.Usage.CompletionTokens
			}
			// When a bansos notice must be appended, hold the terminal finish
			// chunk so the notice text is streamed first. This keeps the
			// Anthropic event order valid (content_block_* before message_delta).
			if notice != "" && chunk.Type == core.ChunkFinish {
				if deferredFinish == nil {
					c := chunk
					deferredFinish = &c
				}
				continue
			}
			chunkCount++
			sanitizer.Process(chunk, renderChunk)
			if len(result.Chunks) == 0 {
				flushToClient()
			}
		case <-heartbeatC:
			// Only beat when the stream has actually been silent; steady chunk
			// traffic is its own keep-alive.
			if time.Since(lastActivity) < heartbeatInterval {
				continue
			}
			if _, werr := bw.Write(sseHeartbeatFrame); werr != nil {
				streamErr = &streamWriteError{err: werr}
				break streamLoop
			}
			if werr := bw.Flush(); werr != nil {
				streamErr = &streamWriteError{err: werr}
				break streamLoop
			}
			flusher.Flush()
			lastActivity = time.Now()
		}
	}

	finish := func(err error) {
		if err == nil && clientGone != nil {
			err = clientGone
		}
		tr.complete(outcome{provider: result.Provider, model: result.Model, account: result.AccountID,
			tokens: totalTokens, chunks: chunkCount, fallbacks: req.Metadata.FallbackCount, err: err})
	}
	if streamErr != nil {
		finish(streamErr)
		return
	}

	// Flush any remaining buffered tool calls and think-tag buffer.
	// Both Flush calls are idempotent — duplicate ChunkFinish events from
	// upstream providers (e.g. some OpenAI-compatible gateways) will not
	// cause re-emission of already-flushed content.
	sanitizer.Flush(renderChunk)

	// Flush think-tag state — emit any remaining buffered text.
	for _, fc := range thinkFilter.Flush() {
		if fc.Type == core.ChunkThinking {
			continue
		}
		events, _ := streamCodec.RenderStreamChunk(fc, state)
		for _, ev := range events {
			_, _ = bw.Write(ev)
		}
	}

	// Append the bansos notice as a trailing text chunk (empty notice = no-op for
	// every regular user key). Rendered through the codec so OpenAI appends to the
	// content string and Anthropic emits a separate text block. It runs before
	// the deferred finish chunk below so the Anthropic event order stays valid.
	if notice != "" {
		noticeChunk := core.StreamChunk{Type: core.ChunkText, Delta: "\n\n---\n" + notice}
		events, _ := streamCodec.RenderStreamChunk(noticeChunk, state)
		for _, ev := range events {
			_, _ = bw.Write(ev)
		}
	}

	// Emit the held-back terminal finish chunk now that the notice is out.
	if deferredFinish != nil {
		events, _ := streamCodec.RenderStreamChunk(*deferredFinish, state)
		for _, ev := range events {
			_, _ = bw.Write(ev)
		}
	}

	for _, ev := range streamCodec.RenderStreamDone(state) {
		_, _ = bw.Write(ev)
	}
	bw.Flush()
	flusher.Flush()

	finish(nil)
}

// appendNoticePart appends the bansos warning as a trailing text part on the
// response. OpenAI renders it appended to the content string; Anthropic renders
// it as a separate text block. Called only when notice != "" (bansos key only).
func appendNoticePart(resp *core.ChatResponse, notice string) {
	resp.Message.Content = append(resp.Message.Content, core.ContentPart{
		Type: core.PartText,
		Text: "\n\n---\n" + notice,
	})
}

// providerStreamEventError keeps a late provider error available to internal
// logs after its wire payload has been replaced with a generic client message.
type providerStreamEventError struct{ detail string }

func (e *providerStreamEventError) Error() string { return "provider stream error: " + e.detail }

// maxDirectStreamEventBytes bounds one buffered SSE event on the zero-copy
// path so a misbehaving upstream cannot grow the event buffer without limit.
const maxDirectStreamEventBytes = 16 << 20

var errDirectStreamEventTooLarge = errors.New("upstream stream event exceeds the maximum supported size")

type streamReadError struct{ err error }

func (e *streamReadError) Error() string { return e.err.Error() }
func (e *streamReadError) Unwrap() error { return e.err }

type streamWriteError struct{ err error }

func (e *streamWriteError) Error() string { return e.err.Error() }
func (e *streamWriteError) Unwrap() error { return e.err }

// copySanitizedStream keeps the direct-stream path lightweight by framing SSE
// events without decoding successful chunks. Only potential error events are
// decoded; those are replaced with a dialect-compatible generic event.
// directReaderPool reuses the 64 KiB read buffers of the zero-copy path; one
// per concurrent stream otherwise churns through the allocator.
var directReaderPool = sync.Pool{
	New: func() any { return bufio.NewReaderSize(nil, 64*1024) },
}

func copySanitizedStream(dst io.Writer, src io.Reader, dialect core.Dialect, flush func()) (int64, error) {
	reader := directReaderPool.Get().(*bufio.Reader)
	reader.Reset(src)
	defer func() {
		reader.Reset(nil)
		directReaderPool.Put(reader)
	}()
	if flush != nil {
		// Defer the flush only while another complete frame is already
		// buffered: a burst of frames that arrived together goes out in one
		// syscall, but a frame followed by a partial one is pushed at once so
		// the client never waits on bytes we already hold.
		inner := flush
		flush = func() {
			if n := reader.Buffered(); n > 0 {
				if pending, err := reader.Peek(n); err == nil && bytes.Contains(pending, []byte("\n\n")) {
					return
				}
			}
			inner()
		}
	}
	if dialect == core.DialectOllama {
		return copySanitizedNDJSON(dst, reader, dialect, flush)
	}

	var event bytes.Buffer
	var written int64
	for {
		line, readErr := reader.ReadSlice('\n')
		if len(line) > 0 {
			if event.Len()+len(line) > maxDirectStreamEventBytes {
				return written, &streamReadError{err: errDirectStreamEventTooLarge}
			}
			_, _ = event.Write(line)
			if len(bytes.TrimRight(line, "\r\n")) == 0 {
				n, err := writeSanitizedFrame(dst, event.Bytes(), dialect, flush)
				written += n
				if err != nil {
					return written, err
				}
				event.Reset()
			}
		}
		if readErr == nil || errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if readErr != io.EOF {
			return written, &streamReadError{err: readErr}
		}
		if event.Len() > 0 {
			n, err := writeSanitizedFrame(dst, event.Bytes(), dialect, flush)
			written += n
			if err != nil {
				return written, err
			}
		}
		return written, nil
	}
}

// noticeInjectingWriter wraps a direct-stream destination and, before the
// terminal frame is written, emits a trailing notice event in the client's
// dialect. Non-terminal frames are forwarded byte-for-byte, so the upstream
// payload is never altered; only the bansos key is ever wrapped.
type noticeInjectingWriter struct {
	dst     io.Writer
	dialect core.Dialect
	notice  string
	emitted bool
	carry   []byte
	// id/model mirror the upstream frames so the notice chunk looks like a
	// native continuation (strict clients validate these fields).
	id    string
	model string
	// maxIndex tracks the highest Anthropic content-block index seen, so the
	// notice block opens at the next free index.
	maxIndex int
}

// Write buffers frames (terminated by a blank line) and defers the terminal
// frame so the notice can be emitted immediately before it.
func (w *noticeInjectingWriter) Write(p []byte) (int, error) {
	total := len(p)
	w.carry = append(w.carry, p...)
	for {
		idx := bytes.Index(w.carry, []byte("\n\n"))
		if idx < 0 {
			return total, nil
		}
		frame := w.carry[:idx+2]
		w.carry = w.carry[idx+2:]
		w.captureIdentity(frame)
		if err := w.writeFrame(frame); err != nil {
			return total, err
		}
	}
}

// captureIdentity records the id/model and the highest content-block index from
// an SSE data frame, if present.
func (w *noticeInjectingWriter) captureIdentity(frame []byte) {
	data := strings.TrimSpace(string(frame))
	if i := strings.Index(data, "data:"); i >= 0 {
		data = strings.TrimSpace(data[i+len("data:"):])
	}
	var probe struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Index *int   `json:"index"`
	}
	if json.Unmarshal([]byte(data), &probe) == nil {
		if probe.ID != "" {
			w.id = probe.ID
		}
		if probe.Model != "" {
			w.model = probe.Model
		}
		if probe.Index != nil && *probe.Index > w.maxIndex {
			w.maxIndex = *probe.Index
		}
	}
}

func (w *noticeInjectingWriter) writeFrame(frame []byte) error {
	if w.notice != "" && !w.emitted && isTerminalFrame(frame, w.dialect) {
		w.emitted = true
		if ev := noticeSSEFrame(w.dialect, w.notice, w.id, w.model, w.maxIndex+1); ev != nil {
			if _, err := w.dst.Write(ev); err != nil {
				return err
			}
		}
	}
	_, err := w.dst.Write(frame)
	return err
}

// FlushFrame releases any buffered partial frame at end-of-stream.
func (w *noticeInjectingWriter) FlushFrame() error {
	if len(w.carry) == 0 {
		return nil
	}
	frame := w.carry
	w.carry = nil
	return w.writeFrame(frame)
}

// isTerminalFrame reports whether an SSE frame ends the stream for the dialect.
func isTerminalFrame(frame []byte, dialect core.Dialect) bool {
	body := strings.TrimSpace(string(frame))
	switch dialect {
	case core.DialectAnthropic:
		return strings.Contains(body, "message_stop")
	case core.DialectGemini:
		return strings.Contains(body, `"finishReason"`)
	default: // OpenAI / Responses / Ollama
		return strings.Contains(body, "[DONE]") ||
			strings.Contains(body, `"finish_reason":"`) ||
			strings.Contains(body, `"finish_reason": "`) ||
			strings.Contains(body, `"done":true`) || strings.Contains(body, `"done": true`)
	}
}

// noticeSSEFrame renders a trailing text event in the client's dialect so the
// notice appears at the end of the assistant message.
func noticeSSEFrame(dialect core.Dialect, notice string, id string, model string, index int) []byte {
	text := "\n\n---\n" + notice
	switch dialect {
	case core.DialectAnthropic:
		// Emit a complete text block so strict Anthropic clients (Claude Code)
		// see a well-formed content_block_start → delta → stop sequence.
		start, _ := json.Marshal(map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		deltaPayload, _ := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{"type": "text_delta", "text": text},
		})
		stop, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": index})
		out := append([]byte("event: content_block_start\ndata: "), start...)
		out = append(out, '\n', '\n')
		out = append(out, "event: content_block_delta\ndata: "...)
		out = append(out, deltaPayload...)
		out = append(out, '\n', '\n')
		out = append(out, "event: content_block_stop\ndata: "...)
		out = append(out, stop...)
		return append(out, '\n', '\n')
	case core.DialectGemini:
		payload, _ := json.Marshal(map[string]any{
			"candidates": []any{map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": text}},
				},
			}},
		})
		return append([]byte("data: "), append(payload, '\n', '\n')...)
	case core.DialectOllama:
		payload, _ := json.Marshal(map[string]any{
			"model":   model,
			"message": map[string]any{"role": "assistant", "content": text},
			"done":    false,
		})
		return append(payload, '\n')
	default: // OpenAI / Responses
		payload := map[string]any{
			"object": "chat.completion.chunk",
			"choices": []any{map[string]any{
				"index":         0,
				"delta":         map[string]any{"content": text},
				"finish_reason": nil,
			}},
		}
		if id != "" {
			payload["id"] = id
		}
		if model != "" {
			payload["model"] = model
		}
		body, _ := json.Marshal(payload)
		return append([]byte("data: "), append(body, '\n', '\n')...)
	}
}

// copySanitizedNDJSON preserves Ollama's one-JSON-object-per-line framing.
// ReadSlice avoids allocating for normal-sized lines; a buffer is used only
// when an unusually large object spans the reader's bounded internal buffer.
func copySanitizedNDJSON(dst io.Writer, reader *bufio.Reader, dialect core.Dialect, flush func()) (int64, error) {
	var oversized bytes.Buffer
	var written int64
	for {
		fragment, readErr := reader.ReadSlice('\n')
		if oversized.Len() == 0 && !errors.Is(readErr, bufio.ErrBufferFull) {
			if len(bytes.TrimSpace(fragment)) > 0 {
				n, err := writeSanitizedFrame(dst, fragment, dialect, flush)
				written += n
				if err != nil {
					return written, err
				}
			}
		} else {
			_, _ = oversized.Write(fragment)
			if !errors.Is(readErr, bufio.ErrBufferFull) {
				if len(bytes.TrimSpace(oversized.Bytes())) > 0 {
					n, err := writeSanitizedFrame(dst, oversized.Bytes(), dialect, flush)
					written += n
					if err != nil {
						return written, err
					}
				}
				oversized.Reset()
			}
		}

		if readErr == nil || errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if readErr == io.EOF {
			return written, nil
		}
		return written, &streamReadError{err: readErr}
	}
}

func writeSanitizedFrame(dst io.Writer, raw []byte, dialect core.Dialect, flush func()) (int64, error) {
	providerErr := hasStreamErrorMarker(raw) && isProviderStreamError(raw)
	out := raw
	if providerErr {
		out = streamErrorEvent(dialect, "upstream provider request failed")
	}
	n, err := dst.Write(out)
	if err == nil && n != len(out) {
		err = io.ErrShortWrite
	}
	if flush != nil {
		flush()
	}
	if err != nil {
		return int64(n), &streamWriteError{err: err}
	}
	if providerErr {
		detail := truncateStreamEvent(string(raw))
		cause := &providerStreamEventError{detail: detail}
		return int64(n), &core.ProviderError{
			Kind: core.ErrUpstream, Message: cause.Error(), Cause: cause,
		}
	}
	return int64(n), nil
}

// hasStreamErrorMarker is the cheap pre-filter for frames that might carry a
// provider error. A tool-call frame full of Go source matches "error" on
// nearly every line, so instead of six substring scans it looks for the
// shapes an error frame actually has: an SSE "event:" line mentioning error
// or failure, or the quoted JSON keys/values an error envelope uses.
func hasStreamErrorMarker(frame []byte) bool {
	if bytes.Contains(frame, []byte(`"error"`)) || bytes.Contains(frame, []byte(`"failed"`)) ||
		bytes.Contains(frame, []byte(`error"`)) {
		return true
	}
	// event: error / event: response.failed
	for rest := frame; len(rest) > 0; {
		nl := bytes.IndexByte(rest, '\n')
		line := rest
		if nl >= 0 {
			line = rest[:nl]
			rest = rest[nl+1:]
		} else {
			rest = nil
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			name := bytes.ToLower(bytes.TrimSpace(line[6:]))
			if bytes.Contains(name, []byte("error")) || bytes.Contains(name, []byte("fail")) {
				return true
			}
		}
	}
	return false
}

func isProviderStreamError(event []byte) bool {
	var data []byte
	for rest := event; len(rest) > 0; {
		nl := bytes.IndexByte(rest, '\n')
		line := rest
		if nl >= 0 {
			line = rest[:nl]
			rest = rest[nl+1:]
		} else {
			rest = nil
		}
		line = bytes.TrimSpace(line)
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			name := bytes.ToLower(bytes.TrimSpace(line[6:]))
			if bytes.Contains(name, []byte("error")) || bytes.Contains(name, []byte("failed")) {
				return true
			}
		case bytes.HasPrefix(line, []byte("data:")):
			payload := bytes.TrimSpace(line[5:])
			if len(payload) > 0 && !bytes.Equal(payload, []byte("[DONE]")) {
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, payload...)
			}
		}
	}
	if len(data) == 0 {
		standalone := bytes.TrimSpace(event)
		if len(standalone) == 0 || standalone[0] != '{' {
			return false
		}
		data = standalone
	}

	var envelope struct {
		Type   string          `json:"type"`
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return false
	}
	typeName := strings.ToLower(envelope.Type)
	status := strings.ToLower(envelope.Status)
	rawError := strings.TrimSpace(string(envelope.Error))
	return strings.Contains(typeName, "error") || strings.Contains(typeName, "failed") ||
		status == "failed" || (rawError != "" && rawError != "null")
}

func streamErrorEvent(dialect core.Dialect, message string) []byte {
	if dialect == core.DialectOllama {
		body, _ := json.Marshal(map[string]any{"error": message})
		return append(body, '\n')
	}

	var payload map[string]any
	if dialect == core.DialectGemini {
		payload = map[string]any{"error": map[string]any{
			"code": http.StatusBadGateway, "message": message, "status": "UNAVAILABLE",
		}}
	} else if dialect == core.DialectAnthropic {
		payload = map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "api_error", "message": message},
		}
	} else {
		payload = map[string]any{"error": map[string]any{
			"message": message, "type": "upstream_error", "code": "upstream_error",
		}}
	}

	body, _ := json.Marshal(payload)
	if dialect == core.DialectAnthropic {
		out := make([]byte, 0, len(body)+22)
		out = append(out, "event: error\ndata: "...)
		out = append(out, body...)
		return append(out, '\n', '\n')
	}
	out := make([]byte, 0, len(body)+8)
	out = append(out, "data: "...)
	out = append(out, body...)
	return append(out, '\n', '\n')
}

func truncateStreamEvent(event string) string {
	const max = 1024
	event = strings.TrimSpace(event)
	if len(event) > max {
		return event[:max] + "…"
	}
	return event
}

// writeProviderError maps a structured provider error to an HTTP status while
// keeping the provider's original message in internal logs only. The body is
// OpenAI-shaped; use writeProviderErrorAs when the client's dialect is known.
func (s *Server) writeProviderError(w http.ResponseWriter, err error) {
	s.writeProviderErrorAs(w, core.DialectOpenAI, err)
}

// writeProviderErrorAs renders the error in the client's own dialect so SDK
// retry logic keeps working: Claude Code reads Anthropic's
// {"type":"error","error":{"type":"overloaded_error"}} to decide whether to
// retry, and Gemini clients expect a google.rpc.Status-shaped envelope.
func (s *Server) writeProviderErrorAs(w http.ResponseWriter, dialect core.Dialect, err error) {
	pe := core.AsProviderError(err)
	status := providerErrorStatus(pe)
	// Retry-After tells well-behaved clients (Claude Code, the OpenAI SDKs)
	// how long to back off. It is meaningful for throttling and for upstream
	// faults where the provider or the dispatcher knows the recovery window.
	if pe.RetryAfter > 0 && (status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout) {
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", math.Ceil(pe.RetryAfter.Seconds())))
	}

	if s.log != nil {
		s.log.Error("provider request failed",
			"kind", pe.Kind,
			"provider", pe.Provider,
			"model", pe.Model,
			"upstream_status", pe.StatusCode,
			"error", err)
	}
	writeDialectError(w, dialect, status, sanitizeUpstreamError(pe))
}

// writeDialectError writes an error envelope in the given client dialect.
func writeDialectError(w http.ResponseWriter, dialect core.Dialect, status int, message string) {
	switch dialect {
	case core.DialectAnthropic:
		writeJSON(w, status, map[string]any{
			"type":  "error",
			"error": map[string]any{"type": anthropicErrorType(status), "message": message},
		})
	case core.DialectGemini:
		writeJSON(w, status, map[string]any{
			"error": map[string]any{"code": status, "message": message, "status": googleStatusName(status)},
		})
	case core.DialectOllama:
		writeJSON(w, status, map[string]any{"error": message})
	default:
		writeError(w, status, message)
	}
}

// anthropicErrorType maps an HTTP status to Anthropic's error type vocabulary.
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden, http.StatusPaymentRequired:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable, 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// googleStatusName maps an HTTP status to the google.rpc.Code name Gemini
// clients expect in error.status.
func googleStatusName(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden, http.StatusPaymentRequired:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return "UNAVAILABLE"
	default:
		return "INTERNAL"
	}
}

// parseAnthropicBetaHeader splits a client's anthropic-beta header into the
// individual feature flags, keeping only well-formed identifiers
// (name-YYYY-MM-DD) so arbitrary header text never reaches the upstream.
func parseAnthropicBetaHeader(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		flag := strings.ToLower(strings.TrimSpace(part))
		if flag == "" || !anthropicBetaFlagRe.MatchString(flag) {
			continue
		}
		out = append(out, flag)
	}
	return out
}

var anthropicBetaFlagRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*-\d{4}-\d{2}-\d{2}$`)

// providerErrorStatus maps an error kind to the HTTP status returned to the
// client.
func providerErrorStatus(pe *core.ProviderError) int {
	switch pe.Kind {
	case core.ErrBadRequest, core.ErrCapability, core.ErrContextWindow, core.ErrContentFilter:
		return http.StatusBadRequest
	case core.ErrModelUnavailable:
		return http.StatusNotFound
	case core.ErrModelOutOfStock:
		return http.StatusServiceUnavailable
	case core.ErrAuth:
		return http.StatusUnauthorized
	case core.ErrRateLimit:
		return http.StatusTooManyRequests
	case core.ErrQuotaExhausted, core.ErrBudgetBlocked:
		return http.StatusPaymentRequired
	case core.ErrPolicyBlocked:
		return http.StatusForbidden
	case core.ErrTimeout:
		return http.StatusGatewayTimeout
	case core.ErrInternal:
		return http.StatusInternalServerError
	case core.ErrUpstream:
		// 529/503 from the provider means "overloaded, try again": surface it
		// as 503 so SDK retry policies treat it as transient.
		if pe.StatusCode == 529 || pe.StatusCode == http.StatusServiceUnavailable {
			return http.StatusServiceUnavailable
		}
	}
	return http.StatusBadGateway
}

// isClientDisconnect reports whether cancellation or a downstream response
// write failed because the client disconnected. Read-side socket errors remain
// provider failures even when their text contains the same reset wording.
func isClientDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if core.IsClientDisconnect(err) {
		return true
	}
	var writeErr *streamWriteError
	if !errors.As(err, &writeErr) {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "reset by peer") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "client disconnected") ||
		strings.Contains(s, "http2: stream closed")
}

// filterAllowedTargets filters resolved routing targets to only include models
// the given API key is allowed to access. Returns empty slice if no target
// matches the key's model access policy.
//
// isChain marks a request that resolved to a routing chain. A chain the key may
// use by name (bare or "chain:") grants every step it resolves to, since the
// key's grant is for the chain as a whole rather than its individual steps.
func (s *Server) filterAllowedTargets(ctx context.Context, keyID, planID, requestedModel string, isChain bool, targets []dispatch.Target) ([]dispatch.Target, error) {
	keys := s.identity.Keys()
	// Effective models: per-key override wins, otherwise the key follows its
	// plan's models live (so plan edits propagate without per-key writes).
	allowed, _, err := keys.EffectiveAllowedModels(ctx, keyID, planID)
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return targets, nil // no restriction
	}

	if isChain {
		name, _ := strings.CutPrefix(requestedModel, "chain:")
		if modelMatchesAny(requestedModel, allowed) || modelMatchesAny(name, allowed) {
			return targets, nil
		}
	}

	// Match all targets in-memory against the already-fetched allowed list.
	// This avoids N additional DB round-trips (one per target) that the
	// previous IsModelAllowed-per-target pattern caused.
	var filtered []dispatch.Target
	for _, t := range targets {
		if modelMatchesAny(t.Model, allowed) {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// modelMatchesAny reports whether model matches any pattern in allowed.
// Patterns support a trailing '*' wildcard (e.g. "claude-*").
func modelMatchesAny(model string, allowed []string) bool {
	lower := strings.ToLower(model)
	for _, pattern := range allowed {
		lp := strings.ToLower(pattern)
		if strings.HasSuffix(lp, "*") {
			if strings.HasPrefix(lower, lp[:len(lp)-1]) {
				return true
			}
		} else if lp == lower {
			return true
		}
	}
	return false
}

// handleKeyUsage serves GET /v1/keys/me/usage — the authenticated API key
// owner can check their own token/cost usage and remaining budget.
func (s *Server) handleKeyUsage(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	ctx := r.Context()

	// Get budgets scoped to this key.
	budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, key.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list budgets")
		return
	}

	type budgetOut struct {
		Period        string  `json:"period"`
		LimitTokens   int64   `json:"limit_tokens"`
		TokensUsed    int64   `json:"tokens_used"`
		TokensRemain  int64   `json:"tokens_remaining"`
		TokensPctUsed float64 `json:"tokens_pct_used"`
		LimitUSD      float64 `json:"limit_usd"`
		SpentUSD      float64 `json:"spent_usd"`
		USDRemaining  float64 `json:"usd_remaining"`
		USDUsed       float64 `json:"usd_pct_used"`
		Alert         bool    `json:"alert"`
	}

	var budgetOuts []budgetOut
	for _, b := range budgets {
		since := budget.PeriodStart(b.Period, time.Now())
		costMicros, tokens, err := s.usage.SpendAndTokens(ctx, b.ScopeKind, b.ScopeID, since)
		if err != nil {
			s.log.Error("key usage: spend lookup failed", "err", err)
			continue
		}

		bo := budgetOut{
			Period:      b.Period,
			LimitTokens: b.LimitTokens,
			TokensUsed:  tokens,
			LimitUSD:    float64(b.LimitMicros) / 1_000_000,
			SpentUSD:    float64(costMicros) / 1_000_000,
		}
		if b.LimitTokens > 0 {
			bo.TokensRemain = b.LimitTokens - tokens
			if bo.TokensRemain < 0 {
				bo.TokensRemain = 0
			}
			bo.TokensPctUsed = float64(tokens) / float64(b.LimitTokens) * 100
		}
		if b.LimitMicros > 0 {
			bo.USDRemaining = bo.LimitUSD - bo.SpentUSD
			if bo.USDRemaining < 0 {
				bo.USDRemaining = 0
			}
			bo.USDUsed = float64(costMicros) / float64(b.LimitMicros) * 100
		}
		// Alert if either threshold crossed.
		if b.AlertPct > 0 {
			if (b.LimitMicros > 0 && costMicros*100 >= b.LimitMicros*int64(b.AlertPct)) ||
				(b.LimitTokens > 0 && tokens*100 >= b.LimitTokens*int64(b.AlertPct)) {
				bo.Alert = true
			}
		}
		budgetOuts = append(budgetOuts, bo)
	}

	// Get allowed models for this key.
	allowedModels, modelsSource, err := s.identity.Keys().EffectiveAllowedModels(ctx, key.ID, key.PlanID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get model access")
		return
	}

	// Get current period summary scoped to this specific key.
	now := time.Now()
	summary, err := s.usage.SummarizeByKey(ctx, key.ID, time.Time{})
	if err != nil {
		s.log.Error("key usage: summarize failed", "err", err)
	}

	daily, _ := s.usage.DailyByKey(ctx, key.ID, now.AddDate(0, 0, -30))
	var dailyOut []map[string]any
	for _, d := range daily {
		dailyOut = append(dailyOut, map[string]any{
			"date": d.Date, "requests": d.Requests,
			"prompt_tokens": d.PromptTokens, "completion_tokens": d.CompletionTokens,
			"cost_usd": float64(d.CostMicros) / 1_000_000,
		})
	}

	chainNames := s.chainNamesByTenant(ctx, key.TenantID)
	models, _ := s.usage.ByModelByKey(ctx, key.ID, now.AddDate(0, 0, -30))
	var modelOut []map[string]any
	for _, m := range models {
		modelOut = append(modelOut, map[string]any{
			"provider": m.Provider, "model": displayModel(m.Model, m.ChainID, chainNames),
			"total_requests": m.TotalRequests,
			"prompt_tokens":  m.PromptTokens, "completion_tokens": m.CompletionTokens,
			"cost_usd": float64(m.CostMicros) / 1_000_000,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key_id":         key.ID,
		"key_name":       key.Name,
		"budgets":        budgetOuts,
		"allowed_models": allowedModels,
		"models_source":  modelsSource,
		"current_period": map[string]any{
			"prompt_tokens":     summary.PromptTokens,
			"completion_tokens": summary.CompletionTokens,
			"total_requests":    summary.TotalRequests,
			"cost_usd":          float64(summary.CostMicros) / 1_000_000,
		},
		"daily":  dailyOut,
		"models": modelOut,
	})
}

// buildKeyUsageMap assembles the portal usage payload for one key.
// chainNamesByTenant returns a chain-id to user-facing-name map for a tenant.
// Callers degrade gracefully to the recorded model when this is empty.
func (s *Server) chainNamesByTenant(ctx context.Context, tenantID string) map[string]string {
	names := map[string]string{}
	chains, err := s.chains.ListByTenant(ctx, tenantID)
	if err != nil {
		s.log.Error("usage: chain lookup failed", "err", err)
		return names
	}
	for _, c := range chains {
		names[c.ID] = c.Name
	}
	return names
}

// displayModel resolves the label shown to a portal user: the chain name when
// the request ran through a known chain, otherwise the recorded model.
func displayModel(model, chainID string, chainNames map[string]string) string {
	if name, ok := chainNames[chainID]; ok && name != "" {
		return name
	}
	return model
}

func (s *Server) buildKeyUsageMap(ctx context.Context, key store.APIKey, days int) (map[string]any, error) {
	// Get budgets scoped to this key.
	budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, key.ID)
	if err != nil {
		return nil, err
	}

	type budgetOut struct {
		Period        string  `json:"period"`
		LimitTokens   int64   `json:"limit_tokens"`
		TokensUsed    int64   `json:"tokens_used"`
		TokensRemain  int64   `json:"tokens_remaining"`
		TokensPctUsed float64 `json:"tokens_pct_used"`
		LimitUSD      float64 `json:"limit_usd"`
		SpentUSD      float64 `json:"spent_usd"`
		USDRemaining  float64 `json:"usd_remaining"`
		USDUsed       float64 `json:"usd_pct_used"`
		Alert         bool    `json:"alert"`
	}

	var budgetOuts []budgetOut
	for _, b := range budgets {
		since := budget.PeriodStart(b.Period, time.Now())
		costMicros, tokens, err := s.usage.SpendAndTokens(ctx, b.ScopeKind, b.ScopeID, since)
		if err != nil {
			s.log.Error("key usage: spend lookup failed", "err", err)
			continue
		}

		bo := budgetOut{
			Period:      b.Period,
			LimitTokens: b.LimitTokens,
			TokensUsed:  tokens,
			LimitUSD:    float64(b.LimitMicros) / 1_000_000,
			SpentUSD:    float64(costMicros) / 1_000_000,
		}
		if b.LimitTokens > 0 {
			bo.TokensRemain = b.LimitTokens - tokens
			if bo.TokensRemain < 0 {
				bo.TokensRemain = 0
			}
			bo.TokensPctUsed = float64(tokens) / float64(b.LimitTokens) * 100
		}
		if b.LimitMicros > 0 {
			bo.USDRemaining = bo.LimitUSD - bo.SpentUSD
			if bo.USDRemaining < 0 {
				bo.USDRemaining = 0
			}
			bo.USDUsed = float64(costMicros) / float64(b.LimitMicros) * 100
		}
		if b.AlertPct > 0 {
			if (b.LimitMicros > 0 && costMicros*100 >= b.LimitMicros*int64(b.AlertPct)) ||
				(b.LimitTokens > 0 && tokens*100 >= b.LimitTokens*int64(b.AlertPct)) {
				bo.Alert = true
			}
		}
		budgetOuts = append(budgetOuts, bo)
	}

	allowedModels, modelsSource, err := s.identity.Keys().EffectiveAllowedModels(ctx, key.ID, key.PlanID)
	if err != nil {
		return nil, err
	}

	// Get current period summary scoped to this specific key.
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	summary, err := s.usage.SummarizeByKey(ctx, key.ID, periodStart)
	if err != nil {
		s.log.Error("key usage: summarize failed", "err", err)
	}

	// Daily usage series for the portal chart.
	daily, _ := s.usage.DailyByKey(ctx, key.ID, now.AddDate(0, 0, -days))
	var dailyOut []map[string]any
	for _, d := range daily {
		dailyOut = append(dailyOut, map[string]any{
			"date":              d.Date,
			"requests":          d.Requests,
			"prompt_tokens":     d.PromptTokens,
			"completion_tokens": d.CompletionTokens,
			"cost_usd":          float64(d.CostMicros) / 1_000_000,
		})
	}

	// Per-model breakdown for this key. Chain-routed requests are shown under
	// the user-facing chain name, not the upstream sub-model that served them.
	chainNames := s.chainNamesByTenant(ctx, key.TenantID)
	models, _ := s.usage.ByModelByKey(ctx, key.ID, now.AddDate(0, 0, -days))
	var modelOut []map[string]any
	for _, m := range models {
		modelOut = append(modelOut, map[string]any{
			"provider":          m.Provider,
			"model":             displayModel(m.Model, m.ChainID, chainNames),
			"total_requests":    m.TotalRequests,
			"prompt_tokens":     m.PromptTokens,
			"completion_tokens": m.CompletionTokens,
			"cost_usd":          float64(m.CostMicros) / 1_000_000,
		})
	}

	// Recent per-request records with token in/out and optimization flags.
	// Requests that ran through a chain are shown under the user-facing chain
	// name, not the upstream sub-model the request happened to land on. A
	// missing/unknown chain id falls back to the recorded model.
	recent, _ := s.usage.RecentByKey(ctx, key.ID, now.AddDate(0, 0, -days), 200)
	recentOut := make([]map[string]any, 0, len(recent))
	for _, rec := range recent {
		entry := map[string]any{
			"id":                rec.ID,
			"provider":          rec.Provider,
			"model":             displayModel(rec.Model, rec.ChainID, chainNames),
			"prompt_tokens":     rec.PromptTokens,
			"completion_tokens": rec.CompletionTokens,
			"cost_usd":          recordCostUSD(rec),
			"cache_hit":         rec.CacheHit,
			"latency_ms":        rec.LatencyMS,
			"created_at":        rec.CreatedAt,
			"optimizations":     []string{},
		}
		var optNames []string
		if rec.SlimActive || rec.SlimBytesSaved > 0 {
			if rec.SlimBytesSaved > 0 {
				entry["slim_bytes_saved"] = rec.SlimBytesSaved
				entry["slim_tokens_saved"] = rec.SlimTokensSaved
			}
			if rec.SlimRules != "" {
				entry["slim_rules"] = rec.SlimRules
			}
			optNames = append(optNames, "RTK")
		}
		if rec.CavemanActive {
			optNames = append(optNames, "Caveman")
		}
		if rec.TerseActive {
			optNames = append(optNames, "Terse")
		}
		if rec.HeadroomActive {
			entry["headroom_tokens_saved"] = rec.HeadroomTokensSaved
			optNames = append(optNames, "Headroom")
		}
		if rec.PonytailActive {
			optNames = append(optNames, "Ponytail")
		}
		entry["optimizations"] = optNames
		if rec.TTFTMS > 0 {
			entry["ttft_ms"] = rec.TTFTMS
		}
		recentOut = append(recentOut, entry)
	}

	return map[string]any{
		"key_id":         key.ID,
		"key_name":       key.Name,
		"budgets":        budgetOuts,
		"allowed_models": allowedModels,
		"models_source":  modelsSource,
		"current_period": map[string]any{
			"prompt_tokens":     summary.PromptTokens,
			"completion_tokens": summary.CompletionTokens,
			"total_requests":    summary.TotalRequests,
			"cost_usd":          float64(summary.CostMicros) / 1_000_000,
		},
		"daily":  dailyOut,
		"models": modelOut,
		"recent": recentOut,
		"days":   days,
	}, nil
}

// detectClient identifies the calling tool from request headers, used for
// telemetry, savings attribution, and client-specific quirks. Best-effort.
//
// Known clients map to stable friendly labels so they aggregate cleanly. Any
// other client is normalized from its User-Agent product token rather than
// dropped, so every request is attributable. Falls back to "unknown" when no
// usable signal exists, so optimization savings are never silently uncounted.
func detectClient(r *http.Request) string {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	switch {
	case strings.Contains(ua, "claude"):
		return "claude-code"
	case strings.Contains(ua, "cursor"):
		return "cursor"
	case strings.Contains(ua, "codex"):
		return "codex"
	case strings.Contains(ua, "cline"):
		return "cline"
	case strings.Contains(ua, "copilot"):
		return "copilot"
	case strings.Contains(ua, "kilo"):
		return "kilo-code"
	case strings.Contains(ua, "opencode"):
		return "opencode"
	case strings.Contains(ua, "droid"):
		return "droid"
	case strings.Contains(ua, "aider"):
		return "aider"
	case strings.Contains(ua, "roo"):
		return "roo-code"
	}
	// Generic fallback: derive a clean label from the User-Agent product token
	// (the text before the first '/' or whitespace), so any client is counted.
	if label := normalizeClientLabel(ua); label != "" {
		return label
	}
	// SDK callers often omit a descriptive UA but set a stainless language hint.
	if lang := strings.TrimSpace(r.Header.Get("x-stainless-lang")); lang != "" {
		return "sdk-" + sanitizeClientToken(strings.ToLower(lang))
	}
	return "unknown"
}

// normalizeClientLabel extracts a stable, lowercase client label from a
// User-Agent string by taking the leading product token (before '/' or space)
// and stripping noise. Returns "" when nothing usable remains.
func normalizeClientLabel(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return ""
	}
	// Take the first product token: "foo-cli/1.2.3 (...)" -> "foo-cli".
	token := ua
	if i := strings.IndexAny(token, "/ \t"); i >= 0 {
		token = token[:i]
	}
	token = sanitizeClientToken(token)
	// Ignore generic HTTP libraries that carry no product identity.
	switch token {
	case "", "mozilla", "python-requests", "python", "go-http-client",
		"node-fetch", "axios", "curl", "okhttp", "java", "undici":
		return ""
	}
	return token
}

// sanitizeClientToken keeps only [a-z0-9-_.] and trims separators, so labels
// are safe to store and group on without surprising characters.
func sanitizeClientToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-_.")
}

func requestAffinityKey(r *http.Request, req *core.ChatRequest) string {
	for _, header := range affinityHeaders {
		if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
			return hashAffinityValue("header:"+strings.ToLower(header), v)
		}
	}

	if v := extraAffinityKey(req); v != "" {
		return hashAffinityValue("body", v)
	}
	if req == nil {
		return ""
	}
	seed := conversationSeed(req)
	if seed == "" {
		return ""
	}
	return hashAffinityValue("fingerprint", seed)
}

func hashAffinityValue(source, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(source + "\x00" + value))
	return source + ":" + hex.EncodeToString(sum[:])
}

// affinityHeaders is the ordered list of HTTP headers checked for routing affinity.
var affinityHeaders = []string{
	"X-KeiRouter-Affinity",
	"X-Conversation-ID",
	"X-Thread-ID",
	"X-Session-ID",
	"X-Amp-Thread-ID",
	"X-Client-Request-ID",
	"OpenAI-Conversation-ID",
}

// extraAffinityKey extracts an affinity key from the already-parsed
// ChatRequest.Extra map, avoiding a full JSON re-parse of the request body.
func extraAffinityKey(req *core.ChatRequest) string {
	if req == nil || len(req.Extra) == 0 {
		return ""
	}
	for _, key := range affinityBodyKeys {
		if v := rawString(req.Extra[key]); v != "" {
			return key + ":" + v
		}
	}
	if v := rawString(req.Extra["conversation"]); v != "" {
		return "conversation:" + v
	}
	if v := rawObjectString(req.Extra["conversation"], "id"); v != "" {
		return "conversation.id:" + v
	}
	if v := rawObjectString(req.Extra["metadata"], "conversation_id"); v != "" {
		return "metadata.conversation_id:" + v
	}
	if v := rawObjectString(req.Extra["metadata"], "thread_id"); v != "" {
		return "metadata.thread_id:" + v
	}
	if v := rawObjectString(req.Extra["metadata"], "session_id"); v != "" {
		return "metadata.session_id:" + v
	}
	if v := rawObjectString(req.Extra["metadata"], "user_id"); v != "" {
		return "metadata.user_id:" + v
	}
	return ""
}

// affinityBodyKeys are the top-level JSON keys checked for routing affinity.
var affinityBodyKeys = []string{
	"conversation_id",
	"thread_id",
	"session_id",
	"prompt_cache_key",
	"previous_response_id",
	"parent_id",
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func rawObjectString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return rawString(obj[key])
}

func conversationSeed(req *core.ChatRequest) string {
	var b strings.Builder
	b.WriteString(req.Metadata.APIKeyID)
	b.WriteByte('\n')
	b.WriteString(req.Metadata.ClientKind)
	b.WriteByte('\n')
	b.WriteString(string(req.Metadata.SourceDialect))
	b.WriteByte('\n')
	b.WriteString(req.Model)
	if system := strings.TrimSpace(req.System); system != "" {
		b.WriteString("\nsystem:")
		b.WriteString(limitAffinityText(system))
	}
	seenText := 0
	for _, msg := range req.Messages {
		if msg.Role != core.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.TextContent())
		if text == "" {
			continue
		}
		b.WriteString("\nuser:")
		b.WriteString(limitAffinityText(text))
		seenText++
		if seenText >= 1 {
			break
		}
	}
	if seenText == 0 {
		return ""
	}
	return b.String()
}

func limitAffinityText(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// recordCostUSD is a usage record's billed cost in USD at full nano precision,
// so a sub-micro charge is shown as charged rather than rounded to zero. Legacy
// rows without nanos fall back to micros.
func recordCostUSD(rec store.RecentRecord) float64 {
	if rec.CostNanos != 0 {
		return float64(rec.CostNanos) / 1_000_000_000
	}
	return float64(rec.CostMicros) / 1_000_000
}
