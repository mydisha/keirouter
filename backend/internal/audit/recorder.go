package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// entryStore is the slice of store.AuditRepo the recorder writes through.
type entryStore interface {
	BatchInsert(ctx context.Context, entries []store.AuditEntry) error
}

// Exporter receives rows after they are committed to the database.
// *OTLPExporter implements it.
type Exporter interface {
	Enqueue(entries []store.AuditEntry)
	Stop(deadline time.Duration)
	Stats() ExporterStats
	Endpoint() string
}

// Config tunes a Recorder.
type Config struct {
	// BufferSize bounds queued events; a full queue drops new events.
	BufferSize int
	// FlushInterval and FlushSize bound how long and how many events wait
	// before a database write.
	FlushInterval time.Duration
	FlushSize     int
	// Key signs the hash chain (see DeriveKey). Empty uses plain SHA-256.
	Key []byte
	// InstanceID names this writer's hash chain and is exported as
	// service.instance.id. Empty generates a random id.
	InstanceID string
	// DefaultTenantID fills events recorded without a tenant.
	DefaultTenantID string
	// Requests, when set, also ships one record per gateway request (see
	// ObserveUsage). Nil exports admin events only.
	Requests UsageExporter
	Logger   *slog.Logger
}

// Recorder queues audit events and writes them in batches from one goroutine.
// Record never blocks, so auditing cannot slow down or fail an admin request;
// the cost is that events are dropped (and counted) when the queue is full.
// All methods are safe on a nil *Recorder, which records nothing.
type Recorder struct {
	store    entryStore
	exporter Exporter
	requests UsageExporter
	log      *slog.Logger
	key      []byte
	chainID  string
	tenantID string

	buf           chan store.AuditEntry
	flushInterval time.Duration
	flushSize     int

	// Chain state, owned by the run goroutine.
	seq      int64
	prevHash string

	written atomic.Int64
	failed  atomic.Int64
	dropped atomic.Int64

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
	flushReq chan chan struct{}
}

// New starts a recorder writing to s and, when exp is non-nil, exporting
// committed rows through it. Call Stop on shutdown to drain the queue.
func New(s entryStore, exp Exporter, cfg Config) *Recorder {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 4096
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 500 * time.Millisecond
	}
	if cfg.FlushSize <= 0 {
		cfg.FlushSize = 200
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID = NewInstanceID()
	}
	r := &Recorder{
		store:         s,
		exporter:      exp,
		requests:      cfg.Requests,
		log:           cfg.Logger,
		key:           cfg.Key,
		chainID:       cfg.InstanceID,
		tenantID:      cfg.DefaultTenantID,
		buf:           make(chan store.AuditEntry, cfg.BufferSize),
		flushInterval: cfg.FlushInterval,
		flushSize:     cfg.FlushSize,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
		flushReq:      make(chan chan struct{}),
	}
	go r.run()
	return r
}

// NewInstanceID returns a random writer id ("ins_" + 16 hex chars).
func NewInstanceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "ins_" + hex.EncodeToString(b[:])
}

// Record queues ev. It never blocks.
func (r *Recorder) Record(ev Event) {
	if r == nil {
		return
	}
	e := r.toEntry(ev)
	select {
	case r.buf <- e:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) toEntry(ev Event) store.AuditEntry {
	now := ev.Time
	if now.IsZero() {
		now = time.Now()
	}
	outcome := ev.Outcome
	if outcome == "" {
		outcome = OutcomeSuccess
	}
	severity := ev.Severity
	if severity == "" {
		severity = severityFor(outcome)
	}
	tenant := ev.TenantID
	if tenant == "" {
		tenant = r.tenantID
	}
	detail := "{}"
	if len(ev.Details) > 0 {
		if b, err := json.Marshal(ev.Details); err == nil {
			detail = string(b)
		}
	}
	return store.AuditEntry{
		ID:           newEntryID(),
		TenantID:     tenant,
		Actor:        ev.ActorID,
		ActorType:    ev.ActorType,
		Action:       ev.Action,
		Target:       ev.ResourceID,
		ResourceType: ev.ResourceType,
		Outcome:      outcome,
		Severity:     severity,
		ClientIP:     ev.ClientIP,
		UserAgent:    truncate(ev.UserAgent, 512),
		HTTPMethod:   ev.Method,
		HTTPRoute:    ev.Route,
		StatusCode:   ev.StatusCode,
		RequestID:    ev.RequestID,
		TraceID:      ev.TraceID,
		SpanID:       ev.SpanID,
		Detail:       detail,
		// Stored with microsecond precision; truncate now so the chain hash
		// matches the row read back.
		CreatedAt: now.UTC().Truncate(time.Microsecond),
	}
}

// Flush writes everything queued so far and waits for it (or ctx).
func (r *Recorder) Flush(ctx context.Context) {
	if r == nil {
		return
	}
	done := make(chan struct{})
	select {
	case r.flushReq <- done:
	case <-r.doneCh:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Stop drains the queue (bounded by deadline), then stops the exporter.
func (r *Recorder) Stop(deadline time.Duration) {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() { close(r.stopCh) })
	select {
	case <-r.doneCh:
	case <-time.After(deadline):
		r.log.Warn("audit recorder drain timed out", "pending", len(r.buf))
	}
	if r.exporter != nil {
		r.exporter.Stop(deadline)
	}
	if r.requests != nil {
		r.requests.Stop(deadline)
	}
}

// ExportsRequests reports whether ObserveUsage ships anything.
func (r *Recorder) ExportsRequests() bool { return r != nil && r.requests != nil }

// ObserveUsage queues one gateway request for OTLP export. It never blocks,
// so it can run on the request path as a meter record observer.
func (r *Recorder) ObserveUsage(rec store.UsageRecord) {
	if r == nil || r.requests == nil {
		return
	}
	r.requests.EnqueueUsage(rec)
}

// ChainID is this writer's hash chain id.
func (r *Recorder) ChainID() string {
	if r == nil {
		return ""
	}
	return r.chainID
}

// Key is the chain signing key, for Verify.
func (r *Recorder) Key() []byte {
	if r == nil {
		return nil
	}
	return r.key
}

// Stats reports recorder counters.
type Stats struct {
	Enabled bool           `json:"enabled"`
	ChainID string         `json:"chain_id,omitempty"`
	Queued  int            `json:"queued"`
	Written int64          `json:"written"`
	Failed  int64          `json:"failed"`
	Dropped int64          `json:"dropped"`
	OTLP    *ExporterStats `json:"otlp,omitempty"`
	// RequestsOTLP covers the per-request export, when enabled.
	RequestsOTLP *ExporterStats `json:"requests_otlp,omitempty"`
}

// Stats returns a snapshot of the recorder's counters.
func (r *Recorder) Stats() Stats {
	if r == nil {
		return Stats{}
	}
	st := Stats{
		Enabled: true,
		ChainID: r.chainID,
		Queued:  len(r.buf),
		Written: r.written.Load(),
		Failed:  r.failed.Load(),
		Dropped: r.dropped.Load(),
	}
	if r.exporter != nil {
		es := r.exporter.Stats()
		st.OTLP = &es
	}
	if r.requests != nil {
		rs := r.requests.Stats()
		st.RequestsOTLP = &rs
	}
	return st
}

func (r *Recorder) run() {
	defer close(r.doneCh)
	ticker := time.NewTicker(r.flushInterval)
	defer ticker.Stop()
	batch := make([]store.AuditEntry, 0, r.flushSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		r.write(batch)
		batch = make([]store.AuditEntry, 0, r.flushSize)
	}
	drain := func() {
		for {
			select {
			case e := <-r.buf:
				batch = append(batch, e)
				if len(batch) >= r.flushSize {
					flush()
				}
			default:
				return
			}
		}
	}

	for {
		select {
		case <-r.stopCh:
			drain()
			flush()
			return
		case <-ticker.C:
			flush()
		case done := <-r.flushReq:
			drain()
			flush()
			close(done)
		case e := <-r.buf:
			batch = append(batch, e)
			if len(batch) >= r.flushSize {
				flush()
			}
		}
	}
}

// write links batch into the chain and inserts it. The chain only advances
// when the insert commits, so a failed batch leaves no gap behind it.
func (r *Recorder) write(batch []store.AuditEntry) {
	seq, prev := r.seq, r.prevHash
	for i := range batch {
		seq++
		batch[i].ChainID = r.chainID
		batch[i].ChainSeq = seq
		batch[i].PrevHash = prev
		batch[i].Hash = entryHash(r.key, batch[i])
		prev = batch[i].Hash
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.store.BatchInsert(ctx, batch); err != nil {
		r.failed.Add(int64(len(batch)))
		r.log.Warn("audit batch insert failed", "err", err, "count", len(batch))
		return
	}
	r.seq, r.prevHash = seq, prev
	r.written.Add(int64(len(batch)))
	if r.exporter != nil {
		r.exporter.Enqueue(batch)
	}
}

func newEntryID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "au_" + hex.EncodeToString(b[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
