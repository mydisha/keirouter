package audit

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func TestActionForRoute(t *testing.T) {
	cases := []struct{ method, pattern, action, resource string }{
		{"POST", "/api/keys", "keys.create", "keys"},
		{"PATCH", "/api/keys/{id}", "keys.update", "keys"},
		{"DELETE", "/api/keys/{id}", "keys.delete", "keys"},
		{"POST", "/api/accounts/{id}/test", "accounts.test", "accounts"},
		{"POST", "/api/settings/endpoint", "settings.endpoint", "settings"},
		{"PUT", "/api/models/alias", "models.alias.update", "models"},
		{"DELETE", "/api/models/disabled", "models.disabled.delete", "models"},
		{"GET", "/api/settings/sqlite/backup", "settings.sqlite.backup.read", "settings"},
		{"POST", "/api/auth/password", "auth.password", "auth"},
	}
	for _, c := range cases {
		action, resource := ActionForRoute(c.method, c.pattern)
		require.Equal(t, c.action, action, c.method+" "+c.pattern)
		require.Equal(t, c.resource, resource, c.method+" "+c.pattern)
	}
}

func TestParseTraceparent(t *testing.T) {
	tid, sid := ParseTraceparent("00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01")
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", tid)
	require.Equal(t, "00f067aa0ba902b7", sid)

	for _, bad := range []string{"", "garbage", "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-4bf92f35-00f067aa0ba902b7-01"} {
		tid, sid := ParseTraceparent(bad)
		require.Empty(t, tid, bad)
		require.Empty(t, sid, bad)
	}
}

func TestOutcomeForStatus(t *testing.T) {
	require.Equal(t, OutcomeSuccess, OutcomeForStatus(201))
	require.Equal(t, OutcomeDenied, OutcomeForStatus(401))
	require.Equal(t, OutcomeDenied, OutcomeForStatus(429))
	require.Equal(t, OutcomeFailure, OutcomeForStatus(400))
	require.Equal(t, OutcomeFailure, OutcomeForStatus(500))
}

// memStore is an in-memory entryStore + chainReader.
type memStore struct {
	mu   sync.Mutex
	rows []store.AuditEntry
	fail atomic.Bool
}

func (m *memStore) BatchInsert(_ context.Context, entries []store.AuditEntry) error {
	if m.fail.Load() {
		return io.ErrUnexpectedEOF
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, entries...)
	return nil
}

func (m *memStore) ChainIDs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, r := range m.rows {
		if !seen[r.ChainID] {
			seen[r.ChainID] = true
			out = append(out, r.ChainID)
		}
	}
	return out, nil
}

func (m *memStore) Chain(_ context.Context, chainID string, afterSeq int64, limit int) ([]store.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.AuditEntry
	for _, r := range m.rows {
		if r.ChainID == chainID && r.ChainSeq > afterSeq && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestRecorderHashChainDetectsTampering(t *testing.T) {
	ms := &memStore{}
	key := DeriveKey([]byte("master-key-for-test-0123456789ab"))
	rec := New(ms, nil, Config{Key: key, FlushSize: 2, DefaultTenantID: "t1"})
	for _, a := range []string{"keys.create", "keys.update", "chains.create", "keys.delete"} {
		rec.Record(Event{Action: a, ActorType: ActorDashboard, ActorID: "session:1", Details: map[string]string{"fields": "name"}})
	}
	rec.Stop(time.Second)
	require.Len(t, ms.rows, 4)
	require.Equal(t, "t1", ms.rows[0].TenantID)
	require.Equal(t, int64(4), ms.rows[3].ChainSeq)
	require.Equal(t, ms.rows[2].Hash, ms.rows[3].PrevHash)

	rep, err := Verify(context.Background(), ms, key)
	require.NoError(t, err)
	require.True(t, rep.OK, "%+v", rep.Breaks)
	require.Equal(t, 4, rep.Entries)

	// A forged edit without the key is caught.
	ms.rows[1].Outcome = OutcomeFailure
	rep, err = Verify(context.Background(), ms, key)
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.Equal(t, int64(2), rep.Breaks[0].Seq)
	ms.rows[1].Outcome = OutcomeSuccess

	// Deleting a middle row leaves a gap.
	ms.rows = append(ms.rows[:2], ms.rows[3:]...)
	rep, err = Verify(context.Background(), ms, key)
	require.NoError(t, err)
	require.False(t, rep.OK)
	require.Contains(t, rep.Breaks[0].Reason, "sequence gap")

	// Retention trimming the oldest rows is not a break.
	ms.rows = ms.rows[1:]
	rep, err = Verify(context.Background(), ms, key)
	require.NoError(t, err)
	require.Len(t, rep.Breaks, 1, "only the deleted middle row")
}

func TestRecorderFailedBatchDoesNotAdvanceChain(t *testing.T) {
	ms := &memStore{}
	rec := New(ms, nil, Config{})
	ms.fail.Store(true)
	rec.Record(Event{Action: "keys.create"})
	rec.Flush(context.Background())
	ms.fail.Store(false)
	rec.Record(Event{Action: "keys.delete"})
	rec.Stop(time.Second)

	require.Len(t, ms.rows, 1)
	require.Equal(t, int64(1), ms.rows[0].ChainSeq)
	require.Empty(t, ms.rows[0].PrevHash)
	st := rec.Stats()
	require.EqualValues(t, 1, st.Failed)
	require.EqualValues(t, 1, st.Written)
}

func TestRecorderNilSafe(t *testing.T) {
	var rec *Recorder
	rec.Record(Event{Action: "x"})
	rec.Flush(context.Background())
	rec.Stop(time.Millisecond)
	require.False(t, rec.Stats().Enabled)
}

func TestResolveOTLPConfig(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	_, ok, err := ResolveOTLPConfig(config.AuditOTLPConfig{}, "1.0", "ins_1", getenv)
	require.NoError(t, err)
	require.False(t, ok, "no endpoint anywhere => export off")

	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://collector:4318/"
	env["OTEL_EXPORTER_OTLP_HEADERS"] = "x-api-key=abc%3D,tenant=acme"
	env["OTEL_RESOURCE_ATTRIBUTES"] = "deployment.environment=prod,service.name=fromenv"
	cfg, ok, err := ResolveOTLPConfig(config.AuditOTLPConfig{Headers: map[string]string{"tenant": "override"}}, "1.0", "ins_1", getenv)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "http://collector:4318/v1/logs", cfg.URL)
	require.Equal(t, "abc=", cfg.Headers["x-api-key"])
	require.Equal(t, "override", cfg.Headers["tenant"])
	require.Equal(t, "fromenv", cfg.Resource["service.name"])
	require.Equal(t, "prod", cfg.Resource["deployment.environment"])
	require.Equal(t, "ins_1", cfg.Resource["service.instance.id"])
	require.Equal(t, "1.0", cfg.Resource["service.version"])

	env["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"] = "https://logs.example.com/custom/path"
	cfg, _, err = ResolveOTLPConfig(config.AuditOTLPConfig{}, "", "", getenv)
	require.NoError(t, err)
	require.Equal(t, "https://logs.example.com/custom/path", cfg.URL, "signal-specific endpoint is used as-is")

	env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "grpc"
	_, ok, err = ResolveOTLPConfig(config.AuditOTLPConfig{}, "", "", getenv)
	require.Error(t, err)
	require.False(t, ok)

	cfg, ok, err = ResolveOTLPConfig(config.AuditOTLPConfig{Endpoint: "http://otel:4318", ServiceName: "kr-prod"}, "", "", getenv)
	require.NoError(t, err, "an explicit endpoint ignores the env protocol")
	require.True(t, ok)
	require.Equal(t, "http://otel:4318/v1/logs", cfg.URL)
	require.Equal(t, "kr-prod", cfg.Resource["service.name"])
}

func TestOTLPExporterSendsSpecShapedJSON(t *testing.T) {
	var (
		mu      sync.Mutex
		bodies  []map[string]any
		headers []http.Header
		calls   atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First call fails transiently to exercise the retry.
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		require.Equal(t, "/v1/logs", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
		zr, err := gzip.NewReader(r.Body)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.NewDecoder(zr).Decode(&body))
		mu.Lock()
		bodies = append(bodies, body)
		headers = append(headers, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewOTLPExporter(OTLPConfig{
		URL: srv.URL + "/v1/logs", Gzip: true, MaxRetries: 2, FlushInterval: 10 * time.Millisecond,
		Headers:  map[string]string{"Authorization": "Bearer t"},
		Resource: map[string]string{"service.name": "keirouter"},
	})
	var slept []time.Duration
	exp.sleep = func(_ context.Context, d time.Duration) bool { slept = append(slept, d); return true }

	exp.Enqueue([]store.AuditEntry{{
		ID: "au_1", TenantID: "default", Actor: "session:ab", ActorType: ActorDashboard,
		Action: "keys.delete", Target: "key_9", ResourceType: "keys", Outcome: OutcomeSuccess,
		Severity: SeverityInfo, ClientIP: "10.0.0.1", HTTPMethod: "DELETE", HTTPRoute: "/api/keys/{id}",
		StatusCode: 200, TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7",
		Detail: `{"fields":"name"}`, ChainID: "ins_1", ChainSeq: 7, Hash: "h",
		CreatedAt: time.Unix(1700000000, 123000).UTC(),
	}})
	require.Eventually(t, func() bool { return exp.Stats().Exported == 1 }, 2*time.Second, 5*time.Millisecond)
	exp.Stop(time.Second)
	require.Equal(t, []time.Duration{time.Second}, slept, "Retry-After honoured")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "Bearer t", headers[0].Get("Authorization"))
	rl := bodies[0]["resourceLogs"].([]any)[0].(map[string]any)
	resAttrs := rl["resource"].(map[string]any)["attributes"].([]any)
	require.Equal(t, "service.name", resAttrs[0].(map[string]any)["key"])
	sl := rl["scopeLogs"].([]any)[0].(map[string]any)
	require.Equal(t, scopeName, sl["scope"].(map[string]any)["name"])
	lr := sl["logRecords"].([]any)[0].(map[string]any)
	require.Equal(t, "1700000000000123000", lr["timeUnixNano"])
	require.Equal(t, float64(9), lr["severityNumber"])
	require.Equal(t, "keirouter.audit.keys.delete", lr["eventName"])
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", lr["traceId"])
	require.Equal(t, "00f067aa0ba902b7", lr["spanId"])
	require.Equal(t, "dashboard session:ab keys.delete key_9 -> success", lr["body"].(map[string]any)["stringValue"])

	attrs := map[string]map[string]any{}
	for _, a := range lr["attributes"].([]any) {
		kv := a.(map[string]any)
		attrs[kv["key"].(string)] = kv["value"].(map[string]any)
	}
	require.Equal(t, "session:ab", attrs["user.id"]["stringValue"])
	require.Equal(t, "10.0.0.1", attrs["client.address"]["stringValue"])
	require.Equal(t, "/api/keys/{id}", attrs["http.route"]["stringValue"])
	require.Equal(t, "200", attrs["http.response.status_code"]["intValue"])
	require.Equal(t, "7", attrs["keirouter.audit.chain.seq"]["intValue"])
	require.Equal(t, "name", attrs["keirouter.audit.detail.fields"]["stringValue"])
}

func TestOTLPExporterDropsOnPermanentError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	exp := NewOTLPExporter(OTLPConfig{URL: srv.URL, MaxRetries: 5, FlushInterval: 5 * time.Millisecond})
	exp.Enqueue([]store.AuditEntry{{ID: "a", Action: "x", CreatedAt: time.Now()}})
	require.Eventually(t, func() bool { return exp.Stats().Failed == 1 }, 2*time.Second, 5*time.Millisecond)
	exp.Stop(time.Second)
	require.EqualValues(t, 1, calls.Load(), "4xx is not retried")
	require.Contains(t, exp.Stats().LastError, "400")
}

func TestRecorderExportsOnlyCommittedRows(t *testing.T) {
	var got atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body otlpLogsData
		_ = json.NewDecoder(r.Body).Decode(&body)
		got.Add(int32(len(body.ResourceLogs[0].ScopeLogs[0].LogRecords)))
	}))
	defer srv.Close()
	ms := &memStore{}
	exp := NewOTLPExporter(OTLPConfig{URL: srv.URL, FlushInterval: 5 * time.Millisecond})
	rec := New(ms, exp, Config{})
	ms.fail.Store(true)
	rec.Record(Event{Action: "lost"})
	rec.Flush(context.Background())
	ms.fail.Store(false)
	rec.Record(Event{Action: "kept"})
	rec.Record(Event{Action: "kept"})
	rec.Stop(2 * time.Second)
	require.EqualValues(t, 2, got.Load())
}
