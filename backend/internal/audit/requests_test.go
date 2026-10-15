package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// captureCollector records every log record the exporter posts, by scope.
type captureCollector struct {
	mu      sync.Mutex
	records map[string][]map[string]any
}

func newCaptureCollector(t *testing.T) (*captureCollector, *httptest.Server) {
	t.Helper()
	c := &captureCollector{records: map[string][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, rl := range body["resourceLogs"].([]any) {
			for _, sl := range rl.(map[string]any)["scopeLogs"].([]any) {
				scope := sl.(map[string]any)["scope"].(map[string]any)["name"].(string)
				for _, lr := range sl.(map[string]any)["logRecords"].([]any) {
					c.records[scope] = append(c.records[scope], lr.(map[string]any))
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return c, srv
}

func (c *captureCollector) scope(name string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.records[name]
}

func recordAttrs(lr map[string]any) map[string]map[string]any {
	attrs := map[string]map[string]any{}
	for _, a := range lr["attributes"].([]any) {
		kv := a.(map[string]any)
		attrs[kv["key"].(string)] = kv["value"].(map[string]any)
	}
	return attrs
}

func TestRequestExportUsesGenAIConventions(t *testing.T) {
	col, srv := newCaptureCollector(t)
	exp := NewOTLPExporter(RequestExporterConfig(OTLPConfig{URL: srv.URL, FlushInterval: 5 * time.Millisecond}))
	exp.EnqueueUsage(store.UsageRecord{
		ID: "u_1", RequestID: "req_1", TenantID: "default", APIKeyID: "key_9", Client: "claude-code",
		Provider: "openai", Model: "gpt-4o", AccountID: "acc_1", ChainID: "coding", FallbackCount: 1,
		Status: "blocked", PromptTokens: 1200, CompletionTokens: 300, CachedTokens: 100,
		CostNanos: 12_300_000, EndToEndLatencyMS: 820, TTFTMS: 210,
		GuardrailAction: "block", GuardrailDetector: "pii", GuardrailDirection: "outbound", GuardrailMidStream: true,
		CreatedAt: time.Unix(1700000000, 0).UTC(),
	})
	require.Eventually(t, func() bool { return exp.Stats().Exported == 1 }, 2*time.Second, 5*time.Millisecond)
	exp.Stop(time.Second)

	recs := col.scope(RequestScopeName)
	require.Len(t, recs, 1)
	lr := recs[0]
	require.Equal(t, RequestEventName, lr["eventName"])
	require.Equal(t, "1700000000000000000", lr["timeUnixNano"])
	require.Equal(t, SeverityWarn, lr["severityText"], "a blocked request is a warning")
	require.Equal(t, "openai/gpt-4o blocked 1200 in 300 out 820ms key key_9", lr["body"].(map[string]any)["stringValue"])

	attrs := recordAttrs(lr)
	require.Equal(t, "openai", attrs["gen_ai.provider.name"]["stringValue"])
	require.Equal(t, "gpt-4o", attrs["gen_ai.response.model"]["stringValue"])
	require.Equal(t, "1200", attrs["gen_ai.usage.input_tokens"]["intValue"])
	require.Equal(t, "300", attrs["gen_ai.usage.output_tokens"]["intValue"])
	require.Equal(t, "key_9", attrs["keirouter.api_key.id"]["stringValue"])
	require.Equal(t, "claude-code", attrs["keirouter.client"]["stringValue"])
	require.Equal(t, "1", attrs["keirouter.fallback.count"]["intValue"])
	require.InDelta(t, 0.0123, attrs["keirouter.cost.usd"]["doubleValue"], 1e-9)
	require.Equal(t, "pii", attrs["keirouter.guardrail.detector"]["stringValue"])
	require.Equal(t, true, attrs["keirouter.guardrail.mid_stream"]["boolValue"])
	require.NotContains(t, attrs, "error.type", "empty values are omitted")
}

func TestRecorderShipsRequestsOnTheirOwnScope(t *testing.T) {
	col, srv := newCaptureCollector(t)
	base := OTLPConfig{URL: srv.URL, FlushInterval: 5 * time.Millisecond}
	rec := New(&memStore{}, NewOTLPExporter(base), Config{Requests: NewOTLPExporter(RequestExporterConfig(base))})
	require.True(t, rec.ExportsRequests())

	rec.Record(Event{Action: "keys.create"})
	rec.ObserveUsage(store.UsageRecord{ID: "u_1", Provider: "openai", Model: "gpt-4o", Status: "failed", ErrorKind: "rate_limit", CreatedAt: time.Now()})
	rec.Stop(2 * time.Second)

	require.Len(t, col.scope(scopeName), 1, "admin events keep the audit scope")
	reqs := col.scope(RequestScopeName)
	require.Len(t, reqs, 1)
	require.Equal(t, SeverityError, reqs[0]["severityText"])
	require.Equal(t, "rate_limit", recordAttrs(reqs[0])["error.type"]["stringValue"])

	st := rec.Stats()
	require.NotNil(t, st.RequestsOTLP)
	require.EqualValues(t, 1, st.RequestsOTLP.Exported)
}

func TestRecorderWithoutRequestExportIgnoresUsage(t *testing.T) {
	rec := New(&memStore{}, nil, Config{})
	defer rec.Stop(time.Second)
	require.False(t, rec.ExportsRequests())
	rec.ObserveUsage(store.UsageRecord{ID: "u_1"})
	require.Nil(t, rec.Stats().RequestsOTLP)

	var nilRec *Recorder
	require.False(t, nilRec.ExportsRequests())
	nilRec.ObserveUsage(store.UsageRecord{ID: "u_1"})
}
