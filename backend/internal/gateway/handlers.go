package gateway

import (
	"bufio"
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

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/limits"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// maxBodyBytes caps inbound request bodies to protect against oversized uploads.
const maxBodyBytes = 32 << 20 // 32 MiB

// logRequest logs a completed request to the console log buffer.
func (s *Server) logRequest(keyName, provider, model string, tokens int, costMicros int64, latencyMs int, cacheHit bool, err error) {
	if s.consoleLog == nil {
		return
	}
	level := "INFO"
	if err != nil {
		level = "ERROR"
	} else if latencyMs > 8000 {
		level = "WARN"
	}
	cost := float64(costMicros) / 1_000_000
	cache := ""
	if cacheHit {
		cache = " · cache"
	}
	s.consoleLog.Logf(level, "[%s] %s · %s · %d tok · $%.4f · %dms%s",
		keyName, provider, model, tokens, cost, latencyMs, cache)
	if err != nil {
		s.consoleLog.Logf("ERROR", "  └─ %v", err)
	}
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
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
	if req == nil {
		return 0
	}
	chars := len(req.System)
	for _, m := range req.Messages {
		for _, part := range m.Content {
			chars += len(part.Text)
			if part.ToolCall != nil {
				chars += len(part.ToolCall.Arguments)
			}
			if part.ToolResult != nil {
				chars += len(part.ToolResult.Content)
			}
		}
	}
	for _, t := range req.Tools {
		chars += len(t.Name) + len(t.Description) + len(t.Parameters)
	}
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
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

	s.consoleLog.Logf("DEBUG", "→ %s %s · dialect=%s · client=%s · key=%s (%s)",
		r.Method, r.URL.Path, dialect, client, key.Name, key.ID)

	codec, err := s.codecs.Codec(dialect)
	if err != nil {
		s.consoleLog.Logf("ERROR", "unsupported dialect: %s", dialect)
		writeError(w, http.StatusInternalServerError, "unsupported dialect")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.consoleLog.Logf("ERROR", "failed to read body: %v", err)
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	s.consoleLog.Logf("DEBUG", "  body read: %d bytes", len(body))

	req, err := codec.ParseRequest(body)
	if err != nil {
		s.consoleLog.Logf("ERROR", "parse failed: %v", err)
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

	s.consoleLog.Logf("DEBUG", "  model=%s · stream=%v · messages=%d · tenant=%s · key=%s (%s)",
		req.Model, req.Stream, len(req.Messages), tenantID, key.Name, key.ID)

	resolved, err := resolveTargets(r.Context(), s.chains, s.aliases, tenantID, req.Model)
	if err != nil {
		var bad badModelError
		if errors.As(err, &bad) {
			s.consoleLog.Logf("WARN", "bad model: %s", bad.Error())
			writeError(w, http.StatusBadRequest, bad.Error())
			return
		}
		s.consoleLog.Logf("ERROR", "resolve targets failed: %v", err)
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
		filtered, ferr := s.filterAllowedTargets(r.Context(), key.ID, resolved.Targets)
		if ferr != nil {
			s.consoleLog.Logf("ERROR", "model access check failed: %v", ferr)
			writeError(w, http.StatusInternalServerError, "model access check failed")
			return
		}
		if len(filtered) == 0 {
			s.consoleLog.Logf("WARN", "model access denied: key=%s (%s) model=%s", key.Name, key.ID, req.Model)
			writeError(w, http.StatusForbidden, "access denied: this API key is not permitted to use model "+req.Model)
			return
		}
		resolved.Targets = filtered
	}

	for i, t := range resolved.Targets {
		s.consoleLog.Logf("DEBUG", "  target[%d]: %s/%s", i, t.Provider, t.Model)
	}
	affinityKey := requestAffinityKey(r, req)
	req.Metadata.ContextAffinityKey = affinityKey
	body = nil // release body for GC — no longer needed

	effectiveLimits, err := s.effectiveLimits(r.Context(), key)
	if err != nil {
		s.consoleLog.Logf("ERROR", "limit resolution failed: %v", err)
		writeError(w, http.StatusInternalServerError, "limit resolution failed")
		return
	}

	opts := pipeline.Options{
		Targets:  resolved.Targets,
		PlanOpts: s.endpointPlanOptions(r.Context(), resolved.PlanOpts, resolved.Targets, affinityKey),
		Slimmer:  s.slimmerConfig(),
		Headroom: s.headroomConfig(),
		Terse:    s.terseConfig(),
		Caveman:  s.cavemanConfig(),
		Limits:   effectiveLimits,
	}

	if req.Stream {
		s.consoleLog.Logf("DEBUG", "  entering stream path")
		s.streamChat(w, r, codec, req, opts, key.Name)
		return
	}
	s.consoleLog.Logf("DEBUG", "  entering unary path")
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
	s.consoleLog.Logf("DEBUG", "  ▶ pipeline.Chat() start")
	result, err := s.pipeline.Chat(r.Context(), req, opts)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		s.consoleLog.Logf("ERROR", "  ✖ pipeline.Chat() failed after %dms: %v", latency, err)
		s.logRequest(keyName, req.Model, req.Model, 0, 0, latency, false, err)
		s.writeProviderError(w, err)
		return
	}

	out, err := codec.RenderResponse(result.Response)
	if err != nil {
		s.consoleLog.Logf("ERROR", "  ✖ render failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to render response")
		return
	}
	tokens := result.Response.Usage.PromptTokens + result.Response.Usage.CompletionTokens
	s.consoleLog.Logf("DEBUG", "  ✔ [%s] %s/%s · %d tok · acct=%s · cache=%v · %dms",
		keyName, result.Provider, result.Model, tokens, result.AccountID, result.CacheHit, latency)
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
	s.consoleLog.Logf("DEBUG", "  ▶ pipeline.Stream() start")
	result, err := s.pipeline.Stream(r.Context(), req, opts)
	if err != nil {
		latency := int(time.Since(start).Milliseconds())
		s.consoleLog.Logf("ERROR", "  ✖ pipeline.Stream() failed after %dms: %v", latency, err)
		s.logRequest(keyName, req.Model, req.Model, 0, 0, latency, false, err)
		s.writeProviderError(w, err)
		return
	}
	s.consoleLog.Logf("DEBUG", "  ✔ [%s] stream connected: %s/%s · acct=%s",
		keyName, result.Provider, result.Model, result.AccountID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-KeiRouter-Provider", result.Provider)
	w.Header().Set("X-KeiRouter-Model", result.Model)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Zero-copy direct pipe path: the pipeline detected same-dialect, no-tools
	// and obtained a raw io.ReadCloser from the upstream. Pipe it directly to
	// the client via io.Copy — no JSON parse/serialize, no goroutines, minimal
	// memory allocation. This is the fastest possible streaming path.
	if result.DirectBody != nil {
		defer result.DirectBody.Close()
		n, cpErr := io.Copy(w, result.DirectBody)
		if cpErr != nil && !isClientDisconnect(cpErr) {
			s.consoleLog.Logf("ERROR", "  ✖ direct pipe error after %d bytes: %v", n, cpErr)
			s.log.Warn("direct pipe error", "bytes", n, "err", cpErr)
		}
		flusher.Flush()
		// Record usage from the captured SSE stream. The pipeline wraps
		// the direct body in a tee reader that captures all bytes; the
		// DirectUsageFunc parses the captured data for usage tokens.
		if result.DirectUsageFunc != nil {
			result.DirectUsageFunc()
		}
		latency := int(time.Since(start).Milliseconds())
		s.consoleLog.Logf("DEBUG", "  ✔ [%s] direct pipe done: %d bytes · %dms", keyName, n, latency)
		s.logRequest(keyName, result.Provider, result.Model, 0, 0, latency, false, nil)
		return
	}

	// Wrap the response writer in a bufio.Writer to batch small SSE writes
	// into fewer syscalls. The pool avoids allocating a new writer per request.
	bw := core.SSEWriterPool.Get().(*bufio.Writer)
	defer core.SSEWriterPool.Put(bw)
	bw.Reset(w)

	state := &transform.StreamState{Model: result.Model}
	streamStart := time.Now()
	var totalTokens int
	var chunkCount int

	// ToolArgSanitizer buffers streaming tool call arguments and emits
	// sanitized JSON when each tool call completes. This fixes malformed
	// arguments from non-Anthropic models (e.g., Read.limit as string).
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
						s.consoleLog.Logf("WARN", "  client disconnected after %d chunks", chunkCount)
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
				s.consoleLog.Logf("WARN", "  client disconnected after %d chunks", chunkCount)
				return
			}
		}
		// Flush the buffered writer to the underlying http.ResponseWriter,
		// then flush the HTTP flusher to push bytes to the client.
		bw.Flush()
		flusher.Flush()
	}

	for chunk := range result.Chunks {
		if chunk.Type == core.ChunkError {
			s.consoleLog.Logf("ERROR", "  ✖ stream chunk error: %v", chunk.Err)
			s.log.Warn("stream error", "err", chunk.Err)
			break
		}
		if chunk.Type == core.ChunkUsage && chunk.Usage != nil {
			totalTokens = chunk.Usage.PromptTokens + chunk.Usage.CompletionTokens
		}
		chunkCount++
		sanitizer.Process(chunk, renderChunk)
	}

	// Flush any remaining buffered tool calls and think-tag buffer.
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

	latency := int(time.Since(streamStart).Milliseconds())
	s.consoleLog.Logf("DEBUG", "  ✔ [%s] stream done: %d chunks · %d tok · %dms", keyName, chunkCount, totalTokens, latency)
	s.logRequest(keyName, result.Provider, result.Model, totalTokens, 0, latency, false, nil)
}

// writeProviderError maps a structured provider error to an HTTP status.
func (s *Server) writeProviderError(w http.ResponseWriter, err error) {
	pe := core.AsProviderError(err)
	status := http.StatusBadGateway
	switch pe.Kind {
	case core.ErrBadRequest:
		status = http.StatusBadRequest
	case core.ErrAuth:
		status = http.StatusUnauthorized
	case core.ErrRateLimit:
		status = http.StatusTooManyRequests
		if pe.RetryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", pe.RetryAfter.Seconds()))
		}
	case core.ErrQuotaExhausted, core.ErrBudgetBlocked:
		status = http.StatusPaymentRequired
	case core.ErrTimeout:
		status = http.StatusGatewayTimeout
	case core.ErrInternal:
		status = http.StatusInternalServerError
	}
	writeError(w, status, pe.Message)
}

// isClientDisconnect reports whether an error is a client disconnection
// (broken pipe, reset by peer) rather than a server-side failure. These are
// expected during streaming and should not be logged as errors.
func isClientDisconnect(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "reset by peer") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "use of closed network connection")
}

// filterAllowedTargets filters resolved routing targets to only include models
// the given API key is allowed to access. Returns empty slice if no target
// matches the key's model access policy.
func (s *Server) filterAllowedTargets(ctx context.Context, keyID string, targets []dispatch.Target) ([]dispatch.Target, error) {
	keys := s.identity.Keys()
	allowed, err := keys.GetAllowedModels(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return targets, nil // no restriction
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
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	summary, err := s.usage.SummarizeByKey(ctx, key.ID, periodStart)
	if err != nil {
		s.log.Error("key usage: summarize failed", "err", err)
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
	})
}

func (s *Server) handlePortalKeyUsage(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	if keyID == "" {
		writeError(w, http.StatusBadRequest, "missing key id")
		return
	}

	ctx := r.Context()
	key, err := s.identity.Keys().Get(ctx, keyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get key")
		return
	}

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
		writeError(w, http.StatusInternalServerError, "failed to get model access")
		return
	}

	// Get current period summary scoped to this specific key.
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	summary, err := s.usage.SummarizeByKey(ctx, key.ID, periodStart)
	if err != nil {
		s.log.Error("key usage: summarize failed", "err", err)
	}

	// Daily usage series for the portal chart (last 30 days).
	daily, _ := s.usage.DailyByKey(ctx, key.ID, now.AddDate(0, 0, -30))
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

	// Per-model breakdown for this key (last 30 days).
	models, _ := s.usage.ByModelByKey(ctx, key.ID, now.AddDate(0, 0, -30))
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
