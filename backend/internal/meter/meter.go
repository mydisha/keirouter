// Package meter records per-request usage and computes cost.
//
// Cost is stored canonically in nanodollars with a micro-USD compatibility
// value for budgets. Pricing resolution distinguishes explicit free models from
// missing prices so unknown usage is never silently reported as free.
package meter

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/usagehub"
)

// Price holds per-million-token rates in USD plus immutable provenance.
type Price struct {
	InputPerM       float64
	OutputPerM      float64
	CachedInputPerM float64
	CacheWritePerM  float64
	ReasoningPerM   float64

	LongContextThreshold int
	LongInputPerM        float64
	LongOutputPerM       float64
	LongCachedInputPerM  float64
	LongCacheWritePerM   float64

	Source       string
	SourceURL    string
	Estimated    bool
	ExplicitFree bool
}

// UsageStore is the persistence surface Meter needs. *store.UsageRepo satisfies
// it for both SQLite and Postgres.
type UsageStore interface {
	Record(ctx context.Context, u store.UsageRecord) error
	RecordBatch(ctx context.Context, records []store.UsageRecord) error
}

// Meter records usage rows and computes cost from a pricing table.
type Meter struct {
	usage UsageStore

	pricingMu   sync.RWMutex
	pricing     map[string]Price // provider-level fallback
	modelPrices map[string]Price // provider/model-level

	hub   *usagehub.Hub
	async *AsyncWriter
}

// SetHub installs a usage event hub. When set, the meter publishes an event
// after every successful Record call. This enables SSE-based near-real-time
// dashboard updates.
func (m *Meter) SetHub(h *usagehub.Hub) { m.hub = h }

// New builds a Meter backed by a usage repo and pricing tables.
func New(usage UsageStore, pricing map[string]Price, modelPrices map[string]Price) *Meter {
	if pricing == nil {
		pricing = map[string]Price{}
	}
	if modelPrices == nil {
		modelPrices = map[string]Price{}
	}
	return &Meter{usage: usage, pricing: pricing, modelPrices: modelPrices}
}

// AsyncConfig configures the buffered usage writer.
type AsyncConfig struct {
	Enabled              bool
	BatchSize            int
	FlushInterval        time.Duration
	QueueSize            int
	FullQueuePolicy      string // "sync" or "drop"
	ShutdownFlushTimeout time.Duration
	Logger               *slog.Logger
}

// EnableAsync installs a buffered, batched usage writer. Call StartAsync after
// construction and Close on shutdown.
func (m *Meter) EnableAsync(cfg AsyncConfig) {
	if !cfg.Enabled {
		return
	}
	m.async = NewAsyncWriter(m.usage, cfg)
}

// StartAsync starts the async writer loop when configured.
func (m *Meter) StartAsync(ctx context.Context) {
	if m.async != nil {
		m.async.Start(ctx)
	}
}

// Close drains pending async usage records.
func (m *Meter) Close(timeout time.Duration) {
	if m.async != nil {
		m.async.Close(timeout)
	}
}

// Event captures the terminal accounting facts for one inbound request.
type Event struct {
	RequestID string
	TenantID  string
	ProjectID string
	APIKeyID  string
	ChainID   string

	// Optional per-step price override from the serving chain step. When any
	// rate is > 0 the meter prices from these instead of the catalog.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64

	Provider  string
	Model     string
	AccountID string
	Client    string
	Status    string
	ErrorKind string

	Usage           core.Usage
	UsageSource     string // provider | estimated | cache | none
	CacheHit        bool
	Latency         time.Duration // winning upstream attempt
	EndToEndLatency time.Duration
	TTFT            time.Duration

	// Token-saving analytics.
	SlimStats      *SlimSnapshot
	SlimActive     bool
	CavemanActive  bool
	TerseActive    bool
	HeadroomStats  *HeadroomSnapshot
	PonytailActive bool
}

// SlimSnapshot captures the RTK slimmer's per-request compression results.
type SlimSnapshot struct {
	BytesSaved  int
	TokensSaved int
	Rules       string // comma-separated rule names that fired
}

// HeadroomSnapshot captures the Headroom compressor's per-request input-side
// savings. It is only populated when Headroom achieved real (non-phantom)
// savings; nil indicates no savings should be recorded for this request.
type HeadroomSnapshot struct {
	TokensSaved int
	BytesSaved  int
	Active      bool
}

// resolvePrice is retained for internal compatibility; callers that need
// provenance should use ResolvePrice.
func (m *Meter) resolvePrice(provider, model string) Price {
	return m.ResolvePrice(provider, model).Price
}

// CostMicros returns the compatibility micro-USD total. CostNanos from
// CalculateCost is authoritative for analytics and historical aggregation.
func (m *Meter) CostMicros(provider, model string, u core.Usage, cacheHit bool) int64 {
	return m.CalculateCost(provider, model, u, cacheHit, 0).CostMicros
}

// Record persists a canonical terminal request row and returns its actual cost
// in micro-USD for the existing budget interface.
func (m *Meter) Record(ctx context.Context, ev Event) (int64, error) {
	u := clampUsage(ev.Usage)
	status := ev.Status
	if status == "" {
		if ev.CacheHit {
			status = "cache_hit"
		} else {
			status = "success"
		}
	}
	usageSource := ev.UsageSource
	if usageSource == "" {
		switch {
		case ev.CacheHit:
			usageSource = "cache"
		case u.PromptTokens+u.CompletionTokens > 0:
			usageSource = "provider"
		default:
			usageSource = "none"
		}
	}
	savedTokens := 0
	if ev.SlimStats != nil {
		savedTokens += ev.SlimStats.TokensSaved
	}
	if ev.HeadroomStats != nil {
		savedTokens += ev.HeadroomStats.TokensSaved
	}
	var cost CostBreakdown
	if u.PromptTokens+u.CompletionTokens == 0 && !ev.CacheHit {
		// A terminal request without upstream usage has no applicable price,
		// including a nominally successful response whose provider omitted usage.
		// Do not inflate coverage merely because the target exists in the catalog.
		cost.Pricing = PricingMatch{Status: "none", MatchKind: "none"}
	} else if price, ok := chainPrice(ev); ok {
		cost = calculateCostFromPrice(pricingMatch("chain", price, "chain", false), u, ev.CacheHit, savedTokens)
	} else {
		cost = m.CalculateCost(ev.Provider, ev.Model, u, ev.CacheHit, savedTokens)
	}
	endToEnd := ev.EndToEndLatency
	if endToEnd <= 0 {
		endToEnd = ev.Latency
	}
	recordedAt := time.Now().UTC()
	var pricingAsOf *time.Time
	if cost.Pricing.Status != "missing" && cost.Pricing.Status != "none" {
		pricingAsOf = &recordedAt
	}
	rec := store.UsageRecord{
		ID: uuid.NewString(), RequestID: ev.RequestID,
		TenantID: ev.TenantID, ProjectID: ev.ProjectID, APIKeyID: ev.APIKeyID,
		ChainID:  ev.ChainID,
		Provider: ev.Provider, Model: ev.Model, AccountID: ev.AccountID, Client: ev.Client,
		Status: status, ErrorKind: ev.ErrorKind, UsageSource: usageSource,
		PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
		CachedTokens: u.CachedTokens, CacheWriteTokens: u.CacheWriteTokens,
		ReasoningTokens: u.ReasoningTokens,
		CostMicros:      cost.CostMicros, CostNanos: cost.CostNanos,
		InputCostNanos: cost.InputCostNanos, CachedCostNanos: cost.CachedCostNanos,
		CacheWriteCostNanos: cost.CacheWriteCostNanos, OutputCostNanos: cost.OutputCostNanos,
		ReasoningCostNanos: cost.ReasoningCostNanos, AvoidedCostNanos: cost.AvoidedCostNanos,
		SavedCostNanos: cost.SavedCostNanos,
		PricingStatus:  cost.Pricing.Status, PricingSource: cost.Pricing.Source, PricingKey: cost.Pricing.Key,
		PricingMatchKind: cost.Pricing.MatchKind, PricingSourceURL: cost.Pricing.SourceURL,
		PricingAsOf:   pricingAsOf,
		InputRatePerM: cost.InputRatePerM, CachedRatePerM: cost.CachedRatePerM,
		CacheWriteRatePerM: cost.CacheWriteRatePerM, OutputRatePerM: cost.OutputRatePerM,
		ReasoningRatePerM: cost.ReasoningRatePerM,
		CacheHit:          ev.CacheHit, LatencyMS: int(endToEnd.Milliseconds()),
		UpstreamLatencyMS: int(ev.Latency.Milliseconds()), EndToEndLatencyMS: int(endToEnd.Milliseconds()),
		TTFTMS: int(ev.TTFT.Milliseconds()), CavemanActive: ev.CavemanActive,
		SlimActive: ev.SlimActive, TerseActive: ev.TerseActive, PonytailActive: ev.PonytailActive,
		CreatedAt: recordedAt,
	}
	if ev.SlimStats != nil {
		rec.SlimBytesSaved, rec.SlimTokensSaved, rec.SlimRules = ev.SlimStats.BytesSaved, ev.SlimStats.TokensSaved, ev.SlimStats.Rules
	}
	if ev.HeadroomStats != nil {
		rec.HeadroomTokensSaved, rec.HeadroomBytesSaved, rec.HeadroomActive = ev.HeadroomStats.TokensSaved, ev.HeadroomStats.BytesSaved, ev.HeadroomStats.Active
	}
	if err := m.recordUsage(ctx, rec); err != nil {
		return cost.CostMicros, err
	}
	if m.hub != nil {
		m.hub.Publish(usagehub.Event{Provider: ev.Provider, Model: ev.Model, AccountID: ev.AccountID,
			Tokens: u.PromptTokens + u.CompletionTokens})
	}
	return cost.CostMicros, nil
}

func (m *Meter) recordUsage(ctx context.Context, rec store.UsageRecord) error {
	if m.async != nil {
		return m.async.Record(ctx, rec)
	}
	return m.usage.Record(ctx, rec)
}

// AsyncStats exposes runtime counters for the async usage writer.
type AsyncStats struct {
	Queued       int
	Written      int64
	Failed       int64
	Dropped      int64
	SyncFallback int64
	Flushes      int64
}

// AsyncWriter serializes usage writes through a single buffered queue and
// flushes records in batches. One writer goroutine is especially friendly to
// SQLite while Postgres benefits from fewer round-trips.
type AsyncWriter struct {
	store           UsageStore
	batchSize       int
	flushInterval   time.Duration
	fullQueuePolicy string
	log             *slog.Logger

	ch     chan store.UsageRecord
	once   sync.Once
	done   chan struct{}
	closed atomic.Bool

	written      atomic.Int64
	failed       atomic.Int64
	dropped      atomic.Int64
	syncFallback atomic.Int64
	flushes      atomic.Int64
}

// NewAsyncWriter constructs an AsyncWriter with sane fallbacks for invalid
// config. The caller must call Start.
func NewAsyncWriter(usageStore UsageStore, cfg AsyncConfig) *AsyncWriter {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = time.Second
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 10000
	}
	if cfg.FullQueuePolicy == "" {
		cfg.FullQueuePolicy = "sync"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AsyncWriter{
		store:           usageStore,
		batchSize:       cfg.BatchSize,
		flushInterval:   cfg.FlushInterval,
		fullQueuePolicy: cfg.FullQueuePolicy,
		log:             cfg.Logger,
		ch:              make(chan store.UsageRecord, cfg.QueueSize),
		done:            make(chan struct{}),
	}
}

// Start launches the flush loop once.
func (w *AsyncWriter) Start(ctx context.Context) {
	w.once.Do(func() {
		go w.run(ctx)
	})
}

// Record enqueues a usage record. If the queue is full, it either performs a
// synchronous fallback write or drops the record based on config.
func (w *AsyncWriter) Record(ctx context.Context, rec store.UsageRecord) error {
	if w.closed.Load() {
		w.syncFallback.Add(1)
		return w.store.Record(ctx, rec)
	}
	select {
	case w.ch <- rec:
		return nil
	default:
		if w.fullQueuePolicy == "drop" {
			w.dropped.Add(1)
			return nil
		}
		w.syncFallback.Add(1)
		return w.store.Record(ctx, rec)
	}
}

// Close stops accepting new async work and waits for the loop to drain.
func (w *AsyncWriter) Close(timeout time.Duration) {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	close(w.ch)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
	case <-timer.C:
		w.log.Warn("usage async writer shutdown timed out", "queued", len(w.ch))
	}
}

// Stats returns current writer counters.
func (w *AsyncWriter) Stats() AsyncStats {
	return AsyncStats{
		Queued:       len(w.ch),
		Written:      w.written.Load(),
		Failed:       w.failed.Load(),
		Dropped:      w.dropped.Load(),
		SyncFallback: w.syncFallback.Load(),
		Flushes:      w.flushes.Load(),
	}
}

func (w *AsyncWriter) run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	batch := make([]store.UsageRecord, 0, w.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.store.RecordBatch(context.Background(), batch); err != nil {
			w.failed.Add(int64(len(batch)))
			w.log.Warn("usage batch write failed; retrying records individually", "count", len(batch), "err", err)
			for _, rec := range batch {
				if err := w.store.Record(context.Background(), rec); err != nil {
					w.failed.Add(1)
					w.log.Warn("usage sync retry failed", "err", err)
				} else {
					w.written.Add(1)
				}
			}
		} else {
			w.written.Add(int64(len(batch)))
			w.flushes.Add(1)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case rec, ok := <-w.ch:
					if !ok {
						flush()
						return
					}
					batch = append(batch, rec)
					if len(batch) >= w.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case rec, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, rec)
			if len(batch) >= w.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// PricingFromCatalog builds a provider-level pricing table from provider specs.
func PricingFromCatalog(specs []SpecPrice) map[string]Price {
	out := make(map[string]Price, len(specs))
	for _, s := range specs {
		out[s.ID] = Price{InputPerM: s.InputPerM, OutputPerM: s.OutputPerM}
	}
	return out
}

// SpecPrice is the minimal pricing projection of a provider spec.
type SpecPrice struct {
	ID         string
	InputPerM  float64
	OutputPerM float64
}
