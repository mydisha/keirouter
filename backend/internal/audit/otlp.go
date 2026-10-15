package audit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// scopeName is the instrumentation scope of exported audit records.
const scopeName = "github.com/mydisha/keirouter/audit"

// EventNamePrefix prefixes every exported event.name.
const EventNamePrefix = "keirouter.audit."

// OTLPConfig configures an OTLPExporter. Build it with ResolveOTLPConfig.
type OTLPConfig struct {
	// URL is the full logs endpoint, e.g. http://collector:4318/v1/logs.
	URL           string
	Headers       map[string]string
	Gzip          bool
	Timeout       time.Duration
	BatchSize     int
	FlushInterval time.Duration
	MaxRetries    int
	QueueSize     int
	// Resource holds the resource attributes (service.name, ...).
	Resource map[string]string
	// Scope is the instrumentation scope name. Empty means the audit scope.
	Scope string
	// ScopeVersion is reported as the instrumentation scope version.
	ScopeVersion string
	Client       *http.Client
	Logger       *slog.Logger
}

// ResolveOTLPConfig merges the audit.otlp config with the standard
// OTEL_EXPORTER_OTLP_* / OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES
// variables. ok is false when no endpoint is configured; err explains a
// configuration that cannot work (e.g. an env-selected gRPC protocol).
func ResolveOTLPConfig(c config.AuditOTLPConfig, version, instanceID string, getenv func(string) string) (cfg OTLPConfig, ok bool, err error) {
	endpoint := strings.TrimSpace(c.Endpoint)
	switch {
	case endpoint != "":
		endpoint = logsURL(endpoint)
	case getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT") != "":
		// The signal-specific variable is used as-is, per the OTel spec.
		endpoint = strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"))
	case getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "":
		endpoint = logsURL(getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	default:
		return cfg, false, nil
	}
	if c.Endpoint == "" {
		proto := getenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL")
		if proto == "" {
			proto = getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
		}
		if proto == "grpc" {
			return cfg, false, errors.New("audit: OTLP export supports http/json and http/protobuf endpoints only; " +
				"OTEL_EXPORTER_OTLP_PROTOCOL=grpc is set — point audit.otlp.endpoint at the collector's HTTP port (4318)")
		}
	}
	u, perr := url.Parse(endpoint)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return cfg, false, fmt.Errorf("audit: invalid OTLP endpoint %q", endpoint)
	}

	headers := parseKV(getenv("OTEL_EXPORTER_OTLP_HEADERS"))
	for k, v := range parseKV(getenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS")) {
		headers[k] = v
	}
	for k, v := range c.Headers {
		headers[k] = v
	}

	resource := parseKV(getenv("OTEL_RESOURCE_ATTRIBUTES"))
	for k, v := range c.ResourceAttributes {
		resource[k] = v
	}
	switch {
	case c.ServiceName != "":
		resource["service.name"] = c.ServiceName
	case getenv("OTEL_SERVICE_NAME") != "":
		resource["service.name"] = getenv("OTEL_SERVICE_NAME")
	case resource["service.name"] == "":
		resource["service.name"] = "keirouter"
	}
	if version != "" && resource["service.version"] == "" {
		resource["service.version"] = version
	}
	if instanceID != "" && resource["service.instance.id"] == "" {
		resource["service.instance.id"] = instanceID
	}

	return OTLPConfig{
		URL:           endpoint,
		Headers:       headers,
		Gzip:          c.Compression != "none",
		Timeout:       c.Timeout,
		BatchSize:     c.BatchSize,
		FlushInterval: c.FlushInterval,
		MaxRetries:    c.MaxRetries,
		Resource:      resource,
		ScopeVersion:  version,
	}, true, nil
}

// logsURL appends the OTLP logs path to a collector base URL.
func logsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1/logs") {
		return base
	}
	return base + "/v1/logs"
}

// parseKV parses the OTel "k1=v1,k2=v2" list format (values URL-encoded).
func parseKV(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if dec, err := url.QueryUnescape(strings.TrimSpace(v)); err == nil {
			v = dec
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// ExporterStats reports exporter counters.
type ExporterStats struct {
	Endpoint string `json:"endpoint"`
	Queued   int    `json:"queued"`
	Exported int64  `json:"exported"`
	// Failed counts records dropped after the retries ran out.
	Failed int64 `json:"failed"`
	// Dropped counts records rejected by a full queue.
	Dropped   int64     `json:"dropped"`
	LastError string    `json:"last_error,omitempty"`
	LastOK    time.Time `json:"last_ok,omitempty"`
}

// OTLPExporter ships records to an OTLP/HTTP logs endpoint as JSON: audit rows
// (Enqueue) or per-request usage rows (EnqueueUsage). It has its own queue, so
// a slow or unreachable collector never delays database writes or requests;
// when the queue is full, records are dropped and counted.
type OTLPExporter struct {
	cfg      OTLPConfig
	log      *slog.Logger
	client   *http.Client
	resource otlpResource
	scope    otlpScope

	queue chan otlpItem

	exported atomic.Int64
	failed   atomic.Int64
	dropped  atomic.Int64
	mu       sync.Mutex
	lastErr  string
	lastOK   time.Time

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
	// sleep is swapped in tests to skip retry backoff.
	sleep func(context.Context, time.Duration) bool
}

// NewOTLPExporter starts an exporter. Call Stop to flush on shutdown.
func NewOTLPExporter(cfg OTLPConfig) *OTLPExporter {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 256
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 2 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 8192
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Scope == "" {
		cfg.Scope = scopeName
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	e := &OTLPExporter{
		cfg:      cfg,
		log:      cfg.Logger,
		client:   client,
		resource: otlpResource{Attributes: stringAttrs(cfg.Resource)},
		scope:    otlpScope{Name: cfg.Scope, Version: cfg.ScopeVersion},
		queue:    make(chan otlpItem, cfg.QueueSize),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		sleep:    sleepCtx,
	}
	go e.run()
	return e
}

// Endpoint is the logs URL records are sent to.
func (e *OTLPExporter) Endpoint() string { return e.cfg.URL }

// otlpItem is a queued record, mapped to its log record on the export
// goroutine so callers only pay for a channel send.
type otlpItem interface {
	logRecord(observedUnixNano string) otlpLogRecord
}

type auditItem struct{ store.AuditEntry }

func (it auditItem) logRecord(observed string) otlpLogRecord {
	return logRecord(it.AuditEntry, observed)
}

// Enqueue queues committed rows for export without blocking.
func (e *OTLPExporter) Enqueue(entries []store.AuditEntry) {
	for _, en := range entries {
		e.enqueue(auditItem{en})
	}
}

func (e *OTLPExporter) enqueue(it otlpItem) {
	select {
	case e.queue <- it:
	default:
		e.dropped.Add(1)
	}
}

// Stop flushes the queue, bounded by deadline.
func (e *OTLPExporter) Stop(deadline time.Duration) {
	e.stopOnce.Do(func() { close(e.stopCh) })
	select {
	case <-e.doneCh:
	case <-time.After(deadline):
		e.log.Warn("OTLP exporter drain timed out", "scope", e.scope.Name, "pending", len(e.queue))
	}
}

// Stats returns a snapshot of the exporter's counters.
func (e *OTLPExporter) Stats() ExporterStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return ExporterStats{
		Endpoint:  e.cfg.URL,
		Queued:    len(e.queue),
		Exported:  e.exported.Load(),
		Failed:    e.failed.Load(),
		Dropped:   e.dropped.Load(),
		LastError: e.lastErr,
		LastOK:    e.lastOK,
	}
}

func (e *OTLPExporter) run() {
	defer close(e.doneCh)
	ticker := time.NewTicker(e.cfg.FlushInterval)
	defer ticker.Stop()
	batch := make([]otlpItem, 0, e.cfg.BatchSize)
	// ctx is cancelled on stop so a retry backoff does not hold shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		e.export(ctx, batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-e.stopCh:
			// Final drain: with ctx cancelled, export makes one attempt per
			// batch and skips the backoff.
			cancel()
			for {
				select {
				case en := <-e.queue:
					batch = append(batch, en)
					if len(batch) >= e.cfg.BatchSize {
						e.export(ctx, batch)
						batch = batch[:0]
					}
				default:
					if len(batch) > 0 {
						e.export(ctx, batch)
					}
					return
				}
			}
		case <-ticker.C:
			flush()
		case en := <-e.queue:
			batch = append(batch, en)
			if len(batch) >= e.cfg.BatchSize {
				flush()
			}
		}
	}
}

// export sends one batch, retrying transient failures with exponential
// backoff (honouring Retry-After) while ctx is live.
func (e *OTLPExporter) export(ctx context.Context, batch []otlpItem) {
	body, err := e.encode(batch)
	if err != nil {
		e.fail(len(batch), err)
		return
	}
	backoff := 500 * time.Millisecond
	for attempt := 0; ; attempt++ {
		retryAfter, err := e.post(body)
		if err == nil {
			e.exported.Add(int64(len(batch)))
			e.mu.Lock()
			e.lastErr, e.lastOK = "", time.Now().UTC()
			e.mu.Unlock()
			return
		}
		var perm permanentError
		if errors.As(err, &perm) || attempt >= e.cfg.MaxRetries || ctx.Err() != nil {
			e.fail(len(batch), err)
			return
		}
		wait := backoff + rand.N(backoff/2+1)
		if retryAfter > 0 {
			wait = min(retryAfter, 60*time.Second)
		}
		if !e.sleep(ctx, wait) {
			e.fail(len(batch), err)
			return
		}
		backoff = min(backoff*2, 8*time.Second)
	}
}

func (e *OTLPExporter) fail(n int, err error) {
	e.failed.Add(int64(n))
	e.mu.Lock()
	e.lastErr = err.Error()
	e.mu.Unlock()
	e.log.Warn("OTLP export failed", "scope", e.scope.Name, "records", n, "err", err)
}

type permanentError struct{ error }

// post sends one request. It returns the server's Retry-After on a
// retryable status, and a permanentError for statuses not worth retrying.
func (e *OTLPExporter) post(body []byte) (time.Duration, error) {
	var payload io.Reader = bytes.NewReader(body)
	if e.cfg.Gzip {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(body)
		_ = zw.Close()
		payload = &buf
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.URL, payload)
	if err != nil {
		return 0, permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	if e.cfg.Gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	for k, v := range e.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return 0, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadGateway ||
		resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout:
		return parseRetryAfter(resp.Header.Get("Retry-After")), fmt.Errorf("collector returned %s", resp.Status)
	default:
		return 0, permanentError{fmt.Errorf("collector returned %s", resp.Status)}
	}
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// ---- OTLP/JSON encoding ------------------------------------------------------
//
// The structs below are the subset of the OTLP logs data model KeiRouter
// emits, in the protobuf JSON mapping OTLP/HTTP uses: lowerCamelCase field
// names, 64-bit integers as decimal strings, trace/span ids as hex.

type otlpLogsData struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeLogs struct {
	Scope      otlpScope       `json:"scope"`
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otlpLogRecord struct {
	TimeUnixNano         string         `json:"timeUnixNano"`
	ObservedTimeUnixNano string         `json:"observedTimeUnixNano"`
	SeverityNumber       int            `json:"severityNumber"`
	SeverityText         string         `json:"severityText"`
	EventName            string         `json:"eventName"`
	Body                 otlpAnyValue   `json:"body"`
	Attributes           []otlpKeyValue `json:"attributes"`
	TraceID              string         `json:"traceId,omitempty"`
	SpanID               string         `json:"spanId,omitempty"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

func strVal(s string) otlpAnyValue { return otlpAnyValue{StringValue: &s} }

func boolVal(b bool) otlpAnyValue { return otlpAnyValue{BoolValue: &b} }

func doubleVal(f float64) otlpAnyValue { return otlpAnyValue{DoubleValue: &f} }

func intVal(n int64) otlpAnyValue {
	s := strconv.FormatInt(n, 10)
	return otlpAnyValue{IntValue: &s}
}

func stringAttrs(m map[string]string) []otlpKeyValue {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]otlpKeyValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, otlpKeyValue{Key: k, Value: strVal(m[k])})
	}
	return out
}

func severityNumber(text string) int {
	switch text {
	case SeverityWarn:
		return 13
	case SeverityError:
		return 17
	default:
		return 9
	}
}

func (e *OTLPExporter) encode(batch []otlpItem) ([]byte, error) {
	observed := strconv.FormatInt(time.Now().UnixNano(), 10)
	records := make([]otlpLogRecord, 0, len(batch))
	for _, it := range batch {
		records = append(records, it.logRecord(observed))
	}
	return json.Marshal(otlpLogsData{ResourceLogs: []otlpResourceLogs{{
		Resource:  e.resource,
		ScopeLogs: []otlpScopeLogs{{Scope: e.scope, LogRecords: records}},
	}}})
}

// logRecord maps an audit row onto an OTLP log record. Attribute names use
// OpenTelemetry semantic conventions where one exists (user.id,
// client.address, http.request.method, http.route, ...) and the
// keirouter.audit.* namespace otherwise.
func logRecord(en store.AuditEntry, observedUnixNano string) otlpLogRecord {
	eventName := EventNamePrefix + en.Action
	attrs := make([]otlpKeyValue, 0, 20)
	add := func(k, v string) {
		if v != "" {
			attrs = append(attrs, otlpKeyValue{Key: k, Value: strVal(v)})
		}
	}
	add("event.name", eventName)
	add("log.record.uid", en.ID)
	add("keirouter.audit.action", en.Action)
	add("keirouter.audit.outcome", en.Outcome)
	add("keirouter.tenant.id", en.TenantID)
	add("user.id", en.Actor)
	add("keirouter.audit.actor.type", en.ActorType)
	add("keirouter.audit.resource.type", en.ResourceType)
	add("keirouter.audit.resource.id", en.Target)
	add("client.address", en.ClientIP)
	add("user_agent.original", en.UserAgent)
	add("http.request.method", en.HTTPMethod)
	add("http.route", en.HTTPRoute)
	if en.StatusCode > 0 {
		attrs = append(attrs, otlpKeyValue{Key: "http.response.status_code", Value: intVal(int64(en.StatusCode))})
	}
	add("keirouter.request.id", en.RequestID)
	add("keirouter.audit.chain.id", en.ChainID)
	if en.ChainSeq > 0 {
		attrs = append(attrs, otlpKeyValue{Key: "keirouter.audit.chain.seq", Value: intVal(en.ChainSeq)})
	}
	add("keirouter.audit.hash", en.Hash)
	if en.Detail != "" && en.Detail != "{}" {
		var details map[string]string
		if json.Unmarshal([]byte(en.Detail), &details) == nil {
			for _, kv := range stringAttrs(details) {
				kv.Key = "keirouter.audit.detail." + kv.Key
				attrs = append(attrs, kv)
			}
		}
	}

	rec := otlpLogRecord{
		TimeUnixNano:         strconv.FormatInt(en.CreatedAt.UnixNano(), 10),
		ObservedTimeUnixNano: observedUnixNano,
		SeverityNumber:       severityNumber(en.Severity),
		SeverityText:         en.Severity,
		EventName:            eventName,
		Body:                 strVal(summary(en)),
		Attributes:           attrs,
	}
	if validHexID(en.TraceID, 32) {
		rec.TraceID = en.TraceID
		if validHexID(en.SpanID, 16) {
			rec.SpanID = en.SpanID
		}
	}
	return rec
}

// summary is the human-readable record body, e.g.
// "dashboard session:1a2b keys.delete key_123 -> success".
func summary(en store.AuditEntry) string {
	var b strings.Builder
	if en.ActorType != "" {
		b.WriteString(en.ActorType)
		b.WriteByte(' ')
	}
	if en.Actor != "" {
		b.WriteString(en.Actor)
		b.WriteByte(' ')
	}
	b.WriteString(en.Action)
	if en.Target != "" {
		b.WriteByte(' ')
		b.WriteString(en.Target)
	}
	b.WriteString(" -> ")
	b.WriteString(en.Outcome)
	return b.String()
}
