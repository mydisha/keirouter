// Package pipeline orchestrates the request lifecycle: it applies token-saving
// transforms, enforces budgets, selects an account via the dispatcher, executes
// the upstream call with fallback, and records usage.
//
// It operates entirely on canonical core types; dialect translation happens at
// the gateway edge, before and after the pipeline. Both unary and streaming
// paths share the same candidate-selection and fallback logic.
package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/cache"
	"github.com/mydisha/keirouter/backend/internal/capability"
	"github.com/mydisha/keirouter/backend/internal/caveman"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/headroom"
	"github.com/mydisha/keirouter/backend/internal/limits"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/normalizer"
	"github.com/mydisha/keirouter/backend/internal/observ"
	"github.com/mydisha/keirouter/backend/internal/slimmer"
	"github.com/mydisha/keirouter/backend/internal/terse"
)

// TimeoutReader provides dynamic timeout values that can be updated at runtime.
type TimeoutReader interface {
	StreamStallTimeout() time.Duration
	ResponseHeaderTimeout() time.Duration
	RequestTimeout() time.Duration
}

// CooldownRetryMax is the maximum time to wait for a cooldown to expire
// before giving up. All accounts on cooldown is a transient state; a short
// wait is better than an immediate failure to the client.
const CooldownRetryMax = 10 * time.Second

// Pipeline wires the request-processing stages together.
type Pipeline struct {
	dispatcher *dispatch.Dispatcher
	meter      *meter.Meter
	budget     *budget.Engine
	slimmer    *slimmer.Engine
	headroom   *headroom.Client
	metrics    *observ.Metrics
	cache      *cache.Cache
	embedder   cache.Embedder
	guardrails *guardrails.Engine
	limiter    limits.Limiter
	log        *slog.Logger

	requestTimeout     time.Duration // upper bound on non-streaming upstream calls
	streamStallTimeout time.Duration // aborts a stream with no bytes for this long
	timeoutReader      TimeoutReader // dynamic timeout source (from dashboard settings)
}

// Deps bundles the pipeline's collaborators.
type Deps struct {
	Dispatcher *dispatch.Dispatcher
	Meter      *meter.Meter
	Budget     *budget.Engine
	Slimmer    *slimmer.Engine
	Headroom   *headroom.Client
	Metrics    *observ.Metrics
	Cache      *cache.Cache
	Embedder   cache.Embedder
	Guardrails *guardrails.Engine
	Limiter    limits.Limiter
	Logger     *slog.Logger

	// RequestTimeout bounds non-streaming upstream calls. Zero means no limit.
	RequestTimeout time.Duration
	// StreamStallTimeout aborts a stream that produces no bytes for this
	// long. Zero means no stall detection.
	StreamStallTimeout time.Duration
	// TimeoutReader provides dynamic timeout values from dashboard settings.
	// When set, it overrides RequestTimeout and StreamStallTimeout.
	TimeoutReader TimeoutReader
}

// New builds a Pipeline.
func New(d Deps) *Pipeline {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Pipeline{
		dispatcher: d.Dispatcher,
		meter:      d.Meter,
		budget:     d.Budget,
		slimmer:    d.Slimmer,
		headroom:   d.Headroom,
		metrics:    d.Metrics,
		cache:      d.Cache,
		embedder:   d.Embedder,
		guardrails: d.Guardrails,
		limiter:    d.Limiter,
		log:        log,

		requestTimeout:     d.RequestTimeout,
		streamStallTimeout: d.StreamStallTimeout,
		timeoutReader:      d.TimeoutReader,
	}
}

// resolvedRequestTimeout returns the effective request timeout, preferring
// the dynamic dashboard value when available.
func (p *Pipeline) resolvedRequestTimeout() time.Duration {
	if p.timeoutReader != nil {
		if t := p.timeoutReader.RequestTimeout(); t > 0 {
			return t
		}
	}
	return p.requestTimeout
}

// resolvedStallTimeout returns the effective stream stall timeout, preferring
// the dynamic dashboard value when available.
func (p *Pipeline) resolvedStallTimeout() time.Duration {
	if p.timeoutReader != nil {
		if t := p.timeoutReader.StreamStallTimeout(); t > 0 {
			return t
		}
	}
	return p.streamStallTimeout
}

// Options carries per-request routing and token-saving settings resolved from
// the API key, request, and global config.
type Options struct {
	// Targets is the ordered fallback chain (provider+model candidates).
	Targets []dispatch.Target
	// PlanOpts carries strategy metadata (round-robin, chain ID, sticky limit).
	PlanOpts dispatch.PlanOptions
	// Slimmer / Terse / Caveman control token-saving transforms. Slimmer (RTK)
	// compresses bulky tool outputs on the input side; Terse and Caveman inject
	// system-prompt directives that reduce output tokens.
	Slimmer slimmer.Config
	Headroom headroom.Config
	Terse   terse.Config
	Caveman caveman.Config
	// Limits carries already-resolved per-key rate limits. Zero values mean unlimited.
	Limits limits.EffectiveLimits
}

// Result reports the outcome of a unary request for metering and audit.
type Result struct {
	Response   *core.ChatResponse
	Provider   string
	Model      string
	AccountID  string
	CostMicros int64
	Latency    time.Duration
	SlimStats  *slimmer.Stats
	HeadroomStats *headroom.Stats
	CacheHit   bool
}

// Chat runs a non-streaming request through the full pipeline with fallback.
func (p *Pipeline) Chat(ctx context.Context, req *core.ChatRequest, opts Options) (*Result, error) {
	if err := p.preflight(ctx, req, opts); err != nil {
		p.log.Debug("preflight rejected", "err", err)
		return nil, err
	}
	release, err := p.acquireLimit(ctx, req, opts)
	if err != nil {
		scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
		p.budgetRelease(scope)
		return nil, err
	}
	defer release(0)

	// Guardrails inbound: PII masking, prompt-injection block, etc. Runs
	// before any token-saving transform so the masked text is what the
	// slimmer sees. A Block decision returns ErrPolicyBlocked, which is
	// non-fallbackable — different providers won't change a safety decision.
	if gres := p.guardrails.Inbound(ctx, req); gres.Action == guardrails.ActionBlock {
		p.log.Debug("guardrails blocked inbound", "reason", gres.Reason)
		scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
		p.budgetRelease(scope)
		return nil, &core.ProviderError{Kind: core.ErrPolicyBlocked, Message: gres.Reason}
	}

	// Semantic cache lookup before any token-saving transform mutates the
	// request, so cache keys are stable across slimmer/terse settings. Cached
	// hits cost nothing and skip the upstream entirely.
	vec := p.embedPrompt(ctx, req)
	if hit, ok := p.cacheLookup(ctx, vec); ok {
		p.log.Debug("cache hit, skipping upstream")
		// Release budget reservation — cache hits cost nothing.
		scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
		p.budgetRelease(scope)
		// Record cache hit for usage visibility.
		if p.meter != nil {
			p.meter.Record(ctx, meter.Event{
				TenantID:  req.Metadata.TenantID,
				ProjectID: req.Metadata.ProjectID,
				APIKeyID:  req.Metadata.APIKeyID,
				Provider:  "cache",
				Model:     hit.Model,
				CacheHit:  true,
			})
		}
		if p.metrics != nil {
			p.metrics.RecordCache(true)
		}
		return &Result{Response: hit, Provider: "cache", Model: hit.Model, CacheHit: true}, nil
	}
	if p.metrics != nil && p.cache != nil && p.cache.Enabled() {
		p.metrics.RecordCache(false)
	}

	slimStats, headStats := p.applyTokenSaving(ctx, req, opts)
	if slimStats != nil {
		p.log.Debug("slimmer stats", "saved", slimStats.Saved(), "hits", len(slimStats.Hits))
	}
	if headStats != nil {
		p.log.Debug("headroom stats", "saved_tokens", headStats.TokensSaved, "transforms", strings.Join(headStats.Transforms, ","))
	}
	save := buildSaveState(slimStats, headStats, opts)

	required := capability.Required(req)
	attempts, err := p.planWithCooldownRetry(ctx, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts)
	if err != nil {
		p.log.Debug("dispatcher plan failed", "err", err)
		return nil, err
	}
	p.log.Debug("dispatcher planned", "attempts", len(attempts), "required", required)

	scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
	var lastErr error
	for i, attempt := range attempts {
		started := time.Now()
		p.log.Debug("attempt start", "i", i, "provider", attempt.Target.Provider,
			"model", attempt.Target.Model, "account", attempt.Account.ID)
		attemptReq := cloneForAttempt(req, attempt.Target.Model)

		// Inject proxy config from credentials into context so the connector's
		// HTTP client uses the right proxy/relay for this account.
		callCtx := core.WithProxy(ctx, attempt.Creds)
		var cancelTimeout context.CancelFunc
		if reqTimeout := p.resolvedRequestTimeout(); reqTimeout > 0 {
			callCtx, cancelTimeout = context.WithTimeout(callCtx, reqTimeout)
		}
		resp, callErr := attempt.Conn.Chat(callCtx, attemptReq, attempt.Creds)
		if cancelTimeout != nil {
			cancelTimeout() // cancel immediately to avoid timer goroutine leak
		}
		latency := time.Since(started)

		if callErr != nil {
			pe := core.AsProviderError(callErr)
			lastErr = pe
			p.dispatcher.NoteFailure(ctx, attempt.Account.ID, pe)
			if p.metrics != nil {
				p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
			}
			if !pe.Fallbackable() {
				p.log.Debug("attempt not fallbackable, aborting", "kind", pe.Kind)
				p.budgetRelease(scope)
				return nil, pe
			}
			if p.metrics != nil {
				p.metrics.RecordFallback(string(pe.Kind))
			}
			p.log.Warn("chat attempt failed, falling back",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model, "kind", pe.Kind)
			continue
		}

		// Reset backoff and clear model cooldown on success.
		p.dispatcher.NoteSuccess(ctx, attempt.Account.ID, attempt.Target.Model)

		// Guardrails outbound: PII scan / response masking. Runs after the
		// upstream succeeds but before we record usage so a Block decision
		// still costs the user but never returns the leaked content.
		if gres := p.guardrails.Outbound(ctx, req, resp); gres.Action == guardrails.ActionBlock {
			p.log.Debug("guardrails blocked outbound", "reason", gres.Reason)
			cost := p.record(ctx, req.Metadata, attempt, resp.Usage, false, latency, save)
			p.budgetConfirm(scope, cost)
			return nil, &core.ProviderError{Kind: core.ErrPolicyBlocked, Message: gres.Reason}
		}

		cost := p.record(ctx, req.Metadata, attempt, resp.Usage, false, latency, save)
		p.cacheStore(ctx, vec, resp)
		// Confirm budget reservation — usage is now recorded in DB.
		p.budgetConfirm(scope, cost)
		p.log.Debug("attempt success", "provider", attempt.Target.Provider,
			"model", attempt.Target.Model, "latency_ms", latency.Milliseconds(),
			"prompt_tok", resp.Usage.PromptTokens, "completion_tok", resp.Usage.CompletionTokens)
		return &Result{
			Response:   resp,
			Provider:   attempt.Target.Provider,
			Model:      attempt.Target.Model,
			AccountID:  attempt.Account.ID,
			CostMicros: cost,
			Latency:    latency,
			SlimStats:  slimStats,
			HeadroomStats: headStats,
		}, nil
	}

	if lastErr == nil {
		lastErr = &core.ProviderError{Kind: core.ErrInternal, Message: "pipeline: no attempts executed"}
	}
	p.budgetRelease(scope)
	return nil, lastErr
}

// StreamResult is delivered before chunks start flowing, identifying the chosen
// target so the gateway can set response headers.
type StreamResult struct {
	Chunks    <-chan core.StreamChunk
	Provider  string
	Model     string
	AccountID string

	// DirectBody is set when the pipeline detected a same-dialect, no-tools
	// scenario and obtained a raw io.ReadCloser from the upstream. The gateway
	// should io.Copy this directly to the response writer for zero-copy
	// streaming. When set, Chunks is nil — the two paths are mutually exclusive.
	DirectBody io.ReadCloser

	// DirectUsageFunc, when non-nil, must be called after the DirectBody stream
	// is fully consumed (after io.Copy completes). It parses the captured SSE
	// data for usage tokens and records them in the meter.
	DirectUsageFunc func()
}

// maxRateLimitRetries is how many times the pipeline will re-plan and retry
// after a rate-limit (429) error, waiting for the per-account cooldown to
// expire between attempts. With a single account this avoids giving up
// immediately on a transient 429.
const maxRateLimitRetries = 3

// Stream runs a streaming request with fallback. Fallback applies only to the
// connection-establishment phase; once the first attempt's channel is returned,
// errors surface as ChunkError on that channel. Usage metering happens in a
// goroutine that observes the final usage chunk.
//
// When all attempts fail with a rate-limit (429) and the error is fallbackable,
// the pipeline re-plans after a short wait (letting the account cooldown
// expire) and retries up to maxRateLimitRetries times. This prevents transient
// 429s from being fatal when only a single account is configured.
func (p *Pipeline) Stream(ctx context.Context, req *core.ChatRequest, opts Options) (*StreamResult, error) {
	if err := p.preflight(ctx, req, opts); err != nil {
		p.log.Debug("stream preflight rejected", "err", err)
		return nil, err
	}
	release, err := p.acquireLimit(ctx, req, opts)
	if err != nil {
		scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
		p.budgetRelease(scope)
		return nil, err
	}

	// Guardrails inbound on streaming requests. Same semantics as Chat().
	// Outbound scanning of streamed chunks is deferred — per-chunk scanning
	// could fragment matches across boundaries; full-stream buffering would
	// undermine the latency advantage of streaming.
	if gres := p.guardrails.Inbound(ctx, req); gres.Action == guardrails.ActionBlock {
		p.log.Debug("guardrails blocked inbound stream", "reason", gres.Reason)
		release(0)
		return nil, &core.ProviderError{Kind: core.ErrPolicyBlocked, Message: gres.Reason}
	}

	slimStats, headStats := p.applyTokenSaving(ctx, req, opts)
	save := buildSaveState(slimStats, headStats, opts)

	required := capability.Required(req)
	scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
	attempts, err := p.planWithCooldownRetry(ctx, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts)
	if err != nil {
		p.log.Debug("stream dispatcher plan failed", "err", err)
		release(0)
		p.budgetRelease(scope)
		return nil, err
	}
	p.log.Debug("stream dispatcher planned", "attempts", len(attempts))

	// Outer rate-limit retry loop: re-plan and retry when all attempts hit 429.
	rlRetries := 0
	for {
		sr, sErr, budgetReleased := p.streamExec(ctx, req, opts, attempts, slimStats, save, required, scope, release)
		if sErr == nil {
			return sr, nil
		}

		// Only retry on rate-limit errors that are still fallbackable.
		pe := core.AsProviderError(sErr)
		if pe.Kind != core.ErrRateLimit || !pe.Fallbackable() || rlRetries >= maxRateLimitRetries {
			release(0)
			if !budgetReleased {
				p.budgetRelease(scope)
			}
			return nil, sErr
		}
		rlRetries++
		// Wait for the cooldown to expire before re-planning. The dispatcher
		// set TransientCooldown (2s-30s) via NoteFailure; give it a moment.
		wait := time.Duration(rlRetries) * 2 * time.Second
		p.log.Warn("all stream attempts hit rate-limit, waiting to retry",
			"wait", wait, "retry", rlRetries, "of", maxRateLimitRetries)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			release(0)
			if !budgetReleased {
				p.budgetRelease(scope)
			}
			return nil, ctx.Err()
		}
		// Re-reserve budget if it was released by streamExec (not-fallbackable path).
		if budgetReleased {
			if rerr := p.budget.Reserve(ctx, scope, 0); rerr != nil {
				return nil, rerr
			}
		}
		attempts, err = p.planWithCooldownRetry(ctx, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts)
		if err != nil {
			p.log.Debug("stream re-plan after rate-limit failed", "err", err)
			p.budgetRelease(scope)
			return nil, sErr
		}
		p.log.Debug("stream re-planned after rate-limit", "attempts", len(attempts))
	} // end outer rate-limit retry loop
}

// streamExec executes one round of attempts (the original Stream logic).
// Returns (result, nil, false) on success, (nil, err, false) for most errors
// where budget is NOT released yet, or (nil, err, true) when budget was
// released inside (non-fallbackable errors).
func (p *Pipeline) streamExec(ctx context.Context, req *core.ChatRequest, opts Options,
	attempts []dispatch.Attempt, slimStats *slimmer.Stats, save *saveState,
	required core.CapabilitySet, scope budget.Scope, release limits.ReleaseFunc) (*StreamResult, error, bool) {

	var lastErr error
	for i, attempt := range attempts {
		attemptReq := cloneForAttempt(req, attempt.Target.Model)
		started := time.Now()
		p.log.Debug("stream attempt start", "i", i, "provider", attempt.Target.Provider,
			"model", attempt.Target.Model, "account", attempt.Account.ID)

		// Capture TTFT from the connector's first-chunk callback.
		// StartedAt is set to the attempt start so connectors measure TTFT
		// from the HTTP call initiation (includes connection + header wait time).
		var ttft time.Duration
		streamCfg := core.StreamConfig{
			StartedAt: started,
			OnFirstChunk: func(elapsed time.Duration) {
				ttft = elapsed
			},
		}

		callCtx := core.WithProxy(ctx, attempt.Creds)

		// Zero-copy fast path: when the client dialect matches the upstream
		// dialect, no tools are present (so no argument sanitization needed),
		// and the connector implements DirectStreamable, we can pipe the raw
		// SSE bytes directly to the client — bypassing all JSON parse/serialize
		// overhead. This is the highest-throughput path for same-dialect proxying.
		if len(attemptReq.Tools) == 0 && req.Metadata.SourceDialect == attempt.Conn.Dialect() {
			if ds, ok := attempt.Conn.(core.DirectStreamable); ok {
				body, _, rawErr := ds.StreamRaw(callCtx, attemptReq, attempt.Creds, streamCfg)
				if rawErr != nil {
					pe := core.AsProviderError(rawErr)
					lastErr = pe
					p.dispatcher.NoteFailure(ctx, attempt.Account.ID, pe)
					if p.metrics != nil {
						p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
					}
					if !pe.Fallbackable() {
						release(0)
						p.budgetRelease(scope)
						return nil, pe, true
					}
					if p.metrics != nil {
						p.metrics.RecordFallback(string(pe.Kind))
					}
					p.log.Warn("direct stream attempt failed, falling back",
						"provider", attempt.Target.Provider, "model", attempt.Target.Model, "kind", pe.Kind)
					continue
				}
				p.log.Debug("direct stream connected (zero-copy)", "provider", attempt.Target.Provider,
					"model", attempt.Target.Model)
				p.dispatcher.NoteSuccess(ctx, attempt.Account.ID, attempt.Target.Model)
				// Wrap the raw body in a tee reader that captures all bytes
				// into a buffer and detects the first byte for TTFT measurement.
				// After io.Copy completes in the gateway, the DirectUsageFunc
				// callback parses the captured SSE data for usage tokens and
				// records them in the meter.
				var capture safeBuffer
				firstByte := &ttftByteDetector{
					started: started,
					onFirst: streamCfg.OnFirstChunk,
				}
				wrapped := &teeReadCloser{r: body, w: io.MultiWriter(&capture, firstByte)}
				meta := req.Metadata
				acc := attempt
				saveCopy := save
				budgetScope := scope
				usageFunc := func() {
					defer release(0)
					usage := extractUsageFromStream(capture.Bytes())
					totalLatency := time.Since(started)
					cost := p.recordWithTTFT(ctx, meta, acc, usage, false, totalLatency, ttft, saveCopy)
					p.budgetConfirm(budgetScope, cost)
				}
				return &StreamResult{
					DirectBody:      wrapped,
					Provider:        attempt.Target.Provider,
					Model:           attempt.Target.Model,
					AccountID:       attempt.Account.ID,
					DirectUsageFunc: usageFunc,
				}, nil, false
			}
		}

		// Standard channel path: parse upstream SSE into canonical chunks,
		// then render back to the client's dialect.
		upstream, callErr := attempt.Conn.Stream(callCtx, attemptReq, attempt.Creds, streamCfg)
		if callErr != nil {
			pe := core.AsProviderError(callErr)
			lastErr = pe
			p.dispatcher.NoteFailure(ctx, attempt.Account.ID, pe)
			if p.metrics != nil {
				p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
			}
			if !pe.Fallbackable() {
				p.log.Debug("stream attempt not fallbackable, aborting", "kind", pe.Kind)
				release(0)
				p.budgetRelease(scope)
				return nil, pe, true
			}
			if p.metrics != nil {
				p.metrics.RecordFallback(string(pe.Kind))
			}
			p.log.Warn("stream attempt failed, falling back",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model, "kind", pe.Kind)
			continue
		}

		p.log.Debug("stream connected", "provider", attempt.Target.Provider,
			"model", attempt.Target.Model, "account", attempt.Account.ID)
		p.dispatcher.NoteSuccess(ctx, attempt.Account.ID, attempt.Target.Model)

		// Tee the upstream channel so we can meter terminal usage without
		// blocking the client consumer.
		out := make(chan core.StreamChunk, 16)
		meta := req.Metadata
		acc := attempt
		go p.pumpStream(ctx, req, upstream, out, meta, acc, started, &ttft, save, scope, release)

		return &StreamResult{
			Chunks:    out,
			Provider:  attempt.Target.Provider,
			Model:     attempt.Target.Model,
			AccountID: attempt.Account.ID,
		}, nil, false
	}

	if lastErr == nil {
		lastErr = &core.ProviderError{Kind: core.ErrInternal, Message: "pipeline: no attempts executed"}
	}
	release(0)
	p.budgetRelease(scope)
	return nil, lastErr, true
}

// pumpStream forwards chunks to the client channel while capturing usage for
// metering when the stream completes. Usage chunks are merged rather than
// replaced: Anthropic streams input tokens in message_start and output tokens
// in message_delta as separate events — replacing would lose the first.
//
// When StreamStallTimeout is configured, a timer resets on every chunk. If no
// chunk arrives within the timeout, the stream is cancelled with ErrTimeout.
//
// pumpStream also runs Outbound guardrails on streaming text via a sliding
// window: every chunkScanWindow characters, the accumulated tail is handed
// to the engine. On a Mask decision the rewritten text replaces the buffered
// tail before forwarding; on a Block decision the stream is cancelled and an
// error chunk is delivered to the client.
func (p *Pipeline) pumpStream(ctx context.Context, req *core.ChatRequest, in <-chan core.StreamChunk, out chan<- core.StreamChunk,
	meta core.RequestMetadata, attempt dispatch.Attempt, started time.Time, ttft *time.Duration, save *saveState, scope budget.Scope, release limits.ReleaseFunc) {
	defer close(out)
	defer release(0)

	// Resolve effective stall timeout (dynamic from dashboard or static config).
	stallTimeout := p.resolvedStallTimeout()

	// Set up stall detection. The timer resets every time a chunk arrives.
	var stallCancel context.CancelFunc
	stallCtx := ctx
	if stallTimeout > 0 {
		stallCtx, stallCancel = context.WithCancel(ctx)
		defer stallCancel()
		// Drain upstream when stall fires.
		go func() {
			<-stallCtx.Done()
			if ctx.Err() == nil && stallCtx.Err() != nil {
				// Stall detected (parent ctx still alive but stall ctx cancelled).
				// Consume remaining upstream chunks to unblock the producer.
				for range in {
				}
			}
		}()
	}

	var stallTimer *time.Timer
	resetStall := func() {
		if stallTimeout <= 0 {
			return
		}
		if stallTimer == nil {
			stallTimer = time.AfterFunc(stallTimeout, stallCancel)
		} else {
			stallTimer.Reset(stallTimeout)
		}
	}
	stopStall := func() {
		if stallTimer != nil {
			stallTimer.Stop()
		}
	}
	defer stopStall()

	resetStall() // arm the timer for the initial connection

	// Sliding window for outbound guardrails. We buffer the tail of the
	// assistant's text output and ask the engine to scan it every
	// chunkScanWindow characters. The window is small (256 chars) so the
	// added per-chunk regex work is microseconds, but large enough that
	// matches that span chunk boundaries (PII split across 2 frames) still
	// fire. ChunkText.Delta is the only chunk type we feed the scanner;
	// thinking, tool calls, and usage events are passed through untouched.
	const chunkScanWindow = 256
	var streamBuf strings.Builder

	var usage core.Usage
	for {
		select {
		case chunk, ok := <-in:
			if !ok {
				// Upstream closed — stream complete.
				// Record actual total upstream duration as latency; TTFT is
				// stored separately in ttft_ms by recordWithTTFT.
				totalLatency := time.Since(started)
				cost := p.recordWithTTFT(ctx, meta, attempt, usage, false, totalLatency, *ttft, save)
				p.budgetConfirm(scope, cost)
				return
			}
			resetStall()
			if chunk.Type == core.ChunkUsage && chunk.Usage != nil {
				usage = mergeUsage(usage, *chunk.Usage)
			}

			// Outbound guardrail scan for streamed text. We accumulate every
			// text delta and rescan the tail at each window boundary. A Block
			// short-circuits: we cancel the stream and surface an error chunk
			// so the client sees a policy refusal mid-stream.
			if chunk.Type == core.ChunkText && chunk.Delta != "" && p.guardrails != nil {
				streamBuf.WriteString(chunk.Delta)
				if streamBuf.Len() >= chunkScanWindow {
					gres := p.guardrails.OutboundChunk(ctx, req, streamBuf.String())
					if gres.Action == guardrails.ActionBlock {
						p.log.Debug("guardrails blocked streaming output", "reason", gres.Reason)
						out <- core.StreamChunk{
							Type: core.ChunkError,
							Err: &core.ProviderError{
								Kind:     core.ErrPolicyBlocked,
								Provider: attempt.Target.Provider,
								Model:    attempt.Target.Model,
								Message:  gres.Reason,
							},
						}
						for range in {
						}
						totalLatency := time.Since(started)
						cost := p.recordWithTTFT(ctx, meta, attempt, usage, false, totalLatency, *ttft, save)
						p.budgetConfirm(scope, cost)
						return
					}
					// Reset the buffer once it has been scanned; we only need
					// rolling lookback within one window, not the whole stream.
					streamBuf.Reset()
				}
			}

			select {
			case out <- chunk:
			case <-stallCtx.Done():
				// Client disconnected or stall — drain upstream.
				for range in {
				}
				return
			}
		case <-stallCtx.Done():
			if ctx.Err() != nil {
				// Parent context cancelled (client disconnected).
				p.budgetRelease(scope)
				for range in {
				}
				return
			}
			// Stall timeout fired.
			stopStall()
			out <- core.StreamChunk{
				Type: core.ChunkError,
				Err:  &core.ProviderError{Kind: core.ErrTimeout, Provider: attempt.Target.Provider, Model: attempt.Target.Model, Message: "stream stall: no data received for " + stallTimeout.String()},
			}
			// Drain upstream.
			for range in {
			}
			totalLatency := time.Since(started)
			cost := p.recordWithTTFT(ctx, meta, attempt, usage, false, totalLatency, *ttft, save)
			p.budgetConfirm(scope, cost)
			return
		}
	}
}

// mergeUsage combines two usage snapshots. Fields present in the new snapshot
// (non-zero) overwrite the old; zero fields preserve the old value. This
// handles providers that split usage across multiple events (e.g. Anthropic
// sends input tokens in message_start and output tokens in message_delta).
func mergeUsage(old, new core.Usage) core.Usage {
	if new.PromptTokens != 0 {
		old.PromptTokens = new.PromptTokens
	}
	if new.CompletionTokens != 0 {
		old.CompletionTokens = new.CompletionTokens
	}
	if new.TotalTokens != 0 {
		old.TotalTokens = new.TotalTokens
	}
	if new.CachedTokens != 0 {
		old.CachedTokens = new.CachedTokens
	}
	if new.CacheWriteTokens != 0 {
		old.CacheWriteTokens = new.CacheWriteTokens
	}
	if new.ReasoningTokens != 0 {
		old.ReasoningTokens = new.ReasoningTokens
	}
	return old
}

// preflight runs validation and the budget guard before any upstream call.
// It uses Reserve() instead of Check() to prevent the TOCTOU race where
// concurrent requests all pass the budget check before any usage is recorded.
func (p *Pipeline) acquireLimit(ctx context.Context, req *core.ChatRequest, opts Options) (limits.ReleaseFunc, error) {
	if p.limiter == nil {
		return func(int64) {}, nil
	}
	provider, model := firstTarget(opts.Targets)
	release, decision, err := p.limiter.Acquire(ctx, limits.Request{
		TenantID:        req.Metadata.TenantID,
		ProjectID:       req.Metadata.ProjectID,
		APIKeyID:        req.Metadata.APIKeyID,
		Provider:        provider,
		Model:           model,
		EstimatedTokens: estimateChatTokens(req),
		Limits:          opts.Limits,
	})
	if err != nil {
		return nil, err
	}
	if !decision.Allowed {
		return nil, rateLimitError(decision)
	}
	return release, nil
}

func firstTarget(targets []dispatch.Target) (string, string) {
	if len(targets) == 0 {
		return "", ""
	}
	return targets[0].Provider, targets[0].Model
}

func estimateChatTokens(req *core.ChatRequest) int64 {
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
	tokens := int64((chars + 3) / 4)
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		tokens += int64(*req.MaxTokens)
	}
	return tokens
}

func rateLimitError(decision limits.Decision) *core.ProviderError {
	return &core.ProviderError{
		Kind:       core.ErrRateLimit,
		Message:    decision.Reason,
		RetryAfter: decision.RetryAfter,
	}
}

func (p *Pipeline) preflight(ctx context.Context, req *core.ChatRequest, opts Options) error {
	if len(opts.Targets) == 0 {
		return &core.ProviderError{Kind: core.ErrBadRequest, Message: "no routing targets resolved for model"}
	}
	if p.budget != nil {
		scope := budget.Scope{
			TenantID:  req.Metadata.TenantID,
			ProjectID: req.Metadata.ProjectID,
			APIKeyID:  req.Metadata.APIKeyID,
		}
		if err := p.budget.Reserve(ctx, scope, 0); err != nil {
			return err
		}
	}
	return nil
}

// budgetConfirm releases the reservation after successful metering.
func (p *Pipeline) budgetConfirm(scope budget.Scope, costMicros int64) {
	if p.budget != nil {
		p.budget.Confirm(scope, costMicros)
	}
}

// budgetRelease removes the reservation when a request fails before metering.
func (p *Pipeline) budgetRelease(scope budget.Scope) {
	if p.budget != nil {
		p.budget.Release(scope)
	}
}

// planWithCooldownRetry wraps dispatcher planning with a brief wait-and-retry
// when all accounts are on cooldown. Cooldown is a transient state (typically
// 2-30s), so a short wait yields usable accounts instead of an instant failure.
// Retries up to 3 times, sleeping up to CooldownRetryMax total.
func (p *Pipeline) planWithCooldownRetry(ctx context.Context, tenantID string, targets []dispatch.Target, required core.CapabilitySet, opts dispatch.PlanOptions) ([]dispatch.Attempt, error) {
	attempts, err := p.dispatcher.PlanWith(ctx, tenantID, targets, required, opts)
	if err == nil && len(attempts) > 0 {
		return attempts, nil
	}
	// Only retry when the error is a cooldown block (all accounts on cooldown).
	if err == nil || !strings.Contains(err.Error(), "on cooldown") {
		return attempts, err
	}

	deadline := time.Now().Add(CooldownRetryMax)
	for i := 0; i < 3; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			break
		}
		wait := time.Duration(i+1) * 2 * time.Second
		if wait > time.Until(deadline) {
			wait = time.Until(deadline)
		}
		if wait <= 0 {
			break
		}
		p.log.Debug("all accounts on cooldown, waiting before retry", "wait", wait, "attempt", i+1)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		attempts, err = p.dispatcher.PlanWith(ctx, tenantID, targets, required, opts)
		if err == nil && len(attempts) > 0 {
			return attempts, nil
		}
		if err == nil || !strings.Contains(err.Error(), "on cooldown") {
			return attempts, err
		}
	}
	return attempts, err
}

// applyTokenSaving runs the input-side (slimmer/RTK) and output-side
// (terse, caveman) token-saving transforms in place. Terse and caveman both
// inject system-prompt directives; if both are enabled, terse runs first and
// caveman appends after, but in practice only one output-saver is used.
func (p *Pipeline) applyTokenSaving(ctx context.Context, req *core.ChatRequest, opts Options) (*slimmer.Stats, *headroom.Stats) {
	// Normalize tool call IDs and fix missing tool results before any
	// downstream processing. This ensures Anthropic-compatible IDs and
	// complete tool_use/tool_result pairs.
	normalizer.Apply(req)

	var stats *slimmer.Stats
	var hstats *headroom.Stats
	if p.headroom != nil && opts.Headroom.Enabled {
		var err error
		hstats, err = p.headroom.Compress(ctx, req, opts.Headroom)
		if err != nil {
			p.log.Warn("headroom compression failed; continuing uncompressed", "err", err)
		} else if hstats != nil {
			p.log.Debug("headroom compressed request", "saved_tokens", hstats.TokensSaved, "transforms", strings.Join(hstats.Transforms, ","))
		}
	} else if p.slimmer != nil && opts.Slimmer.Enabled {
		stats = p.slimmer.Compress(req, opts.Slimmer)
		if stats != nil {
			p.log.Debug("slimmer compressed request", "saved_bytes", stats.Saved(), "hits", len(stats.Hits))
		}
	}
	if !opts.Headroom.OutputShaper {
		terse.Apply(req, opts.Terse)
		caveman.Apply(req, opts.Caveman)
	}
	return stats, hstats
}

// saveState captures which token-saving features were active and their results
// for a single request, so the meter can persist them.
type saveState struct {
	slimSnap *meter.SlimSnapshot
	headroom *meter.HeadroomSnapshot
	caveman  bool
	terse    bool
}

// splitRuleNames splits a comma-separated rule string into individual names.
func splitRuleNames(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if part := s[start:i]; part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

// buildSaveState converts pipeline-level token-saving results into a snapshot
// suitable for the meter. Returns nil when no features were active.
func buildSaveState(stats *slimmer.Stats, hstats *headroom.Stats, opts Options) *saveState {
	save := &saveState{
		caveman: opts.Caveman.Enabled && !opts.Headroom.OutputShaper,
		terse:   opts.Terse.Enabled && !opts.Headroom.OutputShaper,
	}
	if hstats != nil {
		save.headroom = &meter.HeadroomSnapshot{
			TokensBefore:     hstats.TokensBefore,
			TokensAfter:      hstats.TokensAfter,
			TokensSaved:      hstats.TokensSaved,
			CompressionRatio: hstats.CompressionRatio,
			Transforms:       strings.Join(hstats.Transforms, ","),
			CCRHashes:        strings.Join(hstats.CCRHashes, ","),
		}
	}
	if stats != nil && stats.Saved() > 0 {
		// Build comma-separated rule names from hits.
		rules := make(map[string]struct{})
		for _, h := range stats.Hits {
			rules[h.Rule] = struct{}{}
		}
		var ruleNames string
		for name := range rules {
			if ruleNames != "" {
				ruleNames += ","
			}
			ruleNames += name
		}
		save.slimSnap = &meter.SlimSnapshot{
			BytesSaved:  stats.Saved(),
			TokensSaved: stats.Saved() / 4, // rough char-to-token estimate
			Rules:       ruleNames,
		}
	}
	if save.slimSnap == nil && save.headroom == nil && !save.caveman && !save.terse {
		return nil
	}
	return save
}

// record meters a completed attempt; failures to record are logged, not fatal.
func (p *Pipeline) record(ctx context.Context, meta core.RequestMetadata, attempt dispatch.Attempt,
	usage core.Usage, cacheHit bool, latency time.Duration, save *saveState) int64 {
	return p.recordWithTTFT(ctx, meta, attempt, usage, cacheHit, latency, 0, save)
}

// recordWithTTFT is like record but also records time-to-first-token.
func (p *Pipeline) recordWithTTFT(ctx context.Context, meta core.RequestMetadata, attempt dispatch.Attempt,
	usage core.Usage, cacheHit bool, latency, ttft time.Duration, save *saveState) int64 {
	if p.meter == nil {
		return 0
	}
	ev := meter.Event{
		TenantID:  meta.TenantID,
		ProjectID: meta.ProjectID,
		APIKeyID:  meta.APIKeyID,
		Provider:  attempt.Target.Provider,
		Model:     attempt.Target.Model,
		AccountID: attempt.Account.ID,
		Client:    meta.ClientKind,
		Usage:     usage,
		CacheHit:  cacheHit,
		Latency:   latency,
		TTFT:      ttft,
	}
	if save != nil {
		ev.SlimStats = save.slimSnap
		ev.HeadroomStats = save.headroom
		ev.CavemanActive = save.caveman
		ev.TerseActive = save.terse
	}
	cost, err := p.meter.Record(ctx, ev)
	if err != nil {
		p.log.Error("failed to record usage", "err", err)
	}

	if p.metrics != nil {
		p.metrics.RecordRequest(
			attempt.Target.Provider, attempt.Target.Model, "success",
			latency.Seconds(),
			usage.PromptTokens, usage.CompletionTokens, usage.CachedTokens, cost,
			ttft.Seconds(),
		)
		if cacheHit {
			p.metrics.RecordCache(true)
		}
		// Record token-saving metrics.
		if save != nil {
			if save.slimSnap != nil {
				for _, rule := range splitRuleNames(save.slimSnap.Rules) {
					p.metrics.RecordSlimSavings(rule, save.slimSnap.BytesSaved)
				}
			}
			if save.headroom != nil {
				for _, transform := range splitRuleNames(save.headroom.Transforms) {
					p.metrics.RecordHeadroomSavings(transform, save.headroom.TokensSaved)
				}
				if save.headroom.Transforms == "" {
					p.metrics.RecordHeadroomSavings("unknown", save.headroom.TokensSaved)
				}
			}
			if save.caveman {
				p.metrics.RecordCavemanActivation()
			}
			if save.terse {
				p.metrics.RecordTerseActivation()
			}
		}
	}
	return cost
}

// embedPrompt computes a cache key vector for the request, or nil when caching
// is disabled or no embedder is configured.
func (p *Pipeline) embedPrompt(ctx context.Context, req *core.ChatRequest) []float32 {
	if p.cache == nil || !p.cache.Enabled() || p.embedder == nil {
		return nil
	}
	vec, err := p.embedder.Embed(ctx, cache.PromptText(req))
	if err != nil {
		p.log.Warn("cache embed failed; skipping cache", "err", err)
		return nil
	}
	return vec
}

// cacheLookup checks the semantic cache for a stored response.
func (p *Pipeline) cacheLookup(ctx context.Context, vec []float32) (*core.ChatResponse, bool) {
	if p.cache == nil || len(vec) == 0 {
		return nil, false
	}
	resp, ok, err := p.cache.Lookup(ctx, vec)
	if err != nil {
		p.log.Warn("cache lookup failed", "err", err)
		return nil, false
	}
	return resp, ok
}

// cacheStore caches a successful response under its prompt vector.
func (p *Pipeline) cacheStore(ctx context.Context, vec []float32, resp *core.ChatResponse) {
	if p.cache == nil || len(vec) == 0 {
		return
	}
	if err := p.cache.Store(ctx, vec, resp); err != nil {
		p.log.Warn("cache store failed", "err", err)
	}
}

// cloneForAttempt produces a shallow copy of the request with the candidate's
// model id, so each fallback attempt targets the right model without mutating
// the shared request.
func cloneForAttempt(req *core.ChatRequest, model string) *core.ChatRequest {
	clone := *req
	clone.Model = model
	return &clone
}

// ---- direct-body usage capture -----------------------------------------------

// ttftByteDetector is an io.Writer that fires its callback exactly once when
// the first byte is written. Used in the direct stream path to measure TTFT:
// when the tee reader copies the first upstream byte to this writer, the
// elapsed time since the HTTP call start is the time-to-first-token.
type ttftByteDetector struct {
	started time.Time
	onFirst func(time.Duration)
	once    sync.Once
}

func (d *ttftByteDetector) Write(p []byte) (int, error) {
	if len(p) > 0 {
		d.once.Do(func() {
			if d.onFirst != nil {
				d.onFirst(time.Since(d.started))
			}
		})
	}
	return len(p), nil
}

// safeBuffer is a thread-safe bytes.Buffer used as the tee target for direct
// body streams. It captures the head (first 4KB, for Anthropic's message_start
// which carries input_tokens) and the tail (last 256KB, for the final usage
// events). This bounds memory usage while capturing all usage data across
// both OpenAI and Anthropic SSE formats.
type safeBuffer struct {
	mu       sync.Mutex
	head     bytes.Buffer // first 4KB — captures message_start (Anthropic)
	tail     bytes.Buffer // rolling window of last 256KB
	total    int
	headDone bool // true once head is full
}

const headCaptureSize = 4 * 1024   // 4 KiB — enough for message_start
const tailCaptureSize = 256 * 1024 // 256 KiB — usage events are small

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.total += n

	// Capture the first 4KB into head (for Anthropic input_tokens).
	if !b.headDone {
		remain := headCaptureSize - b.head.Len()
		if remain > 0 {
			if n <= remain {
				b.head.Write(p)
			} else {
				b.head.Write(p[:remain])
			}
		}
		if b.head.Len() >= headCaptureSize {
			b.headDone = true
		}
	}

	// Always write to tail. Rather than trimming back to exactly tailCaptureSize
	// on every write past the limit (which copies up to 256KB per chunk for a
	// long stream — wasteful CPU on big coding-session responses), let the tail
	// grow to tailCaptureSize+tailTrimSlack before trimming. This amortizes the
	// trim to roughly once per 256KB of data while keeping the buffer bounded at
	// ~512KB. The final usage events live in the last few KB, so retaining a bit
	// more tail than strictly needed never loses them.
	b.tail.Write(p)
	if b.tail.Len() > tailCaptureSize+tailTrimSlack {
		old := b.tail.Bytes()
		tail := old[len(old)-tailCaptureSize:]
		b.tail.Reset()
		b.tail.Write(tail)
	}

	return n, nil
}

// tailTrimSlack lets the rolling tail overshoot tailCaptureSize before being
// trimmed, amortizing the trim copy. Bounds the tail at tailCaptureSize+slack.
const tailTrimSlack = tailCaptureSize

// Bytes returns the captured data: head first (for Anthropic message_start),
// then the tail (for final usage events). For small streams (<260KB) this is
// the entire stream; for large streams it's the first 4KB + the last portion of
// the stream (the trailing tailCaptureSize bytes, which always contain the
// final usage events).
func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total <= headCaptureSize+tailCaptureSize {
		// Small stream — head contains everything up to 4KB, tail has the rest.
		// Just return tail which has the full content for small streams.
		return b.tail.Bytes()
	}
	// Large stream — combine head + the trailing tailCaptureSize bytes. The tail
	// may carry up to tailTrimSlack extra bytes from amortized trimming; slice to
	// the last tailCaptureSize so the returned size stays predictable.
	tailBytes := b.tail.Bytes()
	if len(tailBytes) > tailCaptureSize {
		tailBytes = tailBytes[len(tailBytes)-tailCaptureSize:]
	}
	out := make([]byte, 0, b.head.Len()+len(tailBytes))
	out = append(out, b.head.Bytes()...)
	out = append(out, tailBytes...)
	return out
}

// teeReadCloser wraps an io.ReadCloser with an io.TeeReader so that every Read
// also writes the bytes to a secondary writer (the capture buffer). Close
// delegates to the original reader.
type teeReadCloser struct {
	r io.ReadCloser
	w io.Writer
}

func (t *teeReadCloser) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		if _, werr := t.w.Write(p[:n]); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (t *teeReadCloser) Close() error {
	return t.r.Close()
}

// sseUsageEnvelope is the minimal JSON structure needed to extract usage from
// both OpenAI and Anthropic SSE payloads. OpenAI puts usage at the top level;
// Anthropic splits it across message_start (input_tokens) and message_delta
// (output_tokens), with usage nested under "message" or at the top level.
type sseUsageEnvelope struct {
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		InputTokens      int `json:"input_tokens"`
		OutputTokens     int `json:"output_tokens"`
	} `json:"usage"`
	Message *struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// extractUsageFromSSEData tries to parse a single SSE data payload for usage
// tokens. Returns nil when the payload contains no usage information.
func extractUsageFromSSEData(data []byte) *core.Usage {
	if !bytes.Contains(data, []byte(`"usage"`)) {
		return nil
	}
	var env sseUsageEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil
	}
	u := &core.Usage{}
	// Top-level usage (OpenAI format, or Anthropic message_delta).
	if env.Usage != nil {
		u.PromptTokens = env.Usage.PromptTokens
		u.CompletionTokens = env.Usage.CompletionTokens
		u.TotalTokens = env.Usage.TotalTokens
		if env.Usage.InputTokens > 0 {
			u.PromptTokens = env.Usage.InputTokens
		}
		if env.Usage.OutputTokens > 0 {
			u.CompletionTokens = env.Usage.OutputTokens
		}
	}
	// Anthropic message_start wraps usage under "message".
	if env.Message != nil && env.Message.Usage != nil {
		if env.Message.Usage.InputTokens > 0 {
			u.PromptTokens = env.Message.Usage.InputTokens
		}
		if env.Message.Usage.OutputTokens > 0 {
			u.CompletionTokens = env.Message.Usage.OutputTokens
		}
	}
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 {
		return nil
	}
	return u
}

// extractUsageFromStream scans captured raw SSE bytes for usage data. It
// handles both OpenAI and Anthropic streaming formats, merging usage across
// multiple events (Anthropic splits input/output tokens across message_start
// and message_delta).
func extractUsageFromStream(raw []byte) core.Usage {
	var usage core.Usage
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" || payload == "" {
			continue
		}
		if u := extractUsageFromSSEData([]byte(payload)); u != nil {
			usage = mergeUsage(usage, *u)
		}
	}
	return usage
}
