package health

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// ProbeRunner executes synthetic provider probes and records their results.
// It is shared by the manual probe API endpoint and may be reused by the
// scheduled probe worker.
type ProbeRunner struct {
	log        *slog.Logger
	repo       *store.ProviderHealthRepo
	accounts   *store.AccountRepo
	conns      *connectors.Registry
	vault      *vault.Vault
	thresholds LatencyThresholds
	timeout    time.Duration
	preparer   CredentialPreparer
}

// CredentialPreparer builds the credentials an account serves traffic with
// (proxy route, OAuth refresh, decrypted secrets). The dispatcher implements
// it; probes use it so they reach the upstream exactly like routed requests.
type CredentialPreparer interface {
	PrepareCredentials(ctx context.Context, acc store.Account) (store.Account, core.Credentials, error)
}

// SetCredentialPreparer routes credential preparation through p. Without one,
// probes fall back to decrypting the account directly from the vault.
func (r *ProbeRunner) SetCredentialPreparer(p CredentialPreparer) { r.preparer = p }

// NewProbeRunner builds a ProbeRunner.
func NewProbeRunner(log *slog.Logger, repo *store.ProviderHealthRepo, accounts *store.AccountRepo, conns *connectors.Registry, vault *vault.Vault, thresholds LatencyThresholds, timeout time.Duration) *ProbeRunner {
	if thresholds == nil {
		thresholds = DefaultLatencyThresholds()
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &ProbeRunner{log: log, repo: repo, accounts: accounts, conns: conns, vault: vault, thresholds: thresholds, timeout: timeout}
}

// ProbeRequest selects a probe target.
type ProbeRequest struct {
	Provider          string
	ProviderAccountID string
	Model             string
	Capability        string // chat_completions | embeddings | ...
	TriggeredBy       string // manual | scheduled | after_failure | startup
}

// ProbeResult is the outcome of one probe.
type ProbeResult struct {
	Provider          string
	ProviderAccountID string
	Model             string
	Capability        string
	Status            string // success | failed
	HTTPStatus        *int
	LatencyMs         *int
	TTFTMs            *int
	ErrorType         *string
	ErrorMessage      *string
	PromptTokens      *int
	CompletionTokens  *int
	CostMicroUSD      *int64
}

// Run executes one probe and persists the result. It never returns an error
// for a failed upstream — that is a valid probe outcome. It returns an error
// only for configuration problems (unknown provider, missing account).
func (r *ProbeRunner) Run(ctx context.Context, req ProbeRequest) (ProbeResult, error) {
	if req.Provider == "" || req.Model == "" {
		return ProbeResult{}, fmt.Errorf("provider and model are required")
	}
	if req.TriggeredBy == "" {
		req.TriggeredBy = "manual"
	}
	res := ProbeResult{
		Provider:          req.Provider,
		ProviderAccountID: req.ProviderAccountID,
		Model:             req.Model,
		Capability:        req.Capability,
	}

	conn, err := r.conns.Get(req.Provider)
	if err != nil {
		return res, fmt.Errorf("unknown provider: %s", req.Provider)
	}

	var creds core.Credentials
	if r.accounts != nil {
		acc, aerr := r.probeAccount(ctx, req)
		if aerr != nil {
			return res, aerr
		}
		res.ProviderAccountID = acc.ID
		c, cerr := r.prepareCredentials(ctx, acc)
		if cerr != nil {
			return res, fmt.Errorf("prepare credentials for account %s: %w", acc.ID, cerr)
		}
		creds = c
	}

	probeCtx, cancel := context.WithTimeout(core.WithProxy(ctx, creds), r.timeout)
	defer cancel()

	capability := req.Capability
	if capability == "" {
		capability = "chat_completions"
		res.Capability = capability
	}

	start := time.Now()
	var callErr error
	var usage core.Usage

	switch capability {
	case "embeddings":
		if mc, ok := conn.(core.MediaConnector); ok {
			probeReq := &core.EmbeddingRequest{
				Model: req.Model,
				Input: []string{"health check"},
			}
			var resp *core.EmbeddingResponse
			resp, callErr = mc.Embeddings(probeCtx, probeReq, creds)
			if resp != nil {
				usage = resp.Usage
			}
		} else {
			callErr = fmt.Errorf("provider %s does not support embeddings", req.Provider)
		}
	default: // chat_completions
		maxTok := 8
		probeReq := &core.ChatRequest{
			Model: req.Model,
			Messages: []core.Message{{
				Role: core.RoleUser,
				Content: []core.ContentPart{{Type: core.PartText, Text: "Reply with OK only."}},
			}},
			MaxTokens: &maxTok,
		}
		var ttft time.Duration
		streamCfg := core.StreamConfig{
			OnFirstChunk: func(elapsed time.Duration) { ttft = elapsed },
		}
		stream, serr := conn.Stream(probeCtx, probeReq, creds, streamCfg)
		if serr == nil {
			sresp, drainErr := drainProbeStream(stream)
			if drainErr != nil {
				callErr = drainErr
			} else if sresp != nil {
				usage = sresp.Usage
			}
			if ttft > 0 {
				ms := int(ttft.Milliseconds())
				res.TTFTMs = &ms
			}
		} else {
			// Fall back to unary for connectors that reject streaming.
			uresp, cerr := conn.Chat(probeCtx, probeReq, creds)
			callErr = cerr
			if uresp != nil {
				usage = uresp.Usage
			}
		}
	}
	latency := int(time.Since(start).Milliseconds())
	res.LatencyMs = &latency

	if callErr != nil {
		pe := core.AsProviderError(callErr)
		et := string(ClassifyError(pe))
		msg := pe.Message
		if msg == "" {
			msg = callErr.Error()
		}
		res.Status = "failed"
		res.ErrorType = &et
		res.ErrorMessage = &msg
		if pe.StatusCode > 0 {
			sc := pe.StatusCode
			res.HTTPStatus = &sc
		}
	} else {
		res.Status = "success"
		hs := 200
		res.HTTPStatus = &hs
	}
	if usage.PromptTokens > 0 {
		pt := usage.PromptTokens
		res.PromptTokens = &pt
	}
	if usage.CompletionTokens > 0 {
		ct := usage.CompletionTokens
		res.CompletionTokens = &ct
	}

	if err := r.repo.InsertProbeResult(ctx, toStored(res, req.TriggeredBy)); err != nil {
		r.log.Warn("probe result insert failed", "err", err, "provider", req.Provider, "model", req.Model)
	}
	r.applyProbeToCurrent(ctx, res, capability)
	return res, nil
}

// probeAccount returns the account a probe authenticates with. Without an
// explicit account it picks the highest-priority enabled account for the
// provider, so provider-level probes (the health page) still carry the API key
// and URL metadata such as Cloudflare's {accountId}.
func (r *ProbeRunner) probeAccount(ctx context.Context, req ProbeRequest) (store.Account, error) {
	if req.ProviderAccountID != "" {
		acc, err := r.accounts.Get(ctx, req.ProviderAccountID)
		if err != nil {
			return store.Account{}, fmt.Errorf("account not found: %s", req.ProviderAccountID)
		}
		if acc.Provider != req.Provider {
			return store.Account{}, fmt.Errorf("account %s belongs to provider %s, not %s", acc.ID, acc.Provider, req.Provider)
		}
		if acc.Disabled {
			return store.Account{}, fmt.Errorf("account %s is disabled", acc.ID)
		}
		return acc, nil
	}
	accs, err := r.accounts.ListByProvider(ctx, store.DefaultTenantID, req.Provider)
	if err != nil {
		return store.Account{}, fmt.Errorf("list accounts for %s: %w", req.Provider, err)
	}
	for _, acc := range accs {
		if !acc.NeedsReconnect {
			return acc, nil
		}
	}
	return store.Account{}, fmt.Errorf("no usable account configured for provider %s", req.Provider)
}

func (r *ProbeRunner) prepareCredentials(ctx context.Context, acc store.Account) (core.Credentials, error) {
	if r.preparer != nil {
		_, creds, err := r.preparer.PrepareCredentials(ctx, acc)
		return creds, err
	}
	if r.vault == nil {
		return core.Credentials{}, nil
	}
	return r.vault.Open(acc)
}

// applyProbeToCurrent updates provider_health_current from a probe outcome.
// If the row already has traffic (request_count > 0), only last_probe_at is
// refreshed so traffic-derived metrics are not clobbered. Otherwise the probe
// verdict sets score/status directly.
func (r *ProbeRunner) applyProbeToCurrent(ctx context.Context, res ProbeResult, capability string) {
	existing, gerr := r.repo.GetCurrent(ctx, res.Provider, res.ProviderAccountID, res.Model, res.Capability)
	now := time.Now()
	if gerr == nil && existing.RequestCount > 0 {
		_ = r.repo.UpdateProbeTimestamp(ctx, res.Provider, res.ProviderAccountID, res.Model, res.Capability, now)
		return
	}

	var score int
	var status string
	var mainIssue, recommendation string
	if res.Status == "success" {
		score = 100
		status = StatusHealthy
	} else {
		et := ProviderErrorUnknown
		if res.ErrorType != nil {
			et = ProviderErrorType(*res.ErrorType)
		}
		threshold := r.thresholds.ThresholdFor(capability)
		p95 := 0
		if res.LatencyMs != nil {
			p95 = *res.LatencyMs
		}
		score = ComputeScore(ScoreInput{
			SuccessRate:         0,
			P95LatencyMs:        p95,
			LatencyThresholdMs:  threshold,
			DominantErrorType:   et,
			ConsecutiveFailures: 1,
		})
		status = StatusFromScore(score, true, false)
		mainIssue = MainIssue(et, p95, threshold, 0)
		recommendation = RecommendationForIssue(mainIssue)
	}

	cur := store.ProviderHealthCurrent{
		ID:                uuid.NewString(),
		Provider:          res.Provider,
		ProviderAccountID: res.ProviderAccountID,
		Model:             res.Model,
		Capability:        res.Capability,
		HealthStatus:      status,
		HealthScore:       score,
		LastProbeAt:       &now,
		LastUpdatedAt:     now,
	}
	if res.Status == "success" {
		cur.LastSuccessAt = &now
	} else {
		cur.LastFailureAt = &now
	}
	if mainIssue != "" {
		cur.MainIssue = &mainIssue
	}
	if recommendation != "" {
		cur.Recommendation = &recommendation
	}
	if err := r.repo.UpsertCurrent(ctx, cur); err != nil {
		r.log.Warn("probe current upsert failed", "err", err, "provider", res.Provider, "model", res.Model)
	}
}

// drainProbeStream consumes a probe stream channel into a synthetic response,
// merging any usage chunks. A ChunkError is surfaced as an error.
func drainProbeStream(ch <-chan core.StreamChunk) (*core.ChatResponse, error) {
	resp := &core.ChatResponse{}
	for chunk := range ch {
		switch chunk.Type {
		case core.ChunkError:
			if chunk.Err != nil {
				return resp, chunk.Err
			}
		case core.ChunkUsage:
			if chunk.Usage != nil {
				resp.Usage = mergeUsage(resp.Usage, *chunk.Usage)
			}
		case core.ChunkFinish:
			resp.FinishReason = chunk.FinishReason
			if chunk.Usage != nil {
				resp.Usage = mergeUsage(resp.Usage, *chunk.Usage)
			}
		}
	}
	return resp, nil
}

func mergeUsage(a, b core.Usage) core.Usage {
	if b.PromptTokens > 0 {
		a.PromptTokens = b.PromptTokens
	}
	if b.CompletionTokens > 0 {
		a.CompletionTokens = b.CompletionTokens
	}
	if b.CachedTokens > 0 {
		a.CachedTokens = b.CachedTokens
	}
	return a
}

func toStored(res ProbeResult, triggeredBy string) store.ProviderProbeResult {
	return store.ProviderProbeResult{
		ID:                  uuid.NewString(),
		Provider:            res.Provider,
		ProviderAccountID:   res.ProviderAccountID,
		Model:               res.Model,
		Capability:          res.Capability,
		Status:              res.Status,
		HTTPStatus:          res.HTTPStatus,
		LatencyMs:           res.LatencyMs,
		TTFTMs:              res.TTFTMs,
		ErrorType:           res.ErrorType,
		ErrorMessage:        res.ErrorMessage,
		PromptTokens:        res.PromptTokens,
		CompletionTokens:    res.CompletionTokens,
		EstimatedCostMicros: res.CostMicroUSD,
		TriggeredBy:         triggeredBy,
		CreatedAt:           time.Now(),
	}
}
