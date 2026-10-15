package pipeline

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// staticPolicySource serves one global guardrail policy.
type staticPolicySource struct{ policy guardrails.Policy }

func (s staticPolicySource) GetByScope(_ context.Context, tenantID string, scope store.GuardrailScope, scopeID string) (store.GuardrailPolicy, error) {
	if scope != store.GuardrailScopeGlobal {
		return store.GuardrailPolicy{}, store.ErrNotFound
	}
	cfg, _ := guardrails.MarshalPolicy(s.policy)
	return store.GuardrailPolicy{TenantID: tenantID, Scope: scope, ScopeID: scopeID, Enabled: true, Config: cfg}, nil
}

// keywordDetector blocks any prompt or response containing word.
type keywordDetector struct{ word string }

func (keywordDetector) Name() string { return "keyword" }

func (d keywordDetector) Inbound(_ context.Context, in *guardrails.InboundRequest, _ guardrails.Policy) (*guardrails.Decision, error) {
	if strings.Contains(in.FlatText, d.word) {
		return &guardrails.Decision{Action: guardrails.ActionBlock, Reason: "keyword"}, nil
	}
	return nil, nil
}

func (d keywordDetector) Outbound(_ context.Context, out *guardrails.OutboundResponse, _ guardrails.Policy) (*guardrails.Decision, error) {
	if strings.Contains(out.Text, d.word) {
		return &guardrails.Decision{Action: guardrails.ActionBlock, Reason: "keyword"}, nil
	}
	return nil, nil
}

// forbiddenConnector streams enough safe text for the guard to release some
// of it before the forbidden word arrives.
type forbiddenConnector struct{ plannerConnector }

func (forbiddenConnector) Stream(context.Context, *core.ChatRequest, core.Credentials, core.StreamConfig) (<-chan core.StreamChunk, error) {
	return streamOf(
		core.StreamChunk{Type: core.ChunkText, Delta: strings.Repeat("safe text ", 30)},
		core.StreamChunk{Type: core.ChunkText, Delta: "FORBIDDEN"},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
	), nil
}

func newGuardrailPipeline(t *testing.T, conn core.Connector) (*Pipeline, *[]store.UsageRecord) {
	t.Helper()
	// scansOutput needs an output detector enabled in the policy; the
	// keyword detector ignores its config.
	policy := guardrails.Policy{Toxicity: &guardrails.ToxicityConfig{Enabled: true}}
	engine := guardrails.NewEngine(guardrails.EngineConfig{
		Resolver:  guardrails.NewResolver(staticPolicySource{policy: policy}, 0),
		Detectors: []guardrails.Detector{keywordDetector{word: "FORBIDDEN"}},
	})
	m := meter.New(newPipelineTestDB(t).Usage(), nil, nil)
	var recs []store.UsageRecord
	m.SetRecordObserver(func(r store.UsageRecord) { recs = append(recs, r) })
	d := newPlannerDispatcherWithConnector(t, conn, "acc-1")
	return New(Deps{Dispatcher: d, Meter: m, Guardrails: engine}), &recs
}

func guardrailStreamRequest(prompt string) *core.ChatRequest {
	return &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{{
			Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: prompt}},
		}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID, SourceDialect: core.DialectOpenAI},
	}
}

func TestStreamGuardrailBlockAfterOutputIsMidStream(t *testing.T) {
	p, recs := newGuardrailPipeline(t, forbiddenConnector{})
	result, err := p.Stream(context.Background(), guardrailStreamRequest("hello"), Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)
	var sawText, sawBlock bool
	for chunk := range result.Chunks {
		switch chunk.Type {
		case core.ChunkText:
			sawText = true
			require.NotContains(t, chunk.Delta, "FORBIDDEN")
		case core.ChunkError:
			sawBlock = core.AsProviderError(chunk.Err).Kind == core.ErrPolicyBlocked
		}
	}
	require.True(t, sawText, "safe text should have been released before the block")
	require.True(t, sawBlock)

	require.Len(t, *recs, 1)
	rec := (*recs)[0]
	require.Equal(t, "blocked", rec.Status)
	require.Equal(t, "block", rec.GuardrailAction)
	require.Equal(t, "keyword", rec.GuardrailDetector)
	require.Equal(t, "outbound", rec.GuardrailDirection)
	require.True(t, rec.GuardrailMidStream)
}

func TestStreamGuardrailInboundBlockRecordsOutcome(t *testing.T) {
	p, recs := newGuardrailPipeline(t, forbiddenConnector{})
	_, err := p.Stream(context.Background(), guardrailStreamRequest("say FORBIDDEN"), Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.Equal(t, core.ErrPolicyBlocked, core.AsProviderError(err).Kind)

	require.Len(t, *recs, 1)
	rec := (*recs)[0]
	require.Equal(t, "blocked", rec.Status)
	require.Equal(t, "block", rec.GuardrailAction)
	require.Equal(t, "inbound", rec.GuardrailDirection)
	require.False(t, rec.GuardrailMidStream)
}

func TestStreamGuardrailCleanRequestRecordsAllow(t *testing.T) {
	p, recs := newGuardrailPipeline(t, pingingConnector{})
	result, err := p.Stream(context.Background(), guardrailStreamRequest("hello"), Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)
	for range result.Chunks {
	}
	require.Len(t, *recs, 1)
	rec := (*recs)[0]
	require.Equal(t, "success", rec.Status)
	require.Equal(t, "allow", rec.GuardrailAction, "guardrails ran and nothing fired")
	require.Empty(t, rec.GuardrailDetector)
}

// rawForbiddenConnector also offers the zero-copy raw stream, so a same-dialect
// request without tools is eligible for the passthrough fast path.
type rawForbiddenConnector struct{ forbiddenConnector }

func (rawForbiddenConnector) StreamRaw(context.Context, *core.ChatRequest, core.Credentials, core.StreamConfig) (io.ReadCloser, http.Header, error) {
	body := `data: {"choices":[{"index":0,"delta":{"content":"FORBIDDEN"}}]}` + "\n\n" + "data: [DONE]\n\n"
	return io.NopCloser(strings.NewReader(body)), http.Header{"Content-Type": {"text/event-stream"}}, nil
}

func TestStreamGuardrailOutputPolicyDisablesZeroCopy(t *testing.T) {
	p, _ := newGuardrailPipeline(t, rawForbiddenConnector{})
	result, err := p.Stream(context.Background(), guardrailStreamRequest("hello"), Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)
	require.Nil(t, result.DirectBody, "raw passthrough would skip the output guardrails")
	var sawBlock bool
	for chunk := range result.Chunks {
		if chunk.Type == core.ChunkText {
			require.NotContains(t, chunk.Delta, "FORBIDDEN")
		}
		if chunk.Type == core.ChunkError {
			sawBlock = core.AsProviderError(chunk.Err).Kind == core.ErrPolicyBlocked
		}
	}
	require.True(t, sawBlock)
}

func TestStreamWithoutOutputGuardrailsKeepsZeroCopy(t *testing.T) {
	m := meter.New(newPipelineTestDB(t).Usage(), nil, nil)
	d := newPlannerDispatcherWithConnector(t, rawForbiddenConnector{}, "acc-1")
	p := New(Deps{Dispatcher: d, Meter: m})
	result, err := p.Stream(context.Background(), guardrailStreamRequest("hello"), Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.DirectBody, "no output detector is enabled, so the fast path stays on")
	_, _ = io.Copy(io.Discard, result.DirectBody)
	_ = result.DirectBody.Close()
	result.DirectCompleteFunc(nil)
}
