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
	"net/http"
	"strings"
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

	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		message := fmt.Sprintf("request body exceeds maximum allowed size of %s", humanBytes(int(limit)))
		if s.consoleLog != nil {
			s.consoleLog.Log("WARN", "Request body too large", message)
		}
		writeError(w, http.StatusRequestEntityTooLarge, message)
		return nil, false
	}

	if s.consoleLog != nil {
		s.consoleLog.Log("ERROR", "Failed to read request body", err.Error())
	}
	writeError(w, http.StatusBadRequest, "failed to read request body")
	return nil, false
}

// logRequest logs a completed request to the console log buffer.
func (s *Server) logRequest(keyName, provider, model string, tokens int, costMicros int64, latencyMs int, cacheHit bool, err error) {
	if s.consoleLog == nil {
		return
	}

	if err != nil {
		detail := fmt.Sprintf("Key:      %s\nProvider: %s\nModel:    %s\nLatency:  %dms\n\n%v",
			keyName, provider, model, latencyMs, err)
		s.consoleLog.Log("ERROR",
			fmt.Sprintf("Request failed · %s · %s", model, humanDuration(latencyMs)),
			detail)
		return
	}

	level := "INFO"
	if latencyMs > 8000 {
		level = "WARN"
	}
	cost := float64(costMicros) / 1_000_000
	cacheNote := ""
	if cacheHit {
		cacheNote = " · cache hit"
	}
	msg := fmt.Sprintf("Request completed · %s · %s tokens · $%.4f · %s%s",
		model, humanInt(tokens), cost, humanDuration(latencyMs), cacheNote)
	detail := fmt.Sprintf(
		"Key:      %s\nProvider: %s\nModel:    %s\nTokens:   %s\nCost:     $%.4f\nLatency:  %dms\nCache:    %v",
		keyName, provider, model, humanInt(tokens), cost, latencyMs, cacheHit)
	s.consoleLog.Log(level, msg, detail)
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

	s.consoleLog.Log("DEBUG",
		fmt.Sprintf("New request from %q (%s API)", client, dialect),
		fmt.Sprintf("Method: %s\nPath:   %s\nClient: %s\nDialect: %s\nKey:    %s (%s)",
			r.Method, r.URL.Path, client, dialect, key.Name, key.ID))

	codec, err := s.codecs.Codec(dialect)
	if err != nil {
		s.consoleLog.Log("ERROR", fmt.Sprintf("Unsupported API dialect: %s", dialect), "")
		writeError(w, http.StatusInternalServerError, "unsupported dialect")
		return
	}

	body, ok := s.readRequestBody(w, r)
	if !ok {
		return
	}
	s.consoleLog.Log("DEBUG", fmt.Sprintf("Read request body (%s)", humanBytes(len(body))), "")

	req, err := codec.ParseRequest(body)
	if err != nil {
		s.consoleLog.Log("ERROR", "Failed to parse request body", err.Error())
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	// Attach routing metadata.
	req.Metadata = core.RequestMetadata{
		ClientKind:    client,
		SourceDialect: dialect,
		APIKeyID:      key.ID,
		TenantID:      tenantID,
		ProjectID:     key.ProjectID,
		RequestID:     chimiddleware.GetReqID(r.Context()),
	}

	streamNote := ""
	if req.Stream {
		streamNote = " · streaming"
	}
	s.consoleLog.Log("DEBUG",
		fmt.Sprintf("Routing %q · %d message%s%s", req.Model, len(req.Messages), plural(len(req.Messages)), streamNote),
		fmt.Sprintf("Model:    %s\nMessages: %d\nStream:   %v\nTenant:   %s\nKey:      %s (%s)",
			req.Model, len(req.Messages), req.Stream, tenantID, key.Name, key.ID))

	resolved, err := resolveTargets(r.Context(), s.chains, s.aliases, s.latencyReader(), tenantID, req.Model)
	if err != nil {
		var bad badModelError
		if errors.As(err, &bad) {
			s.consoleLog.Log("WARN", fmt.Sprintf("Unknown model %q", req.Model), bad.Error())
			writeError(w, http.StatusBadRequest, bad.Error())
			return
		}
		s.consoleLog.Log("ERROR", fmt.Sprintf("Failed to resolve model %q", req.Model), err.Error())
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
		filtered, ferr := s.filterAllowedTargets(r.Context(), key.ID, req.Model, resolved.PlanOpts.ChainID != "", resolved.Targets)
		if ferr != nil {
			s.consoleLog.Log("ERROR", "Model access check failed", ferr.Error())
			writeError(w, http.StatusInternalServerError, "model access check failed")
			return
		}
		if len(filtered) == 0 {
			s.consoleLog.Log("WARN",
				fmt.Sprintf("Access denied · key %q may not use %q", key.Name, req.Model),
				fmt.Sprintf("Key:   %s (%s)\nModel: %s", key.Name, key.ID, req.Model))
			writeError(w, http.StatusForbidden, "access denied: this API key is not permitted to use model "+req.Model)
			return
		}
		resolved.Targets = filtered
	}

	if len(resolved.Targets) > 0 {
		primary := resolved.Targets[0]
		var tb strings.Builder
		for i, t := range resolved.Targets {
			if i > 0 {
				tb.WriteByte('\n')
			}
			fmt.Fprintf(&tb, "%d. %s/%s", i+1, t.Provider, t.Model)
		}
		msg := fmt.Sprintf("Resolved to %s/%s", primary.Provider, primary.Model)
		if len(resolved.Targets) > 1 {
			msg = fmt.Sprintf("%s (+%d fallback%s)", msg, len(resolved.Targets)-1, plural(len(resolved.Targets)-1))
		}
		s.consoleLog.Log("DEBUG", msg, tb.String())
	}
	affinityKey := requestAffinityKey(r, req)
	req.Metadata.ContextAffinityKey = affinityKey
	body = nil // release body for GC — no longer needed

	effectiveLimits, err := s.effectiveLimits(r.Context(), key)
	if err != nil {
		s.consoleLog.Log("ERROR", "Failed to resolve rate limits", err.Error())
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
		Limits:   effectiveLimits,
	}

	if req.Stream {
		s.consoleLog.Log("DEBUG", "Dispatching as streaming response", "")
		s.streamChat(w, r, codec, req, opts, key.Name)
		return
	}
	s.consoleLog.Log("DEBUG", "Dispatching as standard response", "")
	s.unaryChat(w, r, codec, req, opts, key.Name)
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
func (s *Server) unaryChat(w http.ResponseWriter, r *http.Request, codec transform.Codec, req *core.ChatRequest, opts pipeline.Options, keyName string) {
	start := time.Now()
	s.consoleLog.Log("DEBUG", "Sending request to provider…", "")
	result, err := s.pipeline.Chat(r.Context(), req, opts)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		s.consoleLog.Log("ERROR", fmt.Sprintf("Provider request failed after %s", humanDuration(latency)), err.Error())
		s.logRequest(keyName, req.Model, req.Model, 0, 0, latency, false, err)
		s.writeProviderError(w, err)
		return
	}

	out, err := codec.RenderResponse(result.Response)
	if err != nil {
		s.consoleLog.Log("ERROR", "Failed to render provider response", err.Error())
		writeError(w, http.StatusInternalServerError, "failed to render response")
		return
	}
	tokens := result.Response.Usage.PromptTokens + result.Response.Usage.CompletionTokens
	s.consoleLog.Log("DEBUG",
		fmt.Sprintf("Response from %s/%s · %s tokens · %s", result.Provider, result.Model, humanInt(tokens), humanDuration(latency)),
		fmt.Sprintf("Provider: %s\nModel:    %s\nTokens:   %s\nAccount:  %s\nCache:    %v\nLatency:  %dms",
			result.Provider, result.Model, humanInt(tokens), result.AccountID, result.CacheHit, latency))
	s.logRequest(keyName, result.Provider, result.Model, tokens, result.CostMicros, latency, result.CacheHit, nil)
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
func (s *Server) streamChat(w http.ResponseWriter, r *http.Request, codec transform.Codec, req *core.ChatRequest, opts pipeline.Options, keyName string) {
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

	start := time.Now()
	s.consoleLog.Log("DEBUG", "Opening stream to provider…", "")
	result, err := s.pipeline.Stream(r.Context(), req, opts)
	if err != nil {
		latency := int(time.Since(start).Milliseconds())
		s.consoleLog.Log("ERROR", fmt.Sprintf("Stream failed to start after %s", humanDuration(latency)), err.Error())
		s.logRequest(keyName, req.Model, req.Model, 0, 0, latency, false, err)
		s.writeProviderError(w, err)
		return
	}
	s.consoleLog.Log("DEBUG",
		fmt.Sprintf("Streaming from %s/%s", result.Provider, result.Model),
		fmt.Sprintf("Provider: %s\nModel:    %s\nAccount:  %s", result.Provider, result.Model, result.AccountID))

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
		n, cpErr := copySanitizedStream(dst, result.DirectBody, req.Metadata.SourceDialect, frameFlush)
		if hw != nil {
			hw.stop()
		}
		if cpErr != nil && !isClientDisconnect(cpErr) {
			s.consoleLog.Log("ERROR", fmt.Sprintf("Stream interrupted after %s", humanBytes(int(n))), cpErr.Error())
			s.log.Warn("direct stream error", "bytes", n, "err", cpErr)
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
		latency := int(time.Since(start).Milliseconds())
		if cpErr != nil {
			s.logRequest(keyName, result.Provider, result.Model, 0, 0, latency, false, cpErr)
			return
		}
		s.consoleLog.Log("DEBUG", fmt.Sprintf("Stream finished · %s · %s", humanBytes(int(n)), humanDuration(latency)), "")
		s.logRequest(keyName, result.Provider, result.Model, 0, 0, latency, false, nil)
		return
	}

	// Wrap the response writer in a bufio.Writer to batch small SSE writes
	// into fewer syscalls. The pool avoids allocating a new writer per request.
	bw := core.SSEWriterPool.Get().(*bufio.Writer)
	defer core.SSEWriterPool.Put(bw)
	bw.Reset(w)

	state := &transform.StreamState{Model: result.Model}
	transform.ResetStreamState(state)
	streamStart := time.Now()
	var totalTokens int
	var chunkCount int

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
						s.consoleLog.Log("WARN", fmt.Sprintf("Client disconnected after %d chunks", chunkCount), "")
						return
					}
				}
			}
			bw.Flush()
			flusher.Flush()
			return
		}
		events, rerr := streamCodec.RenderStreamChunk(cleaned, state)
		if rerr != nil {
			s.log.Warn("failed to render stream chunk", "err", rerr)
			return
		}
		for _, ev := range events {
			if _, werr := bw.Write(ev); werr != nil {
				s.consoleLog.Log("WARN", fmt.Sprintf("Client disconnected after %d chunks", chunkCount), "")
				return
			}
		}
		// Flush the buffered writer to the underlying http.ResponseWriter,
		// then flush the HTTP flusher to push bytes to the client.
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
				s.consoleLog.Log("ERROR", "Provider stream error", fmt.Sprintf("%v", streamErr))
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
			chunkCount++
			sanitizer.Process(chunk, renderChunk)
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

	latency := int(time.Since(streamStart).Milliseconds())
	if streamErr != nil {
		s.logRequest(keyName, result.Provider, result.Model, totalTokens, 0, latency, false, streamErr)
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

	for _, ev := range streamCodec.RenderStreamDone(state) {
		_, _ = bw.Write(ev)
	}
	bw.Flush()
	flusher.Flush()

	s.consoleLog.Log("DEBUG",
		fmt.Sprintf("Stream complete · %d chunks · %s tokens · %s", chunkCount, humanInt(totalTokens), humanDuration(latency)),
		fmt.Sprintf("Provider: %s\nModel:    %s\nChunks:   %d\nTokens:   %s\nLatency:  %dms",
			result.Provider, result.Model, chunkCount, humanInt(totalTokens), latency))
	s.logRequest(keyName, result.Provider, result.Model, totalTokens, 0, latency, false, nil)
}

// providerStreamEventError keeps a late provider error available to internal
// logs after its wire payload has been replaced with a generic client message.
type providerStreamEventError struct{ detail string }

func (e *providerStreamEventError) Error() string { return "provider stream error: " + e.detail }

type streamReadError struct{ err error }

func (e *streamReadError) Error() string { return e.err.Error() }
func (e *streamReadError) Unwrap() error { return e.err }

type streamWriteError struct{ err error }

func (e *streamWriteError) Error() string { return e.err.Error() }
func (e *streamWriteError) Unwrap() error { return e.err }

// copySanitizedStream keeps the direct-stream path lightweight by framing SSE
// events without decoding successful chunks. Only potential error events are
// decoded; those are replaced with a dialect-compatible generic event.
func copySanitizedStream(dst io.Writer, src io.Reader, dialect core.Dialect, flush func()) (int64, error) {
	reader := bufio.NewReaderSize(src, 64*1024)
	if dialect == core.DialectOllama {
		return copySanitizedNDJSON(dst, reader, dialect, flush)
	}

	var event bytes.Buffer
	var written int64
	for {
		line, readErr := reader.ReadSlice('\n')
		if len(line) > 0 {
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
	providerErr := hasStreamErrorMarker(raw) && isProviderStreamError(string(raw))
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

func hasStreamErrorMarker(frame []byte) bool {
	return bytes.Contains(frame, []byte("error")) || bytes.Contains(frame, []byte("Error")) ||
		bytes.Contains(frame, []byte("ERROR")) || bytes.Contains(frame, []byte("failed")) ||
		bytes.Contains(frame, []byte("Failed")) || bytes.Contains(frame, []byte("FAILED"))
}

func isProviderStreamError(event string) bool {
	lowerEvent := strings.ToLower(event)
	if !strings.Contains(lowerEvent, "error") && !strings.Contains(lowerEvent, "failed") {
		return false
	}

	var data strings.Builder
	for _, line := range strings.Split(event, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		switch {
		case strings.HasPrefix(line, "event:"):
			name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "event:")))
			if strings.Contains(name, "error") || strings.Contains(name, "failed") {
				return true
			}
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload != "" && payload != "[DONE]" {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(payload)
			}
		}
	}
	if data.Len() == 0 {
		standalone := strings.TrimSpace(event)
		if !strings.HasPrefix(standalone, "{") {
			return false
		}
		data.WriteString(standalone)
	}

	var envelope struct {
		Type   string          `json:"type"`
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(data.String()), &envelope); err != nil {
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
// keeping the provider's original message in internal logs only.
func (s *Server) writeProviderError(w http.ResponseWriter, err error) {
	pe := core.AsProviderError(err)
	status := http.StatusBadGateway
	switch pe.Kind {
	case core.ErrBadRequest, core.ErrCapability:
		status = http.StatusBadRequest
	case core.ErrModelUnavailable:
		status = http.StatusNotFound
	case core.ErrAuth:
		status = http.StatusUnauthorized
	case core.ErrRateLimit:
		status = http.StatusTooManyRequests
		if pe.RetryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", pe.RetryAfter.Seconds()))
		}
	case core.ErrQuotaExhausted, core.ErrBudgetBlocked:
		status = http.StatusPaymentRequired
	case core.ErrPolicyBlocked:
		status = http.StatusForbidden
	case core.ErrTimeout:
		status = http.StatusGatewayTimeout
	case core.ErrInternal:
		status = http.StatusInternalServerError
	}

	if s.log != nil {
		s.log.Error("provider request failed",
			"kind", pe.Kind,
			"provider", pe.Provider,
			"model", pe.Model,
			"upstream_status", pe.StatusCode,
			"error", err)
	}
	writeError(w, status, sanitizeUpstreamError(pe))
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
func (s *Server) filterAllowedTargets(ctx context.Context, keyID, requestedModel string, isChain bool, targets []dispatch.Target) ([]dispatch.Target, error) {
	keys := s.identity.Keys()
	allowed, err := keys.GetAllowedModels(ctx, keyID)
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
	allowedModels, err := s.identity.Keys().GetAllowedModels(ctx, key.ID)
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

	models, _ := s.usage.ByModelByKey(ctx, key.ID, now.AddDate(0, 0, -30))
	var modelOut []map[string]any
	for _, m := range models {
		modelOut = append(modelOut, map[string]any{
			"provider": m.Provider, "model": m.Model,
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

	allowedModels, err := s.identity.Keys().GetAllowedModels(ctx, key.ID)
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

	// Per-model breakdown for this key.
	models, _ := s.usage.ByModelByKey(ctx, key.ID, now.AddDate(0, 0, -days))
	var modelOut []map[string]any
	for _, m := range models {
		modelOut = append(modelOut, map[string]any{
			"provider":          m.Provider,
			"model":             m.Model,
			"total_requests":    m.TotalRequests,
			"prompt_tokens":     m.PromptTokens,
			"completion_tokens": m.CompletionTokens,
			"cost_usd":          float64(m.CostMicros) / 1_000_000,
		})
	}

	// Recent per-request records with token in/out and optimization flags.
	recent, _ := s.usage.RecentByKey(ctx, key.ID, now.AddDate(0, 0, -days), 200)
	recentOut := make([]map[string]any, 0, len(recent))
	for _, rec := range recent {
		entry := map[string]any{
			"id":                rec.ID,
			"provider":          rec.Provider,
			"model":             rec.Model,
			"prompt_tokens":     rec.PromptTokens,
			"completion_tokens": rec.CompletionTokens,
			"cost_usd":          float64(rec.CostMicros) / 1_000_000,
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
