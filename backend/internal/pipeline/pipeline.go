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
	"errors"
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
	"github.com/mydisha/keirouter/backend/internal/fastjson"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/headroom"
	"github.com/mydisha/keirouter/backend/internal/health"
	"github.com/mydisha/keirouter/backend/internal/limits"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/normalizer"
	"github.com/mydisha/keirouter/backend/internal/observ"
	"github.com/mydisha/keirouter/backend/internal/ponytail"
	"github.com/mydisha/keirouter/backend/internal/slimmer"
	"github.com/mydisha/keirouter/backend/internal/terse"
	"github.com/mydisha/keirouter/backend/internal/transform"
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
	headroom   *headroom.Compressor
	metrics    *observ.Metrics
	cache      *cache.Cache
	embedder   cache.Embedder
	guardrails *guardrails.Engine
	limiter    limits.Limiter
	log        *slog.Logger
	telemetry  *health.Service

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
	// Telemetry records provider health events. Best-effort, never blocks.
	Telemetry *health.Service
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
		headroom:   headroom.New(log),
		metrics:    d.Metrics,
		cache:      d.Cache,
		embedder:   d.Embedder,
		guardrails: d.Guardrails,
		limiter:    d.Limiter,
		log:        log,
		telemetry:  d.Telemetry,

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

// defaultResponseHeaderTimeout bounds time-to-first-byte on streaming calls
// when no dashboard value is configured.
const defaultResponseHeaderTimeout = 60 * time.Second

// resolvedHeaderTimeout returns the time-to-headers budget for streaming
// calls, preferring the dynamic dashboard value when available. Unary calls
// are bounded by the request timeout instead (their headers only arrive once
// generation finishes).
func (p *Pipeline) resolvedHeaderTimeout() time.Duration {
	if p.timeoutReader != nil {
		if t := p.timeoutReader.ResponseHeaderTimeout(); t > 0 {
			return t
		}
	}
	return defaultResponseHeaderTimeout
}

// streamCallContext derives the context a streaming connector call runs under:
// proxy credentials plus the time-to-headers budget. The returned cancel is
// owned by whichever party finishes the stream.
func (p *Pipeline) streamCallContext(ctx context.Context, creds core.Credentials) (context.Context, context.CancelFunc) {
	return context.WithCancel(core.WithResponseHeaderTimeout(core.WithProxy(ctx, creds), p.resolvedHeaderTimeout()))
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
	Terse   terse.Config
	Caveman caveman.Config
	// Headroom (input side) calls an external proxy to compress messages;
	// Ponytail (output side) injects a system-prompt block biasing toward
	// minimal output. Both run inside applyTokenSaving before format translation.
	Headroom headroom.Config
	Ponytail ponytail.Config
	// Limits carries already-resolved per-key rate limits. Zero values mean unlimited.
	Limits limits.EffectiveLimits
	// OnStreamPending, when set, is invoked once if a streaming attempt is
	// connected but has produced no output for streamPendingNotifyAfter. The
	// gateway uses it to commit response headers and start heartbeats so the
	// client connection survives long silent generations. After it fires,
	// any terminal error must be delivered in-band on the stream.
	OnStreamPending func()
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
	CacheHit   bool
}

// Chat runs a non-streaming request through the full pipeline with fallback.
func (p *Pipeline) Chat(ctx context.Context, req *core.ChatRequest, opts Options) (*Result, error) {
	requestStarted := time.Now()
	if err := p.preflight(ctx, req, opts); err != nil {
		p.log.Debug("preflight rejected", "err", err)
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
		return nil, err
	}
	release, err := p.acquireLimit(ctx, req, opts)
	if err != nil {
		scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
		p.budgetRelease(scope)
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
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
		err := &core.ProviderError{Kind: core.ErrPolicyBlocked, Message: gres.Reason}
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
		return nil, err
	}

	// Semantic cache lookup before any token-saving transform mutates the
	// request, so cache keys are stable across slimmer/terse settings. Cached
	// hits cost nothing and skip the upstream entirely.
	vec := p.embedPrompt(ctx, req)
	if hit, ok := p.cacheLookup(ctx, vec); ok {
		p.log.Debug("cache hit, skipping upstream")
		scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
		p.budgetRelease(scope)
		attempt := attemptForTargets(opts.Targets)
		if hit.Model != "" {
			attempt.Target.Model = hit.Model
		}
		usage := hit.Usage
		usage.Source = core.UsageSourceCache
		cost := p.recordOutcomeWithTTFT(ctx, req.Metadata, attempt, usage, "cache_hit", "", true,
			0, time.Since(requestStarted), 0, nil)
		return &Result{
			Response: hit, Provider: attempt.Target.Provider, Model: attempt.Target.Model,
			CostMicros: cost, CacheHit: true,
		}, nil
	}
	if p.metrics != nil && p.cache != nil && p.cache.Enabled() {
		p.metrics.RecordCache(false)
	}

	slimStats, hrStats := p.applyTokenSaving(ctx, req, opts)
	if slimStats != nil {
		p.log.Debug("slimmer stats", "saved", slimStats.Saved(), "hits", len(slimStats.Hits))
	}
	save := buildSaveState(slimStats, hrStats, opts)

	// HardRequired returns only non-strippable capabilities (tool calling,
	// streaming). Strippable modalities (vision, audio) are handled by the
	// pipeline's modality stripping, not the dispatch guard, so a request with
	// images is never hard-rejected just because a model profile lacks vision.
	required := capability.HardRequired(req)
	attempts, err := p.planWithCooldownRetry(ctx, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts)
	if err != nil {
		p.log.Debug("dispatcher plan failed", "err", err)
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
		return nil, err
	}
	p.log.Debug("dispatcher planned", "attempts", len(attempts), "required", required)

	scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
	var lastErr error
	var lastAttempt dispatch.Attempt
	var lastLatency time.Duration
	fellBack := false // tracks whether the current request triggered ≥1 fallback
	attemptBaseReq := req
	repairInstructionApplied := false
	var priorAttemptUsage core.Usage
	planner := newAttemptPlanner(p.dispatcher, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts, attempts)
	attempt, hasAttempt := planner.Current()
	rlRetries := 0
	waitBudget := CooldownRetryMax
	for i := 0; ; i++ {
		if !hasAttempt {
			// Every candidate was tried. When all of them were throttled with
			// a short reset window, wait once and re-plan instead of handing
			// the client a 429 (parity with the streaming path).
			wait, shouldRetry := streamRateLimitWait(core.AsProviderError(lastErr), rlRetries, waitBudget)
			if !shouldRetry {
				break
			}
			rlRetries++
			p.log.Warn("all chat attempts hit rate-limit, waiting to retry",
				"wait", wait, "retry", rlRetries, "of", maxRateLimitRetries)
			waitedAt := time.Now()
			if !sleepCtx(ctx, wait) {
				break
			}
			waitBudget -= time.Since(waitedAt)
			attempts, err = p.planWithCooldownRetry(ctx, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts)
			if err != nil {
				lastErr = err
				break
			}
			planner = newAttemptPlanner(p.dispatcher, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts, attempts)
			if attempt, hasAttempt = planner.Current(); !hasAttempt {
				break
			}
			fellBack = true
			req.Metadata.FallbackCount++
		}
		lastAttempt = attempt
		started := time.Now()
		p.log.Debug("attempt start", "i", i, "provider", attempt.Target.Provider,
			"model", attempt.Target.Model, "account", attempt.Account.ID)
		attemptReq := cloneForAttempt(attemptBaseReq, attempt.Target.Model)

		// Soft-degrade unsupported modalities. Stripping replaces input
		// modalities the resolved profile cannot handle with text placeholders,
		// so the upstream receives a valid request it can process. For built-in
		// providers this is normally a no-op (the dispatch guard already verifies
		// capabilities), but it provides a safety net when a profile is incomplete.
		// For custom/dynamic providers (where the guard is relaxed), stripping
		// is the primary downgrade mechanism.
		if capability.StripUnsupportedModalities(attemptReq, attempt.Target.Provider, attempt.Target.Model) {
			p.log.Debug("stripped unsupported modalities",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model)
		}

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
		if callErr != nil && isUnsupportedCloudflareImageError(callErr, attempt.Target.Provider, attempt.Target.Model, attemptReq) {
			p.log.Debug("retrying Cloudflare request without images", "model", attempt.Target.Model)
			retryReq := cloneForAttempt(attemptBaseReq, attempt.Target.Model)
			capability.StripImages(retryReq)
			retryCtx := core.WithProxy(ctx, attempt.Creds)
			var retryCancel context.CancelFunc
			if reqTimeout := p.resolvedRequestTimeout(); reqTimeout > 0 {
				retryCtx, retryCancel = context.WithTimeout(retryCtx, reqTimeout)
			}
			resp, callErr = attempt.Conn.Chat(retryCtx, retryReq, attempt.Creds)
			if retryCancel != nil {
				retryCancel()
			}
		}
		latency := time.Since(started)

		// Some upstream providers reject non-streaming requests with 400
		// "Stream must be set to true". ErrBadRequest is not fallbackable, so
		// the pipeline would normally abort the entire chain. Detect this
		// specific error and transparently retry with streaming, draining the
		// stream into a single ChatResponse. This handles all connectors
		// uniformly (OpenAI-compatible, Qwen, IFlow, etc.) without requiring
		// per-connector fixes.
		if callErr != nil && isStreamRequiredError(callErr) {
			p.log.Debug("provider requires streaming, retrying via Stream()",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model)
			streamCtx := core.WithProxy(ctx, attempt.Creds)
			var streamCancel context.CancelFunc
			if reqTimeout := p.resolvedRequestTimeout(); reqTimeout > 0 {
				streamCtx, streamCancel = context.WithTimeout(streamCtx, reqTimeout)
			}
			streamReq := cloneForAttempt(attemptBaseReq, attempt.Target.Model)
			stream, sErr := attempt.Conn.Stream(streamCtx, streamReq, attempt.Creds, core.StreamConfig{})
			if sErr == nil {
				sResp, drainErr := drainStream(stream, req.Model)
				if streamCancel != nil {
					streamCancel()
				}
				if drainErr == nil {
					callErr = nil
					resp = sResp
					latency = time.Since(started)
				} else {
					callErr = drainErr
				}
			} else {
				if streamCancel != nil {
					streamCancel()
				}
				callErr = sErr
			}
		}
		// Include the full transparent stream retry/drain in the upstream attempt
		// duration, including terminal stream-open and drain failures.
		latency = time.Since(started)
		lastLatency = latency

		if callErr != nil {
			pe := providerErrorForAttempt(callErr, attempt)
			lastErr = pe
			if pe.AttemptUsage != nil {
				priorAttemptUsage = addAttemptUsage(priorAttemptUsage, *pe.AttemptUsage)
			}
			repairRetry := pe.RetrySystemInstruction != "" && !repairInstructionApplied
			if repairRetry {
				attemptBaseReq = cloneWithSystemInstruction(req, pe.RetrySystemInstruction)
				repairInstructionApplied = true
			}
			p.dispatcher.NoteFailure(ctx, attempt.Account.ID, pe)
			if p.metrics != nil {
				p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
			}
			if !pe.Fallbackable() {
				p.recordFailureTelemetry(req.Metadata, attempt, pe, latency, false)
				p.log.Debug("attempt not fallbackable, aborting", "kind", pe.Kind)
				pe, cost := p.recordAttemptTerminal(ctx, req.Metadata, attempt, pe, priorAttemptUsage,
					latency, time.Since(requestStarted), 0, nil, fellBack)
				p.budgetConfirm(scope, cost)
				return nil, pe
			}
			var nextAttempt dispatch.Attempt
			var canFallback bool
			if repairRetry {
				nextAttempt, canFallback = planner.AfterRepair(ctx, attempt, pe)
			} else {
				nextAttempt, canFallback = planner.AfterFailure(ctx, attempt, pe)
			}
			p.recordFailureTelemetry(req.Metadata, attempt, pe, latency, canFallback)
			if !canFallback {
				hasAttempt = false
				continue
			}
			if p.metrics != nil {
				p.metrics.RecordFallback(string(pe.Kind))
			}
			fellBack = true
			req.Metadata.FallbackCount++
			p.log.Warn("chat attempt failed, falling back",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model, "kind", pe.Kind)
			attempt = nextAttempt
			continue
		}

		if priorAttemptUsage.PromptTokens != 0 || priorAttemptUsage.CompletionTokens != 0 ||
			priorAttemptUsage.TotalTokens != 0 {
			resp.Usage = addAttemptUsage(priorAttemptUsage, resp.Usage)
		}
		if restorer := newToolNameRestorer(req.Tools); restorer != nil {
			for i := range resp.Message.Content {
				if resp.Message.Content[i].Type == core.PartToolCall {
					restorer.restore(resp.Message.Content[i].ToolCall)
				}
			}
		}

		// Reset backoff and clear model cooldown on success.
		p.dispatcher.NoteSuccessAt(ctx, attempt.Target.Provider, attempt.Account.ID, attempt.Target.Model, started)

		// Guardrails outbound: PII scan / response masking. Runs after the
		// upstream succeeds but before we record usage so a Block decision
		// still costs the user but never returns the leaked content.
		if gres := p.guardrails.Outbound(ctx, req, resp); gres.Action == guardrails.ActionBlock {
			p.log.Debug("guardrails blocked outbound", "reason", gres.Reason)
			blockErr := &core.ProviderError{
				Kind: core.ErrPolicyBlocked, Provider: attempt.Target.Provider,
				Model: attempt.Target.Model, Message: gres.Reason,
			}
			cost := p.recordOutcomeWithTTFT(ctx, req.Metadata, attempt, resp.Usage,
				"blocked", string(blockErr.Kind), false, latency, time.Since(requestStarted), 0, save)
			// The policy blocked delivery, but the provider itself completed
			// successfully and should remain healthy.
			p.recordSuccessTelemetry(req.Metadata, attempt, resp.Usage, latency, 0, fellBack, cost)
			p.budgetConfirm(scope, cost)
			return nil, blockErr
		}

		cost := p.recordWithEndToEnd(ctx, req.Metadata, attempt, resp.Usage, false,
			latency, time.Since(requestStarted), save, fellBack)
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
		}, nil
	}

	if lastErr == nil {
		lastErr = &core.ProviderError{Kind: core.ErrInternal, Message: "pipeline: no attempts executed"}
	}
	if lastAttempt.Target.Provider == "" && lastAttempt.Target.Model == "" {
		lastAttempt = attemptForTargets(opts.Targets)
	}
	pe, cost := p.recordAttemptTerminal(ctx, req.Metadata, lastAttempt, lastErr, priorAttemptUsage,
		lastLatency, time.Since(requestStarted), 0, nil, fellBack)
	p.budgetConfirm(scope, cost)
	return nil, pe
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

	// DirectCompleteFunc, when non-nil, must be called exactly once after the
	// DirectBody stream terminates. A nil error records successful usage; a
	// provider/stall error updates cooldown and failure telemetry instead.
	DirectCompleteFunc func(error)
}

// maxRateLimitRetries bounds re-dispatch after every candidate was temporarily
// throttled. The retry-after and request deadline impose the tighter limit.
const maxRateLimitRetries = 3

const cooldownWaitMargin = 50 * time.Millisecond

func streamRateLimitWait(pe *core.ProviderError, retries int, budget time.Duration) (time.Duration, bool) {
	if pe == nil || pe.Kind != core.ErrRateLimit || !pe.Fallbackable() ||
		pe.Provider == "kiro" || retries >= maxRateLimitRetries || pe.RetryAfter <= 0 {
		return 0, false
	}
	wait := pe.RetryAfter + cooldownWaitMargin
	if wait > budget {
		return 0, false
	}
	return wait, true
}

// Stream runs a streaming request with fallback. Fallback applies only to the
// connection-establishment phase; once the first attempt's channel is returned,
// errors surface as ChunkError on that channel. Usage metering happens in a
// goroutine that observes the final usage chunk.
//
// When all attempts fail with a transient rate-limit (429) and the error is
// fallbackable, the pipeline may re-plan after a short wait. Explicit upstream
// reset times and account-wide Kiro limits are returned immediately.
func (p *Pipeline) Stream(ctx context.Context, req *core.ChatRequest, opts Options) (*StreamResult, error) {
	requestStarted := time.Now()
	if err := p.preflight(ctx, req, opts); err != nil {
		p.log.Debug("stream preflight rejected", "err", err)
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
		return nil, err
	}
	release, err := p.acquireLimit(ctx, req, opts)
	if err != nil {
		scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
		p.budgetRelease(scope)
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
		return nil, err
	}

	// Guardrails inbound on streaming requests. Same semantics as Chat().
	// Outbound scanning of streamed chunks is deferred — per-chunk scanning
	// could fragment matches across boundaries; full-stream buffering would
	// undermine the latency advantage of streaming.
	if gres := p.guardrails.Inbound(ctx, req); gres.Action == guardrails.ActionBlock {
		p.log.Debug("guardrails blocked inbound stream", "reason", gres.Reason)
		release(0)
		err := &core.ProviderError{Kind: core.ErrPolicyBlocked, Message: gres.Reason}
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
		return nil, err
	}

	slimStats, hrStats := p.applyTokenSaving(ctx, req, opts)
	save := buildSaveState(slimStats, hrStats, opts)

	// HardRequired: only non-strippable capabilities are enforced by the
	// dispatch guard. Strippable modalities (vision, audio) are soft-degraded
	// by modality stripping in the attempt loop below.
	required := capability.HardRequired(req)
	scope := budget.Scope{TenantID: req.Metadata.TenantID, ProjectID: req.Metadata.ProjectID, APIKeyID: req.Metadata.APIKeyID}
	attempts, err := p.planWithCooldownRetry(ctx, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts)
	if err != nil {
		p.log.Debug("stream dispatcher plan failed", "err", err)
		release(0)
		p.budgetRelease(scope)
		p.recordLocalTerminal(ctx, req.Metadata, opts.Targets, err, requestStarted)
		return nil, err
	}
	p.log.Debug("stream dispatcher planned", "attempts", len(attempts))

	// Outer rate-limit retry loop: re-plan only when the soonest cooldown fits
	// inside the bounded wait budget.
	rlRetries := 0
	waitBudget := CooldownRetryMax
	for {
		sr, sErr, budgetReleased, lastAttempt, lastLatency, fellBack := p.streamExec(
			ctx, req, opts, attempts, slimStats, save, required, scope, release, requestStarted)
		if sErr == nil {
			return sr, nil
		}

		pe := providerErrorForAttempt(sErr, lastAttempt)
		wait, shouldRetry := streamRateLimitWait(pe, rlRetries, waitBudget)
		if !shouldRetry {
			release(0)
			if !budgetReleased {
				p.budgetRelease(scope)
			}
			pe, cost := p.recordAttemptTerminal(ctx, req.Metadata, lastAttempt, pe, core.Usage{},
				lastLatency, time.Since(requestStarted), 0, nil, fellBack || rlRetries > 0)
			p.budgetConfirm(scope, cost)
			return nil, pe
		}
		rlRetries++
		p.log.Warn("all stream attempts hit rate-limit, waiting to retry",
			"wait", wait, "retry", rlRetries, "of", maxRateLimitRetries)
		waitedAt := time.Now()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			release(0)
			if !budgetReleased {
				p.budgetRelease(scope)
			}
			status, errorKind := terminalStatusAndKind(ctx.Err())
			cost := p.recordOutcomeWithTTFT(context.WithoutCancel(ctx), req.Metadata, lastAttempt,
				core.Usage{}, status, errorKind, false, lastLatency, time.Since(requestStarted), 0, nil)
			p.budgetConfirm(scope, cost)
			return nil, ctx.Err()
		}
		// Re-reserve budget if it was released by streamExec (not-fallbackable path).
		if budgetReleased {
			if rerr := p.budget.Reserve(ctx, scope, 0); rerr != nil {
				status, errorKind := terminalStatusAndKind(rerr)
				p.recordOutcomeWithTTFT(context.WithoutCancel(ctx), req.Metadata, lastAttempt, core.Usage{},
					status, errorKind, false, lastLatency, time.Since(requestStarted), 0, nil)
				return nil, rerr
			}
		}
		waitBudget -= time.Since(waitedAt)
		attempts, err = p.planWithCooldownRetry(ctx, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts)
		if err != nil {
			p.log.Debug("stream re-plan after rate-limit failed", "err", err)
			p.budgetRelease(scope)
			pe, cost := p.recordAttemptTerminal(ctx, req.Metadata, lastAttempt, sErr, core.Usage{},
				lastLatency, time.Since(requestStarted), 0, nil, true)
			p.budgetConfirm(scope, cost)
			return nil, pe
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
	required core.CapabilitySet, scope budget.Scope, release limits.ReleaseFunc,
	requestStarted time.Time) (*StreamResult, error, bool, dispatch.Attempt, time.Duration, bool) {

	var lastErr error
	var lastAttempt dispatch.Attempt
	var lastLatency time.Duration
	fellBack := false // tracks whether the current request triggered ≥1 fallback
	planner := newAttemptPlanner(p.dispatcher, req.Metadata.TenantID, opts.Targets, required, opts.PlanOpts, attempts)
	attempt, hasAttempt := planner.Current()
	for i := 0; hasAttempt; i++ {
		lastAttempt = attempt
		attemptReq := cloneForAttempt(req, attempt.Target.Model)
		started := time.Now()
		p.log.Debug("stream attempt start", "i", i, "provider", attempt.Target.Provider,
			"model", attempt.Target.Model, "account", attempt.Account.ID)

		// Soft-degrade unsupported modalities. Stripping replaces input
		// modalities the resolved profile cannot handle with text placeholders,
		// so the upstream receives a valid request it can process. For built-in
		// providers this is normally a no-op (the dispatch guard already verifies
		// capabilities), but it provides a safety net when a profile is incomplete.
		// For custom/dynamic providers (where the guard is relaxed), stripping
		// is the primary downgrade mechanism.
		if capability.StripUnsupportedModalities(attemptReq, attempt.Target.Provider, attempt.Target.Model) {
			p.log.Debug("stripped unsupported modalities",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model)
		}

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

		// The successful stream owns this cancellation function. pumpStream or
		// the direct response body cancels it on completion, client disconnect,
		// policy block, or stall so connector goroutines cannot leak.
		callCtx, cancelUpstream := p.streamCallContext(ctx, attempt.Creds)

		// Zero-copy fast path: when the client dialect matches the upstream
		// dialect, no tools are present (so no argument sanitization needed),
		// and the connector implements DirectStreamable, we can pipe the raw
		// SSE bytes directly to the client — bypassing all JSON parse/serialize
		// overhead. This is the highest-throughput path for same-dialect proxying.
		//
		// Tools force the channel path: fragmenting upstreams (Kiro, Cursor,
		// CommandCode, and some OpenAI-compatible providers) split tool-call
		// arguments across frames, and clients like Cline reject a tool call
		// whose arguments are not reassembled into one complete JSON object.
		// The channel path runs the ToolArgSanitizer, which buffers and
		// reassembles those fragments before rendering.
		//
		// include_usage also forces the channel path: the client asked for a
		// guaranteed usage event, which pumpStream synthesizes when the provider
		// omits one. Raw byte piping can't inject a synthetic event, so we trade
		// the zero-copy throughput for correct usage accounting on these opt-in
		// requests.
		if len(attemptReq.Tools) == 0 && !req.IncludeUsage && req.Metadata.SourceDialect == attempt.Conn.Dialect() &&
			!requiresParsedStreamAdmission(attempt.Target.Provider) {
			if ds, ok := attempt.Conn.(core.DirectStreamable); ok {
				body, _, rawErr := ds.StreamRaw(callCtx, attemptReq, attempt.Creds, streamCfg)
				if rawErr != nil && isUnsupportedCloudflareImageError(rawErr, attempt.Target.Provider, attempt.Target.Model, attemptReq) {
					cancelUpstream()
					retryReq := cloneForAttempt(req, attempt.Target.Model)
					capability.StripImages(retryReq)
					callCtx, cancelUpstream = p.streamCallContext(ctx, attempt.Creds)
					body, _, rawErr = ds.StreamRaw(callCtx, retryReq, attempt.Creds, streamCfg)
				}
				if rawErr != nil {
					cancelUpstream()
					pe := providerErrorForAttempt(rawErr, attempt)
					lastErr = pe
					p.dispatcher.NoteFailure(ctx, attempt.Account.ID, pe)
					if p.metrics != nil {
						p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
					}
					attemptLatency := time.Since(started)
					lastLatency = attemptLatency
					if !pe.Fallbackable() {
						p.recordFailureTelemetry(req.Metadata, attempt, pe, attemptLatency, false)
						release(0)
						p.budgetRelease(scope)
						return nil, pe, true, attempt, attemptLatency, fellBack
					}
					nextAttempt, canFallback := planner.AfterFailure(ctx, attempt, pe)
					p.recordFailureTelemetry(req.Metadata, attempt, pe, attemptLatency, canFallback)
					if !canFallback {
						break
					}
					if p.metrics != nil {
						p.metrics.RecordFallback(string(pe.Kind))
					}
					fellBack = true
					req.Metadata.FallbackCount++
					p.log.Warn("direct stream attempt failed, falling back",
						"provider", attempt.Target.Provider, "model", attempt.Target.Model, "kind", pe.Kind)
					attempt = nextAttempt
					continue
				}
				p.log.Debug("direct stream connected (zero-copy)", "provider", attempt.Target.Provider,
					"model", attempt.Target.Model)
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
				streamBody := newStallReadCloser(
					body,
					p.resolvedStallTimeout(),
					cancelUpstream,
					attempt.Target.Provider,
					attempt.Target.Model,
				)
				wrapped := &teeReadCloser{r: streamBody, w: io.MultiWriter(&capture, firstByte)}
				meta := req.Metadata
				acc := attempt
				saveCopy := save
				budgetScope := scope
				var completeOnce sync.Once
				completeFunc := func(streamErr error) {
					completeOnce.Do(func() {
						defer release(0)
						recordCtx := context.WithoutCancel(ctx)
						usage := capturedStreamUsage(req, capture.Bytes())
						if errors.Is(streamErr, context.Canceled) {
							usage = partialStreamUsage(req, usage, 0)
							status, errorKind := terminalStatusAndKind(streamErr)
							cost := p.recordOutcomeWithTTFT(recordCtx, meta, acc, usage, status, errorKind, false,
								time.Since(started), time.Since(requestStarted), ttft, saveCopy)
							p.budgetConfirm(budgetScope, cost)
							return
						}
						if streamErr != nil {
							pe := p.recordTerminalStreamFailure(recordCtx, req, meta, acc, streamErr,
								started, requestStarted, usage, 0, ttft, saveCopy, fellBack, budgetScope)
							p.log.Warn("direct stream terminated with upstream error",
								"provider", acc.Target.Provider, "model", acc.Target.Model, "kind", pe.Kind)
							return
						}

						totalLatency := time.Since(started)
						p.dispatcher.NoteSuccessAt(recordCtx, acc.Target.Provider, acc.Account.ID, acc.Target.Model, started)
						cost := p.recordWithTTFT(recordCtx, meta, acc, usage, false,
							totalLatency, time.Since(requestStarted), ttft, saveCopy, fellBack)
						p.budgetConfirm(budgetScope, cost)
					})
				}
				return &StreamResult{
					DirectBody:         wrapped,
					Provider:           attempt.Target.Provider,
					Model:              attempt.Target.Model,
					AccountID:          attempt.Account.ID,
					DirectCompleteFunc: completeFunc,
				}, nil, false, attempt, 0, fellBack
			}
		}

		// Standard channel path: parse upstream SSE into canonical chunks,
		// then render back to the client's dialect.
		upstream, callErr := attempt.Conn.Stream(callCtx, attemptReq, attempt.Creds, streamCfg)
		if callErr != nil && isUnsupportedCloudflareImageError(callErr, attempt.Target.Provider, attempt.Target.Model, attemptReq) {
			cancelUpstream()
			retryReq := cloneForAttempt(req, attempt.Target.Model)
			capability.StripImages(retryReq)
			callCtx, cancelUpstream = p.streamCallContext(ctx, attempt.Creds)
			upstream, callErr = attempt.Conn.Stream(callCtx, retryReq, attempt.Creds, streamCfg)
		}
		if callErr != nil {
			cancelUpstream()
			pe := providerErrorForAttempt(callErr, attempt)
			lastErr = pe
			p.dispatcher.NoteFailure(ctx, attempt.Account.ID, pe)
			if p.metrics != nil {
				p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
			}
			attemptLatency := time.Since(started)
			lastLatency = attemptLatency
			if !pe.Fallbackable() {
				p.recordFailureTelemetry(req.Metadata, attempt, pe, attemptLatency, false)
				p.log.Debug("stream attempt not fallbackable, aborting", "kind", pe.Kind)
				release(0)
				p.budgetRelease(scope)
				return nil, pe, true, attempt, attemptLatency, fellBack
			}
			nextAttempt, canFallback := planner.AfterFailure(ctx, attempt, pe)
			p.recordFailureTelemetry(req.Metadata, attempt, pe, attemptLatency, canFallback)
			if !canFallback {
				break
			}
			if p.metrics != nil {
				p.metrics.RecordFallback(string(pe.Kind))
			}
			fellBack = true
			req.Metadata.FallbackCount++
			p.log.Warn("stream attempt failed, falling back",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model, "kind", pe.Kind)
			attempt = nextAttempt
			continue
		}

		upstream, callErr = admitStreamNotify(callCtx, upstream, p.resolvedStallTimeout(), attempt.Target.Provider, attempt.Target.Model,
			opts.OnStreamPending, streamPendingNotifyAfter)
		if callErr != nil {
			cancelUpstream()
			pe := providerErrorForAttempt(callErr, attempt)
			lastErr = pe
			p.dispatcher.NoteFailure(ctx, attempt.Account.ID, pe)
			if p.metrics != nil {
				p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
			}
			attemptLatency := time.Since(started)
			lastLatency = attemptLatency
			if !pe.Fallbackable() {
				p.recordFailureTelemetry(req.Metadata, attempt, pe, attemptLatency, false)
				release(0)
				p.budgetRelease(scope)
				return nil, pe, true, attempt, attemptLatency, fellBack
			}
			nextAttempt, canFallback := planner.AfterFailure(ctx, attempt, pe)
			p.recordFailureTelemetry(req.Metadata, attempt, pe, attemptLatency, canFallback)
			if !canFallback {
				break
			}
			if p.metrics != nil {
				p.metrics.RecordFallback(string(pe.Kind))
			}
			fellBack = true
			req.Metadata.FallbackCount++
			p.log.Warn("stream rejected before output, falling back",
				"provider", attempt.Target.Provider, "model", attempt.Target.Model, "kind", pe.Kind)
			attempt = nextAttempt
			continue
		}

		p.log.Debug("stream connected", "provider", attempt.Target.Provider,
			"model", attempt.Target.Model, "account", attempt.Account.ID)

		// Tee the upstream channel so we can meter terminal usage without
		// blocking the client consumer.
		out := make(chan core.StreamChunk, 16)
		meta := req.Metadata
		acc := attempt
		go p.pumpStream(ctx, req, upstream, out, meta, acc, started, requestStarted,
			&ttft, save, scope, release, fellBack, cancelUpstream)

		return &StreamResult{
			Chunks:    out,
			Provider:  attempt.Target.Provider,
			Model:     attempt.Target.Model,
			AccountID: attempt.Account.ID,
		}, nil, false, attempt, 0, fellBack
	}

	if lastErr == nil {
		lastErr = &core.ProviderError{Kind: core.ErrInternal, Message: "pipeline: no attempts executed"}
	}
	if lastAttempt.Target.Provider == "" && lastAttempt.Target.Model == "" {
		lastAttempt = attemptForTargets(opts.Targets)
	}
	release(0)
	p.budgetRelease(scope)
	return nil, lastErr, true, lastAttempt, lastLatency, fellBack
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
	meta core.RequestMetadata, attempt dispatch.Attempt, attemptStarted, requestStarted time.Time,
	ttft *time.Duration, save *saveState, scope budget.Scope, release limits.ReleaseFunc,
	fellBack bool, cancelUpstream context.CancelFunc) {
	defer close(out)
	defer release(0)
	defer cancelUpstream()

	// Resolve effective stall timeout (dynamic from dashboard or static config).
	stallTimeout := p.resolvedStallTimeout()

	// A single timer channel is selected by the same goroutine that owns
	// terminal accounting. This avoids cancellation/stall races and guarantees
	// every exit settles the budget exactly once.
	var stallTimer *time.Timer
	var stallC <-chan time.Time
	if stallTimeout > 0 {
		stallTimer = time.NewTimer(stallTimeout)
		stallC = stallTimer.C
	}
	resetStall := func() {
		if stallTimer == nil {
			return
		}
		if !stallTimer.Stop() {
			select {
			case <-stallTimer.C:
			default:
			}
		}
		stallTimer.Reset(stallTimeout)
	}
	stopStall := func() {
		if stallTimer != nil {
			stallTimer.Stop()
		}
	}
	defer stopStall()

	var usage core.Usage
	var sawUsage bool
	var completionChars int
	// sawFinish/sawToolCall detect a stream that was cut off: an EOF without
	// a terminal event. LiteLLM synthesizes finish_reason=stop in that case;
	// we do the same once output exists, and report an error when none does.
	var sawFinish, sawToolCall bool
	nameRestorer := newToolNameRestorer(req.Tools)

	settleClientCancellation := func() {
		cancelUpstream()
		usage = partialStreamUsage(req, usage, completionChars)
		status, errorKind := terminalStatusAndKind(context.Canceled)
		cost := p.recordOutcomeWithTTFT(context.WithoutCancel(ctx), meta, attempt, usage,
			status, errorKind, false, time.Since(attemptStarted), time.Since(requestStarted), *ttft, save)
		p.budgetConfirm(scope, cost)
	}
	settleStall := func() {
		if ctx.Err() != nil {
			settleClientCancellation()
			return
		}
		cancelUpstream()
		stallErr := &core.ProviderError{
			Kind: core.ErrTimeout, Provider: attempt.Target.Provider, Model: attempt.Target.Model,
			Message: "stream stall: no data received for " + stallTimeout.String(),
		}
		pe := p.recordTerminalStreamFailure(ctx, req, meta, attempt, stallErr,
			attemptStarted, requestStarted, usage, completionChars, *ttft, save, fellBack, scope)
		select {
		case out <- core.StreamChunk{Type: core.ChunkError, Err: pe}:
		case <-ctx.Done():
		}
	}

	// Sliding window for outbound guardrails. We buffer the tail of the
	// assistant's text output and ask the engine to scan it every
	// chunkScanWindow characters. The window is small (256 chars) so the
	// added per-chunk regex work is microseconds, but large enough that
	// matches that span chunk boundaries (PII split across 2 frames) still
	// fire. ChunkText.Delta is the only chunk type we feed the scanner;
	// thinking, tool calls, and usage events are passed through untouched.
	const chunkScanWindow = 256
	var streamBuf strings.Builder

	// sawUsage tracks whether the upstream ever emitted a usage event with real
	// token counts. completionChars accumulates the assistant's streamed output
	// (text + reasoning) length so we can synthesize an explicit estimate when
	// the provider reported nothing.
	for {
		select {
		case chunk, ok := <-in:
			if !ok {
				if ctx.Err() != nil {
					settleClientCancellation()
					return
				}
				stopStall()
				if !sawFinish {
					if completionChars == 0 && !sawToolCall {
						// The upstream closed the connection before producing
						// anything: a failure, not an empty answer.
						truncErr := &core.ProviderError{
							Kind: core.ErrUpstream, Scope: core.FailureScopeProvider,
							Provider: attempt.Target.Provider, Model: attempt.Target.Model,
							Message: "upstream closed the stream before producing output",
						}
						pe := p.recordTerminalStreamFailure(ctx, req, meta, attempt, truncErr,
							attemptStarted, requestStarted, usage, completionChars, *ttft, save, fellBack, scope)
						select {
						case out <- core.StreamChunk{Type: core.ChunkError, Err: pe}:
						case <-ctx.Done():
						}
						return
					}
					// Output arrived but no terminal event did (the upstream
					// hung up cleanly mid-generation, or the dialect never
					// sends one). Close the client's stream properly.
					finish := core.FinishStop
					if sawToolCall {
						finish = core.FinishToolCalls
					}
					p.log.Debug("stream ended without finish event; synthesizing",
						"provider", attempt.Target.Provider, "model", attempt.Target.Model, "finish", finish)
					select {
					case out <- core.StreamChunk{Type: core.ChunkFinish, FinishReason: finish}:
					case <-ctx.Done():
						settleClientCancellation()
						return
					}
				}
				// Always synthesize an auditable estimate when the provider omits
				// usage. Only emit it to the client when include_usage was requested.
				if !sawUsage {
					est := estimateStreamUsage(req, completionChars)
					usage = est
					if req.IncludeUsage && est.TotalTokens > 0 {
						select {
						case out <- core.StreamChunk{Type: core.ChunkUsage, Usage: &est}:
						case <-ctx.Done():
							settleClientCancellation()
							return
						}
					}
				}
				upstreamLatency := time.Since(attemptStarted)
				p.dispatcher.NoteSuccessAt(context.WithoutCancel(ctx), attempt.Target.Provider, attempt.Account.ID, attempt.Target.Model, attemptStarted)
				cost := p.recordWithTTFT(ctx, meta, attempt, usage, false, upstreamLatency,
					time.Since(requestStarted), *ttft, save, fellBack)
				p.budgetConfirm(scope, cost)
				return
			}
			resetStall()
			if chunk.Type == core.ChunkPing {
				// Keep-alive from the upstream: proves liveness for the stall
				// detector but carries nothing for the client.
				continue
			}
			if chunk.Type == core.ChunkError {
				if ctx.Err() != nil {
					settleClientCancellation()
					return
				}
				streamErr := chunk.Err
				if streamErr == nil {
					streamErr = &core.ProviderError{Kind: core.ErrUpstream, Message: "provider stream failed"}
				}
				pe := p.recordTerminalStreamFailure(ctx, req, meta, attempt, streamErr,
					attemptStarted, requestStarted, usage, completionChars, *ttft, save, fellBack, scope)
				chunk.Err = pe
				select {
				case out <- chunk:
				case <-ctx.Done():
				}
				return
			}
			switch chunk.Type {
			case core.ChunkUsage:
				if chunk.Usage != nil {
					usage = mergeUsage(usage, *chunk.Usage)
					if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 || chunk.Usage.TotalTokens > 0 {
						sawUsage = true
					}
				}
			case core.ChunkText, core.ChunkThinking:
				completionChars += len(chunk.Delta)
			case core.ChunkToolCall:
				sawToolCall = true
				nameRestorer.restore(chunk.ToolCall)
			case core.ChunkFinish:
				sawFinish = true
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
						select {
						case out <- core.StreamChunk{
							Type: core.ChunkError,
							Err: &core.ProviderError{
								Kind:     core.ErrPolicyBlocked,
								Provider: attempt.Target.Provider,
								Model:    attempt.Target.Model,
								Message:  gres.Reason,
							},
						}:
						case <-ctx.Done():
							settleClientCancellation()
							return
						}
						cancelUpstream()
						blockedUsage := partialStreamUsage(req, usage, completionChars)
						upstreamLatency := time.Since(attemptStarted)
						cost := p.recordOutcomeWithTTFT(context.WithoutCancel(ctx), meta, attempt, blockedUsage,
							"blocked", string(core.ErrPolicyBlocked), false, upstreamLatency,
							time.Since(requestStarted), *ttft, save)
						p.dispatcher.NoteSuccess(context.WithoutCancel(ctx), attempt.Target.Provider, attempt.Account.ID, attempt.Target.Model)
						p.recordSuccessTelemetry(meta, attempt, blockedUsage, upstreamLatency, *ttft, fellBack, cost)
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
			case <-ctx.Done():
				settleClientCancellation()
				return
			case <-stallC:
				settleStall()
				return
			}
		case <-ctx.Done():
			settleClientCancellation()
			return
		case <-stallC:
			settleStall()
			return
		}
	}
}

// estimateStreamUsage synthesizes a usage snapshot for a streaming response
// when the upstream provider reported no token counts. Prompt tokens are
// estimated from the request; completion tokens from the streamed output
// length. Both use the ~4 chars/token heuristic. Used only as a fallback so
// include_usage clients always receive a usage event.
func estimateStreamUsage(req *core.ChatRequest, completionChars int) core.Usage {
	prompt := core.EstimatePromptTokens(req)
	completion := core.EstimateTokensFromChars(completionChars)
	return core.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		Source:           core.UsageSourceEstimated,
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
	if new.Source != "" {
		old.Source = new.Source
	}
	if old.PromptTokens+old.CompletionTokens > 0 {
		old.TotalTokens = old.PromptTokens + old.CompletionTokens
	}
	return old
}

func addAttemptUsage(total, attempt core.Usage) core.Usage {
	totalTokens := total.TotalTokens
	if totalTokens == 0 {
		totalTokens = total.PromptTokens + total.CompletionTokens
	}
	attemptTokens := attempt.TotalTokens
	if attemptTokens == 0 {
		attemptTokens = attempt.PromptTokens + attempt.CompletionTokens
	}

	total.PromptTokens += attempt.PromptTokens
	total.CompletionTokens += attempt.CompletionTokens
	total.TotalTokens = totalTokens + attemptTokens
	total.CachedTokens += attempt.CachedTokens
	total.CacheWriteTokens += attempt.CacheWriteTokens
	total.ReasoningTokens += attempt.ReasoningTokens

	switch {
	case total.Source == "":
		total.Source = attempt.Source
	case attempt.Source == "":
	case total.Source != attempt.Source:
		total.Source = core.UsageSourceEstimated
	}
	return total
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

// planWithCooldownRetry waits for the earliest typed cooldown reported by the
// dispatcher. Long quota/auth windows are returned immediately; only cooldowns
// that fit the request's bounded wait budget are retried.
func (p *Pipeline) planWithCooldownRetry(ctx context.Context, tenantID string, targets []dispatch.Target, required core.CapabilitySet, opts dispatch.PlanOptions) ([]dispatch.Attempt, error) {
	// The attempt planner consumes one candidate per plan and re-plans after
	// each failure, so resolving (and OAuth-refreshing) more than one account
	// here is wasted work on every request.
	opts.Limit = 1
	attempts, err := p.dispatcher.PlanWith(ctx, tenantID, targets, required, opts)
	if err == nil && len(attempts) > 0 {
		return attempts, nil
	}
	if err == nil {
		return attempts, err
	}

	deadline := time.Now().Add(CooldownRetryMax)
	for i := 0; i < 3; i++ {
		pe := core.AsProviderError(err)
		if pe.Kind != core.ErrRateLimit || pe.RetryAfter <= 0 {
			return attempts, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		wait := pe.RetryAfter + cooldownWaitMargin
		remaining := time.Until(deadline)
		if wait <= 0 || remaining <= 0 || wait > remaining {
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
		if err == nil {
			return attempts, err
		}
	}
	return attempts, err
}

// applyTokenSaving runs the input-side (slimmer/RTK, headroom) and output-side
// (terse, caveman, ponytail) token-saving transforms in place, in a fixed
// deterministic order before format translation:
//
//	normalizer -> slimmer -> headroom -> normalizer (input) -> terse -> caveman -> ponytail (output)
//
// Terse and caveman both inject system-prompt directives; if both are enabled,
// terse runs first and caveman appends after, but in practice only one
// output-saver is used. Ponytail appends its block last, preserving any
// existing directives. Headroom is fail-open and never returns an error.
func (p *Pipeline) applyTokenSaving(ctx context.Context, req *core.ChatRequest, opts Options) (*slimmer.Stats, *headroom.Stats) {
	// Normalize tool call IDs and fix missing tool results before any
	// downstream processing. This ensures Anthropic-compatible IDs and
	// complete tool_use/tool_result pairs.
	normalizer.Apply(req)

	var stats *slimmer.Stats
	if p.slimmer != nil && opts.Slimmer.Enabled {
		stats = p.slimmer.Compress(req, opts.Slimmer)
		if stats != nil {
			p.log.Debug("slimmer compressed request", "saved_bytes", stats.Saved(), "hits", len(stats.Hits))
		}
	}

	var hrStats *headroom.Stats
	if p.headroom != nil && opts.Headroom.Enabled {
		// Fail-open: never returns an error; leaves req untouched on failure.
		hrStats = p.headroom.Compress(ctx, req, opts.Headroom)
	}

	// Compression may remove one side of a tool call/result pair. Reconcile the
	// final message history again before it reaches a provider connector.
	normalizer.Apply(req)

	terse.Apply(req, opts.Terse)
	caveman.Apply(req, opts.Caveman)
	ponytail.Apply(req, opts.Ponytail)
	return stats, hrStats
}

// saveState captures which token-saving features were active and their results
// for a single request, so the meter can persist them.
type saveState struct {
	slimSnap     *meter.SlimSnapshot
	headroomSnap *meter.HeadroomSnapshot
	slim         bool
	caveman      bool
	terse        bool
	ponytail     bool
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
//
// The Headroom snapshot is populated only when the compressor achieved real
// (non-phantom) token savings; in every other case headroomSnap stays nil so
// the meter records zero tokens/bytes and HeadroomActive=false. The Ponytail
// flag mirrors opts.Ponytail.Enabled.
func buildSaveState(stats *slimmer.Stats, hr *headroom.Stats, opts Options) *saveState {
	save := &saveState{
		slim:     opts.Slimmer.Enabled,
		caveman:  opts.Caveman.Enabled,
		terse:    opts.Terse.Enabled,
		ponytail: opts.Ponytail.Enabled,
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
	// Populate the Headroom snapshot only for real, non-phantom savings
	// (values already clamped >= 0 by headroom.Stats).
	if hr != nil && hr.Compressed && hr.TokensSaved > 0 && !hr.Phantom {
		save.headroomSnap = &meter.HeadroomSnapshot{
			TokensSaved: hr.TokensSaved,
			BytesSaved:  hr.BytesSaved,
			Active:      true,
		}
	}
	if save.slimSnap == nil && save.headroomSnap == nil && !save.caveman && !save.terse && !save.ponytail {
		return nil
	}
	return save
}

// record retains the original internal compatibility signature; callers that
// know the full request duration use recordWithEndToEnd.
func (p *Pipeline) record(ctx context.Context, meta core.RequestMetadata, attempt dispatch.Attempt,
	usage core.Usage, cacheHit bool, upstreamLatency time.Duration, save *saveState, fellBack bool) int64 {
	return p.recordWithTTFT(ctx, meta, attempt, usage, cacheHit, upstreamLatency, upstreamLatency, 0, save, fellBack)
}

func (p *Pipeline) recordWithEndToEnd(ctx context.Context, meta core.RequestMetadata, attempt dispatch.Attempt,
	usage core.Usage, cacheHit bool, upstreamLatency, endToEndLatency time.Duration,
	save *saveState, fellBack bool) int64 {
	return p.recordWithTTFT(ctx, meta, attempt, usage, cacheHit, upstreamLatency, endToEndLatency, 0, save, fellBack)
}

// recordSuccessTelemetry emits a best-effort health telemetry event for a
// successful upstream attempt. fellBack reports whether the request triggered
// at least one fallback before succeeding, so the chain-impact view computes
// an accurate fallback rate per request (not per attempt).
func (p *Pipeline) recordSuccessTelemetry(meta core.RequestMetadata, attempt dispatch.Attempt,
	usage core.Usage, latency, ttft time.Duration, fellBack bool, costMicros int64) {
	if p.telemetry == nil {
		return
	}
	ev := health.ProviderTelemetryEvent{
		Timestamp:         time.Now(),
		Provider:          attempt.Target.Provider,
		ProviderAccountID: attempt.Account.ID,
		Model:             attempt.Target.Model,
		Capability:        capabilityOf(attempt.Target.Provider, attempt.Target.Model),
		Status:            "success",
		LatencyMs:         int(latency.Milliseconds()),
		TTFTMs:            int(ttft.Milliseconds()),
		InputTokens:       usage.PromptTokens,
		OutputTokens:      usage.CompletionTokens,
		CostMicroUSD:      costMicros,
		ChainID:           meta.ChainID,
		FallbackTriggered: fellBack,
	}
	p.telemetry.Record(ev)
}

// recordTerminalStreamFailure updates routing cooldowns, metrics, health, usage,
// and budget state when an upstream fails after response headers were committed.
// Partial provider usage is retained; if absent, input/output are explicitly
// estimated from the canonical request and streamed text seen so far.
func (p *Pipeline) recordTerminalStreamFailure(ctx context.Context, req *core.ChatRequest,
	meta core.RequestMetadata, attempt dispatch.Attempt, streamErr error,
	attemptStarted, requestStarted time.Time, usage core.Usage, completionChars int,
	ttft time.Duration, save *saveState, fellBack bool, scope budget.Scope) *core.ProviderError {
	pe := providerErrorForAttempt(streamErr, attempt)
	clientCanceled := core.IsClientDisconnect(pe)
	if !clientCanceled {
		p.dispatcher.NoteFailure(context.WithoutCancel(ctx), attempt.Account.ID, pe)
		if p.metrics != nil {
			p.metrics.RecordUpstreamError(attempt.Target.Provider, string(pe.Kind))
		}
		upstreamLatency := time.Since(attemptStarted)
		p.recordFailureTelemetry(meta, attempt, pe, upstreamLatency, false)
	}
	upstreamLatency := time.Since(attemptStarted)
	usage = partialStreamUsage(req, usage, completionChars)
	pe, cost := p.recordAttemptTerminal(ctx, meta, attempt, pe, usage, upstreamLatency,
		time.Since(requestStarted), ttft, save, fellBack)
	p.budgetConfirm(scope, cost)
	return pe
}

// recordFailureTelemetry emits a best-effort health telemetry event for a
// failed upstream attempt. fallbackable reports whether the dispatcher
// advanced to the next candidate (i.e. fallback was triggered).
func (p *Pipeline) recordFailureTelemetry(meta core.RequestMetadata, attempt dispatch.Attempt,
	pe *core.ProviderError, latency time.Duration, fallbackable bool) {
	if p.telemetry == nil || pe == nil {
		return
	}
	ev := health.ProviderTelemetryEvent{
		Timestamp:         time.Now(),
		Provider:          attempt.Target.Provider,
		ProviderAccountID: attempt.Account.ID,
		Model:             attempt.Target.Model,
		Capability:        capabilityOf(attempt.Target.Provider, attempt.Target.Model),
		Status:            "failed",
		HTTPStatus:        pe.StatusCode,
		LatencyMs:         int(latency.Milliseconds()),
		ErrorType:         health.ClassifyError(pe),
		ErrorMessage:      pe.Message,
		ChainID:           meta.ChainID,
		FallbackTriggered: fallbackable,
	}
	p.telemetry.Record(ev)
}

// recordFinalFailureTelemetry emits a best-effort health telemetry event when
// all attempts in a chain have failed, so the chain-impact view can count
// final failures (requests lost after every fallback).
func (p *Pipeline) recordFinalFailureTelemetry(meta core.RequestMetadata, attempt dispatch.Attempt,
	lastErr error, fellBack bool) {
	if p.telemetry == nil || lastErr == nil {
		return
	}
	pe, _ := lastErr.(*core.ProviderError)
	if pe == nil {
		pe = core.AsProviderError(lastErr)
	}
	provider, model := attempt.Target.Provider, attempt.Target.Model
	if provider == "" {
		provider = pe.Provider
	}
	if model == "" {
		model = pe.Model
	}
	ev := health.ProviderTelemetryEvent{
		Timestamp:         time.Now(),
		Provider:          provider,
		ProviderAccountID: attempt.Account.ID,
		Model:             model,
		Capability:        capabilityOf(provider, model),
		Status:            "failed",
		HTTPStatus:        pe.StatusCode,
		ErrorType:         health.ClassifyError(pe),
		ErrorMessage:      pe.Message,
		ChainID:           meta.ChainID,
		FinalFailure:      true,
		FallbackTriggered: fellBack,
	}
	p.telemetry.Record(ev)
}

func terminalStatusAndKind(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return "cancelled", "cancelled"
	}
	pe := core.AsProviderError(err)
	if pe != nil && pe.Kind == core.ErrClientCanceled {
		return "cancelled", string(pe.Kind)
	}
	if pe != nil && pe.Kind == core.ErrPolicyBlocked {
		return "blocked", string(pe.Kind)
	}
	if pe != nil {
		return "failed", string(pe.Kind)
	}
	return "failed", "internal"
}

func providerErrorForAttempt(err error, attempt dispatch.Attempt) *core.ProviderError {
	if err == nil {
		err = &core.ProviderError{Kind: core.ErrInternal, Message: "request failed"}
	}
	pe := core.AsProviderError(err)
	copy := *pe
	pe = &copy
	if pe.Provider == "" {
		pe.Provider = attempt.Target.Provider
	}
	if pe.Model == "" {
		pe.Model = attempt.Target.Model
	}
	return pe
}

func attemptForTargets(targets []dispatch.Target) dispatch.Attempt {
	provider, model := firstTarget(targets)
	return dispatch.Attempt{Target: dispatch.Target{Provider: provider, Model: model}}
}

// recordLocalTerminal records requests rejected before an upstream attempt
// exists (limits, budgets, policy, planning, or client cancellation). These do
// not affect provider health because no provider was contacted.
func (p *Pipeline) recordLocalTerminal(ctx context.Context, meta core.RequestMetadata,
	targets []dispatch.Target, err error, requestStarted time.Time) {
	status, errorKind := terminalStatusAndKind(err)
	p.recordOutcomeWithTTFT(context.WithoutCancel(ctx), meta, attemptForTargets(targets), core.Usage{},
		status, errorKind, false, 0, time.Since(requestStarted), 0, nil)
}

// recordAttemptTerminal persists the single terminal row for a request whose
// final provider attempt failed. Per-attempt health telemetry is emitted by the
// caller; this helper emits only the chain-level final-failure marker.
func (p *Pipeline) recordAttemptTerminal(ctx context.Context, meta core.RequestMetadata,
	attempt dispatch.Attempt, err error, usage core.Usage, upstreamLatency, endToEndLatency,
	ttft time.Duration, save *saveState, fellBack bool) (*core.ProviderError, int64) {
	pe := providerErrorForAttempt(err, attempt)
	status, errorKind := terminalStatusAndKind(pe)
	cost := p.recordOutcomeWithTTFT(context.WithoutCancel(ctx), meta, attempt, usage,
		status, errorKind, false, upstreamLatency, endToEndLatency, ttft, save)
	if pe.Kind != core.ErrClientCanceled {
		p.recordFinalFailureTelemetry(meta, attempt, pe, fellBack)
	}
	return pe, cost
}

func partialStreamUsage(req *core.ChatRequest, usage core.Usage, completionChars int) core.Usage {
	if usage.PromptTokens+usage.CompletionTokens > 0 {
		return usage
	}
	return estimateStreamUsage(req, completionChars)
}

// capabilityOf derives the capability label for a target. KeiRouter routes by
// service kind, but telemetry keys on a stable capability string; default to
// chat_completions for LLM models.
func capabilityOf(provider, model string) string {
	_ = provider
	_ = model
	return "chat_completions"
}

// recordWithTTFT records a successful upstream attempt and emits provider
// health telemetry. endToEndLatency includes routing, fallback, and transforms;
// upstreamLatency covers only the winning provider attempt.
func (p *Pipeline) recordWithTTFT(ctx context.Context, meta core.RequestMetadata, attempt dispatch.Attempt,
	usage core.Usage, cacheHit bool, upstreamLatency, endToEndLatency, ttft time.Duration,
	save *saveState, fellBack bool) int64 {
	cost := p.recordOutcomeWithTTFT(ctx, meta, attempt, usage, "success", "", cacheHit,
		upstreamLatency, endToEndLatency, ttft, save)
	if !cacheHit {
		p.recordSuccessTelemetry(meta, attempt, usage, upstreamLatency, ttft, fellBack, cost)
	}
	return cost
}

// recordOutcomeWithTTFT is the single persistence path for every terminal
// request status. It deliberately does not emit provider-health telemetry:
// callers know whether a blocked/failed request reflects provider behavior.
func (p *Pipeline) recordOutcomeWithTTFT(ctx context.Context, meta core.RequestMetadata, attempt dispatch.Attempt,
	usage core.Usage, status, errorKind string, cacheHit bool,
	upstreamLatency, endToEndLatency, ttft time.Duration, save *saveState) int64 {
	if status == "" {
		status = "success"
	}
	if usage.Source == "" && usage.PromptTokens+usage.CompletionTokens > 0 {
		usage.Source = core.UsageSourceProvider
	}
	ev := meter.Event{
		RequestID:       meta.RequestID,
		TenantID:        meta.TenantID,
		ProjectID:       meta.ProjectID,
		APIKeyID:        meta.APIKeyID,
		Provider:        attempt.Target.Provider,
		Model:           attempt.Target.Model,
		AccountID:       attempt.Account.ID,
		Client:          meta.ClientKind,
		ChainID:         meta.ChainID,
		FallbackCount:   meta.FallbackCount,
		Status:          status,
		ErrorKind:       errorKind,
		Usage:           usage,
		UsageSource:     string(usage.Source),
		CacheHit:        cacheHit,
		Latency:         upstreamLatency,
		EndToEndLatency: endToEndLatency,
		TTFT:            ttft,
	}
	if save != nil {
		ev.SlimStats = save.slimSnap
		ev.SlimActive = save.slim
		ev.CavemanActive = save.caveman
		ev.TerseActive = save.terse
		ev.HeadroomStats = save.headroomSnap
		ev.PonytailActive = save.ponytail
	}

	var cost int64
	if p.meter != nil {
		var err error
		cost, err = p.meter.Record(ctx, ev)
		if err != nil {
			p.log.Error("failed to record usage", "status", status, "err", err)
		}
	}

	if p.metrics != nil {
		p.metrics.RecordRequest(
			attempt.Target.Provider, attempt.Target.Model, status,
			upstreamLatency.Seconds(),
			usage.PromptTokens, usage.CompletionTokens, usage.CachedTokens, cost,
			ttft.Seconds(),
		)
		if cacheHit {
			p.metrics.RecordCache(true)
		}
		if save != nil {
			if save.slimSnap != nil {
				for _, rule := range splitRuleNames(save.slimSnap.Rules) {
					p.metrics.RecordSlimSavings(rule, save.slimSnap.BytesSaved)
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
// toolNameRestorer maps provider-sanitized tool names (Gemini rewrites
// characters and truncates at 64) back to the names the client declared, so
// the client never receives a tool call for a tool it does not know.
type toolNameRestorer map[string]string

func newToolNameRestorer(tools []core.Tool) toolNameRestorer {
	if len(tools) == 0 {
		return nil
	}
	var m toolNameRestorer
	for _, t := range tools {
		for _, alias := range transform.ToolNameAliases(t.Name) {
			if alias == t.Name {
				continue
			}
			if m == nil {
				m = make(toolNameRestorer, len(tools))
			}
			m[alias] = t.Name
		}
	}
	return m
}

// restore rewrites a tool call name in place when it is a known alias.
func (r toolNameRestorer) restore(tc *core.ToolCall) {
	if r == nil || tc == nil || tc.Name == "" {
		return
	}
	if orig, ok := r[tc.Name]; ok {
		tc.Name = orig
	}
}

func cloneForAttempt(req *core.ChatRequest, model string) *core.ChatRequest {
	clone := *req
	clone.Messages = make([]core.Message, len(req.Messages))
	for i, message := range req.Messages {
		clone.Messages[i] = message
		clone.Messages[i].Content = append([]core.ContentPart(nil), message.Content...)
	}
	clone.Model = model
	return &clone
}

func cloneWithSystemInstruction(req *core.ChatRequest, instruction string) *core.ChatRequest {
	clone := *req
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return &clone
	}
	if strings.TrimSpace(clone.System) == "" {
		clone.System = instruction
	} else {
		clone.System += "\n\n" + instruction
	}
	return &clone
}

// isStreamRequiredError reports whether the error indicates the upstream
// provider rejected a non-streaming request with "Stream must be set to true".
// Some providers only accept streaming requests and return HTTP 400 when
// stream=false. Since ErrBadRequest is not fallbackable, the pipeline must
// detect this specific case and retry with streaming.
func isStreamRequiredError(err error) bool {
	if err == nil {
		return false
	}
	pe := core.AsProviderError(err)
	if pe == nil {
		return false
	}
	// Accept any 4xx (some providers return 400, others 422).
	if pe.StatusCode < 400 || pe.StatusCode >= 500 {
		return false
	}
	msg := strings.ToLower(pe.Message)
	return strings.Contains(msg, "stream must be set to true") ||
		strings.Contains(msg, "streaming is required") ||
		strings.Contains(msg, "stream parameter is required") ||
		strings.Contains(msg, `"stream" must be true`)
}

// drainStream consumes a stream channel and folds the chunks into a single
// ChatResponse. Used by the pipeline when a non-streaming Chat() call failed
// with a "Stream must be set to true" error and the pipeline retries with
// streaming internally.
func drainStream(stream <-chan core.StreamChunk, model string) (*core.ChatResponse, error) {
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

// stallReadCloser enforces the configured no-data timeout for direct streams.
// Closing the body unblocks an in-flight Read while cancelling the connector
// context terminates any producer goroutines owned by the connector.
type stallReadCloser struct {
	body     io.ReadCloser
	cancel   context.CancelFunc
	timeout  time.Duration
	provider string
	model    string

	mu       sync.Mutex
	timer    *time.Timer
	closed   bool
	timedOut bool
}

func newStallReadCloser(body io.ReadCloser, timeout time.Duration, cancel context.CancelFunc, provider, model string) io.ReadCloser {
	s := &stallReadCloser{
		body: body, cancel: cancel, timeout: timeout, provider: provider, model: model,
	}
	if timeout > 0 {
		s.timer = time.AfterFunc(timeout, s.handleTimeout)
	}
	return s
}

func (s *stallReadCloser) handleTimeout() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.timedOut = true
	s.mu.Unlock()

	s.cancel()
	_ = s.body.Close()
}

func (s *stallReadCloser) Read(p []byte) (int, error) {
	n, err := s.body.Read(p)

	s.mu.Lock()
	if n > 0 && !s.closed && s.timer != nil {
		s.timer.Reset(s.timeout)
	}
	timedOut := s.timedOut
	if err != nil && !s.closed && s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()

	if err != nil {
		s.cancel()
	}
	if timedOut {
		return n, &core.ProviderError{
			Kind: core.ErrTimeout, Provider: s.provider, Model: s.model,
			Message: "stream stall: no data received for " + s.timeout.String(), Cause: err,
		}
	}
	return n, err
}

func (s *stallReadCloser) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.cancel()
		return nil
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()

	s.cancel()
	return s.body.Close()
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
		PromptTokens             int `json:"prompt_tokens"`
		CompletionTokens         int `json:"completion_tokens"`
		TotalTokens              int `json:"total_tokens"`
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		PromptTokensDetails      *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	Message *struct {
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	UsageMetadata *struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
		TotalTokenCount         int `json:"totalTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

// extractUsageFromSSEData tries to parse a single SSE data payload for usage
// tokens. Returns nil when the payload contains no usage information.
func extractUsageFromSSEData(data []byte) *core.Usage {
	if !bytes.Contains(data, []byte(`"usage"`)) &&
		!bytes.Contains(data, []byte(`"usageMetadata"`)) &&
		!bytes.Contains(data, []byte(`"prompt_eval_count"`)) &&
		!bytes.Contains(data, []byte(`"eval_count"`)) {
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
		u.CachedTokens = env.Usage.CacheReadInputTokens
		u.CacheWriteTokens = env.Usage.CacheCreationInputTokens
		// Anthropic reports regular, cache-read, and cache-creation input as
		// separate categories. Canonical prompt usage includes all three; cache
		// counters remain subsets used to apply their component rates.
		if env.Usage.InputTokens > 0 || u.CachedTokens > 0 || u.CacheWriteTokens > 0 {
			u.PromptTokens = env.Usage.InputTokens + u.CachedTokens + u.CacheWriteTokens
		}
		if env.Usage.PromptTokensDetails != nil && env.Usage.PromptTokensDetails.CachedTokens > 0 {
			u.CachedTokens = env.Usage.PromptTokensDetails.CachedTokens
		}
		if env.Usage.CompletionTokensDetails != nil {
			u.ReasoningTokens = env.Usage.CompletionTokensDetails.ReasoningTokens
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
		u.CachedTokens = env.Message.Usage.CacheReadInputTokens
		u.CacheWriteTokens = env.Message.Usage.CacheCreationInputTokens
		if env.Message.Usage.InputTokens > 0 || u.CachedTokens > 0 || u.CacheWriteTokens > 0 {
			u.PromptTokens = env.Message.Usage.InputTokens + u.CachedTokens + u.CacheWriteTokens
		}
	}
	// Gemini streams usageMetadata instead of an OpenAI-style usage object.
	if env.UsageMetadata != nil {
		u.PromptTokens = env.UsageMetadata.PromptTokenCount
		u.CompletionTokens = env.UsageMetadata.CandidatesTokenCount + env.UsageMetadata.ThoughtsTokenCount
		u.TotalTokens = env.UsageMetadata.TotalTokenCount
		u.CachedTokens = env.UsageMetadata.CachedContentTokenCount
		u.ReasoningTokens = env.UsageMetadata.ThoughtsTokenCount
	}
	// Ollama's final NDJSON object exposes counters at the top level.
	if env.PromptEvalCount > 0 || env.EvalCount > 0 {
		u.PromptTokens = env.PromptEvalCount
		u.CompletionTokens = env.EvalCount
		u.TotalTokens = env.PromptEvalCount + env.EvalCount
	}
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 {
		return nil
	}
	u.Source = core.UsageSourceProvider
	if u.PromptTokens+u.CompletionTokens > 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
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
	scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)
	for scanner.Scan() {
		payload, ok := streamJSONPayloadBytes(scanner.Bytes())
		if !ok || !mayCarryUsage(payload) {
			continue
		}
		if u := extractUsageFromSSEData(payload); u != nil {
			usage = mergeUsage(usage, *u)
		}
	}
	return usage
}

var (
	usageMarker     = []byte("usage")      // OpenAI, Anthropic, Gemini ("usageMetadata")
	evalCountMarker = []byte("eval_count") // Ollama prompt_eval_count / eval_count
)

// mayCarryUsage is the pre-filter that lets frames without any token counts
// skip the JSON decode entirely.
func mayCarryUsage(payload []byte) bool {
	return bytes.Contains(payload, usageMarker) || bytes.Contains(payload, evalCountMarker)
}

// streamJSONPayloadBytes is streamJSONPayload without the string copies.
func streamJSONPayloadBytes(line []byte) ([]byte, bool) {
	line = bytes.TrimSpace(bytes.TrimRight(line, "\r"))
	if bytes.HasPrefix(line, []byte("data:")) {
		line = bytes.TrimSpace(line[5:])
	}
	if len(line) == 0 || line[0] != '{' {
		return nil, false
	}
	return line, true
}

// streamTextEnvelope is the minimal shape needed to count streamed output
// characters across dialects. It is reused across frames.
type streamTextEnvelope struct {
	Delta   json.RawMessage `json:"delta"`
	Message struct {
		Content  json.RawMessage `json:"content"`
		Thinking json.RawMessage `json:"thinking"`
	} `json:"message"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func capturedStreamUsage(req *core.ChatRequest, raw []byte) core.Usage {
	if usage := extractUsageFromStream(raw); usage.PromptTokens+usage.CompletionTokens > 0 {
		return usage
	}
	return estimateStreamUsage(req, completionCharsFromStream(raw))
}

func completionCharsFromStream(raw []byte) int {
	var chars int
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)
	var envelope streamTextEnvelope
	for scanner.Scan() {
		payload, ok := streamJSONPayloadBytes(scanner.Bytes())
		if !ok {
			continue
		}
		envelope = streamTextEnvelope{}
		if fastjson.Unmarshal(payload, &envelope) != nil {
			continue
		}
		for _, choice := range envelope.Choices {
			chars += len(choice.Delta.Content) + len(choice.Delta.ReasoningContent)
		}
		for _, candidate := range envelope.Candidates {
			for _, part := range candidate.Content.Parts {
				chars += len(part.Text)
			}
		}
		chars += rawJSONStringLen(envelope.Message.Content) + rawJSONStringLen(envelope.Message.Thinking)
		if delta := bytes.TrimSpace(envelope.Delta); len(delta) > 0 {
			// Branch on the first byte instead of a failing decode: Anthropic
			// deltas are objects, Responses deltas are strings.
			switch delta[0] {
			case '"':
				var text string
				if fastjson.Unmarshal(delta, &text) == nil {
					chars += len(text)
				}
			case '{':
				var antDelta struct {
					Text     string `json:"text"`
					Thinking string `json:"thinking"`
				}
				if fastjson.Unmarshal(delta, &antDelta) == nil {
					chars += len(antDelta.Text) + len(antDelta.Thinking)
				}
			}
		}
	}
	return chars
}

func rawJSONStringLen(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	return len(value)
}
