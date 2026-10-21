package pipeline

import (
	"context"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/limits"
)

// This file extends the pipeline with the non-chat service kinds: embeddings,
// image generation, speech-to-text, text-to-speech, web search, and web fetch.
//
// Each method resolves an account for the requested provider/model via the
// dispatcher (reusing account selection, cooldowns, and credential opening),
// then type-asserts the connector to the matching capability interface and
// invokes it with fallback across the resolved attempts. Budget preflight runs
// the same way as chat; metering of media calls is best-effort (token usage is
// often unavailable for these endpoints).

// MediaOptions carries the resolved routing target for a media request.
type MediaOptions struct {
	// Targets is the ordered fallback chain (provider+model candidates).
	Targets []dispatch.Target
	// TenantID / ProjectID / APIKeyID scope budget and metering.
	TenantID  string
	ProjectID string
	APIKeyID  string
	Limits    limits.EffectiveLimits
	// AccountID pins operations tied to upstream state, such as polling an
	// asynchronous video job, to the credential that created that state.
	AccountID string
}

// mediaAttempts resolves the ordered attempts for a media request and runs the
// budget preflight. Capability checks are skipped (media kinds aren't modeled
// in the chat capability matrix).
func (p *Pipeline) mediaAttempts(ctx context.Context, opts MediaOptions) ([]dispatch.Attempt, error) {
	if len(opts.Targets) == 0 {
		return nil, &core.ProviderError{Kind: core.ErrBadRequest, Message: "no routing targets resolved for model"}
	}
	if p.budget != nil {
		if err := p.budget.CheckOrError(ctx, budgetScope(opts)); err != nil {
			return nil, err
		}
	}
	planOpts := dispatch.PlanOptions{}
	if opts.AccountID != "" {
		planOpts.AllowedAccountIDs = map[string]struct{}{opts.AccountID: {}}
	}
	return p.dispatcher.PlanWith(ctx, opts.TenantID, opts.Targets, core.NewCapabilitySet(), planOpts)
}

func (p *Pipeline) acquireMediaLimit(ctx context.Context, opts MediaOptions, estimatedTokens int64) (limits.ReleaseFunc, error) {
	if p.limiter == nil {
		return func(int64) {}, nil
	}
	provider, model := firstTarget(opts.Targets)
	release, decision, err := p.limiter.Acquire(ctx, limits.Request{
		TenantID:        opts.TenantID,
		ProjectID:       opts.ProjectID,
		APIKeyID:        opts.APIKeyID,
		Provider:        provider,
		Model:           model,
		EstimatedTokens: estimatedTokens,
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

// Embeddings runs an embeddings request with fallback.
func (p *Pipeline) Embeddings(ctx context.Context, req *core.EmbeddingRequest, opts MediaOptions) (*core.EmbeddingResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, estimateEmbeddingTokens(req))
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.MediaConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "embeddings")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.Embeddings(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// GenerateImage runs an image-generation request with fallback.
func (p *Pipeline) GenerateImage(ctx context.Context, req *core.ImageRequest, opts MediaOptions) (*core.ImageResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, 1)
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.ImageConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "image generation")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.GenerateImage(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// Transcribe runs a speech-to-text request with fallback.
func (p *Pipeline) Transcribe(ctx context.Context, req *core.TranscriptionRequest, opts MediaOptions) (*core.TranscriptionResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, 1)
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.TranscriptionConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "transcription")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.Transcribe(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// Synthesize runs a text-to-speech request with fallback.
func (p *Pipeline) Synthesize(ctx context.Context, req *core.SpeechRequest, opts MediaOptions) (*core.SpeechResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, int64((len(req.Input)+3)/4))
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.SpeechConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "speech synthesis")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.Synthesize(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// Search runs a web-search request with fallback.
func (p *Pipeline) Search(ctx context.Context, req *core.SearchRequest, opts MediaOptions) (*core.SearchResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, 1)
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.SearchConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "web search")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.Search(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// Fetch runs a web-fetch request with fallback.
func (p *Pipeline) Fetch(ctx context.Context, req *core.FetchRequest, opts MediaOptions) (*core.FetchResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, 1)
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.FetchConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "web fetch")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.Fetch(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// GenerateVideo submits an asynchronous video-generation job. Unlike other
// media kinds this does NOT fall back across attempts once a request has been
// sent: a network error after submission may mean the job was created upstream,
// so retrying could duplicate it. It only advances past attempts whose provider
// lacks the capability (no request sent yet).
func (p *Pipeline) GenerateVideo(ctx context.Context, req *core.VideoRequest, opts MediaOptions) (*core.VideoResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, 1)
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.VideoConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "video generation")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.GenerateVideo(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			// Record the failure for cooldown accounting but never retry the
			// submission on another attempt — that could create a duplicate job.
			p.noteMediaFailure(ctx, a, callErr)
			return nil, "", callErr
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		resp.AccountID = a.Account.ID
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// PollVideo checks the status of an in-flight video job. Account-bound polling
// is pinned to the credential that submitted the job.
func (p *Pipeline) PollVideo(ctx context.Context, req *core.VideoStatusRequest, opts MediaOptions) (*core.VideoResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, 1)
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		conn, ok := a.Conn.(core.VideoConnector)
		if !ok {
			lastErr = unsupported(a.Target.Provider, "video generation")
			continue
		}
		r := *req
		r.Model = a.Target.Model
		resp, callErr := conn.PollVideo(core.WithProxy(ctx, a.Creds), &r, a.Creds)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		resp.AccountID = a.Account.ID
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

// UnderstandImage runs an image-understanding (image-to-text) request with
// fallback.
func (p *Pipeline) UnderstandImage(ctx context.Context, req *core.ImageUnderstandingRequest, opts MediaOptions) (*core.ImageUnderstandingResponse, string, error) {
	attempts, err := p.mediaAttempts(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	release, err := p.acquireMediaLimit(ctx, opts, 1)
	if err != nil {
		return nil, "", err
	}
	defer release(0)
	var lastErr error
	for _, a := range attempts {
		r := *req
		r.Model = a.Target.Model
		resp, callErr := understandImage(ctx, a, &r)
		if callErr != nil {
			if !p.noteMediaFailure(ctx, a, callErr) {
				return nil, "", callErr
			}
			lastErr = callErr
			continue
		}
		p.dispatcher.NoteSuccess(ctx, a.Target.Provider, a.Account.ID, a.Target.Model)
		return resp, a.Target.Provider, nil
	}
	return nil, "", orInternal(lastErr)
}

func understandImage(ctx context.Context, attempt dispatch.Attempt, req *core.ImageUnderstandingRequest) (*core.ImageUnderstandingResponse, error) {
	if conn, ok := attempt.Conn.(core.ImageUnderstandingConnector); ok {
		return conn.UnderstandImage(core.WithProxy(ctx, attempt.Creds), req, attempt.Creds)
	}

	content := make([]core.ContentPart, 0, len(req.Images)+1)
	if req.Prompt != "" {
		content = append(content, core.ContentPart{Type: core.PartText, Text: req.Prompt})
	}
	for _, image := range req.Images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		content = append(content, core.ContentPart{Type: core.PartImage, Media: mediaPayload(image)})
	}

	var maxTokens *int
	if req.MaxTokens > 0 {
		value := req.MaxTokens
		maxTokens = &value
	}
	chatResp, err := attempt.Conn.Chat(core.WithProxy(ctx, attempt.Creds), &core.ChatRequest{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Messages: []core.Message{{
			Role:    core.RoleUser,
			Content: content,
		}},
	}, attempt.Creds)
	if err != nil {
		return nil, err
	}
	if chatResp == nil {
		return nil, &core.ProviderError{
			Kind: core.ErrUpstream, Provider: attempt.Target.Provider,
			Model: req.Model, Message: "image understanding returned an empty response",
		}
	}
	return &core.ImageUnderstandingResponse{
		Model: req.Model,
		Text:  chatResp.Message.TextContent(),
		Usage: chatResp.Usage,
	}, nil
}

func mediaPayload(value string) *core.MediaPayload {
	if strings.HasPrefix(value, "data:") {
		if metadata, data, ok := strings.Cut(value, ","); ok && strings.Contains(metadata, ";base64") {
			mimeType := strings.TrimPrefix(strings.SplitN(metadata, ";", 2)[0], "data:")
			return &core.MediaPayload{MIMEType: mimeType, Data: data}
		}
	}
	return &core.MediaPayload{URL: value}
}

func estimateEmbeddingTokens(req *core.EmbeddingRequest) int64 {
	if req == nil {
		return 0
	}
	chars := 0
	for _, input := range req.Input {
		chars += len(input)
	}
	return int64((chars + 3) / 4)
}

// noteMediaFailure records a cooldown and reports whether the pipeline should
// keep trying the next attempt (true) or surface the error now (false).
func (p *Pipeline) noteMediaFailure(ctx context.Context, a dispatch.Attempt, err error) bool {
	pe := core.AsProviderError(err)
	if pe.Provider == "" {
		pe.Provider = a.Target.Provider
	}
	if pe.Model == "" {
		pe.Model = a.Target.Model
	}
	pe.MarketRouted = isMarketRouted(a.Target)
	p.dispatcher.NoteFailure(ctx, a.Account.ID, pe)
	if p.metrics != nil {
		p.metrics.RecordUpstreamError(a.Target.Provider, string(pe.Kind))
	}
	if !pe.Fallbackable() {
		return false
	}
	if p.metrics != nil {
		p.metrics.RecordFallback(string(pe.Kind))
	}
	p.log.Warn("media attempt failed, falling back",
		"provider", a.Target.Provider, "model", a.Target.Model, "kind", pe.Kind)
	noteAttemptFailed(ctx, a.Target.Provider, a.Target.Model, pe)
	return true
}

// unsupported builds a bad-request error for a provider that lacks a capability.
func unsupported(provider, capability string) error {
	return &core.ProviderError{Kind: core.ErrBadRequest, Provider: provider,
		Message: "provider " + provider + " does not support " + capability}
}

// orInternal returns err, or a generic internal error when nil.
func orInternal(err error) error {
	if err != nil {
		return err
	}
	return &core.ProviderError{Kind: core.ErrInternal, Message: "pipeline: no attempts executed"}
}

// budgetScope adapts media options to the budget engine's scope type.
func budgetScope(opts MediaOptions) budget.Scope {
	return budget.Scope{TenantID: opts.TenantID, ProjectID: opts.ProjectID, APIKeyID: opts.APIKeyID}
}
