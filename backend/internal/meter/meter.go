// Package meter records per-request usage and computes cost.
//
// Cost is stored in micros (millionths of a USD) as an integer to avoid
// floating-point drift in budget accounting. The pricing table maps
// provider/model to its per-million-token rates; unknown models fall back to
// provider-level rates, then to zero (treated as free for display purposes).
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

// Price holds per-million-token rates in USD.
type Price struct {
	InputPerM       float64 // standard input tokens
	OutputPerM      float64 // standard output tokens
	CachedInputPerM float64 // cache-read input tokens (often 50-90% off standard)
	CacheWritePerM  float64 // cache-write input tokens (often 25% above standard)
	ReasoningPerM   float64 // reasoning/extended-thinking output tokens
}

// UsageStore is the persistence surface Meter needs. *store.UsageRepo satisfies
// it for both SQLite and Postgres.
type UsageStore interface {
	Record(ctx context.Context, u store.UsageRecord) error
	RecordBatch(ctx context.Context, records []store.UsageRecord) error
}

// Meter records usage rows and computes cost from a pricing table.
type Meter struct {
	usage       UsageStore
	pricing     map[string]Price // provider-level fallback
	modelPrices map[string]Price // provider/model-level (e.g. "openai/gpt-4o")
	hub         *usagehub.Hub    // notifies subscribers of new usage records
	async       *AsyncWriter
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

// Event captures the facts about one completed (or cached) request.
type Event struct {
	TenantID  string
	ProjectID string
	APIKeyID  string
	Provider  string
	Model     string
	AccountID string
	Client    string // detected calling tool (claude-code, codex, ...) or "unknown"
	Usage     core.Usage
	CacheHit  bool
	Latency   time.Duration
	TTFT      time.Duration // time-to-first-token (0 if not measured)

	// Token-saving analytics.
	SlimStats     *SlimSnapshot // nil when RTK did not fire
	HeadroomStats *HeadroomSnapshot // nil when Headroom did not fire
	CavemanActive bool          // caveman output compression was active
	TerseActive   bool          // terse output compression was active
}

// SlimSnapshot captures the RTK slimmer's per-request compression results.
type SlimSnapshot struct {
	BytesSaved  int
	TokensSaved int
	Rules       string // comma-separated rule names that fired
}

// HeadroomSnapshot captures Headroom's exact per-request compression result.
type HeadroomSnapshot struct {
	TokensBefore     int
	TokensAfter      int
	TokensSaved      int
	CompressionRatio float64
	Transforms       string // comma-separated transform names
	CCRHashes        string // comma-separated CCR hashes
}

// resolvePrice looks up the price for a provider/model pair. It tries
// "provider/model" first, then "provider", then returns a zero price.
func (m *Meter) resolvePrice(provider, model string) Price {
	// Try exact model match first.
	if model != "" {
		if p, ok := m.modelPrices[provider+"/"+model]; ok {
			return p
		}
	}
	// Fall back to provider-level.
	if p, ok := m.pricing[provider]; ok {
		return p
	}
	return Price{}
}

// CostMicros returns the cost of a usage event in micros of USD. Cached
// requests cost nothing (the whole point of the cache). Cached read tokens
// use the discounted CachedInputPerM rate; cache write tokens use the
// (often higher) CacheWritePerM rate. Reasoning tokens use the ReasoningPerM
// rate when set.
func (m *Meter) CostMicros(provider, model string, u core.Usage, cacheHit bool) int64 {
	if cacheHit {
		return 0
	}
	p := m.resolvePrice(provider, model)

	// Standard input tokens = total input minus cache reads and writes.
	standardInput := u.PromptTokens - u.CachedTokens - u.CacheWriteTokens
	if standardInput < 0 {
		standardInput = 0
	}

	// Cost breakdown:
	//   standard input     * InputPerM
	// + cache-read input   * CachedInputPerM  (often 50-90% off InputPerM)
	// + cache-write input  * CacheWritePerM   (often 25% above InputPerM)
	// + reasoning tokens   * ReasoningPerM    (often same as OutputPerM)
	// + completion tokens  * OutputPerM
	inputCost := float64(standardInput) * p.InputPerM
	cachedReadCost := float64(u.CachedTokens) * p.CachedInputPerM
	cacheWriteCost := float64(u.CacheWriteTokens) * p.CacheWritePerM
	reasoningCost := float64(u.ReasoningTokens) * p.ReasoningPerM
	outputCost := float64(u.CompletionTokens) * p.OutputPerM

	return int64(inputCost + cachedReadCost + cacheWriteCost + reasoningCost + outputCost)
}

// Record persists a usage row for an event and returns the computed cost.
func (m *Meter) Record(ctx context.Context, ev Event) (int64, error) {
	cost := m.CostMicros(ev.Provider, ev.Model, ev.Usage, ev.CacheHit)
	rec := store.UsageRecord{
		ID:               uuid.NewString(),
		TenantID:         ev.TenantID,
		ProjectID:        ev.ProjectID,
		APIKeyID:         ev.APIKeyID,
		Provider:         ev.Provider,
		Model:            ev.Model,
		AccountID:        ev.AccountID,
		Client:           ev.Client,
		PromptTokens:     ev.Usage.PromptTokens,
		CompletionTokens: ev.Usage.CompletionTokens,
		CachedTokens:     ev.Usage.CachedTokens,
		CacheWriteTokens: ev.Usage.CacheWriteTokens,
		CostMicros:       cost,
		CacheHit:         ev.CacheHit,
		LatencyMS:        int(ev.Latency.Milliseconds()),
		TTFTMS:           int(ev.TTFT.Milliseconds()),
		CavemanActive:    ev.CavemanActive,
		TerseActive:      ev.TerseActive,
		CreatedAt:        time.Now(),
	}
	if ev.SlimStats != nil {
		rec.SlimBytesSaved = ev.SlimStats.BytesSaved
		rec.SlimTokensSaved = ev.SlimStats.TokensSaved
		rec.SlimRules = ev.SlimStats.Rules
	}
	if ev.HeadroomStats != nil {
		rec.HeadroomActive = true
		rec.HeadroomTokensBefore = ev.HeadroomStats.TokensBefore
		rec.HeadroomTokensAfter = ev.HeadroomStats.TokensAfter
		rec.HeadroomTokensSaved = ev.HeadroomStats.TokensSaved
		rec.HeadroomCompressionRatio = ev.HeadroomStats.CompressionRatio
		rec.HeadroomTransforms = ev.HeadroomStats.Transforms
		rec.HeadroomCCRHashes = ev.HeadroomStats.CCRHashes
	}
	if err := m.recordUsage(ctx, rec); err != nil {
		return cost, err
	}
	// Notify SSE subscribers of the new usage record for near-real-time
	// dashboard updates.
	if m.hub != nil {
		m.hub.Publish(usagehub.Event{
			Provider:  ev.Provider,
			Model:     ev.Model,
			AccountID: ev.AccountID,
			Tokens:    ev.Usage.PromptTokens + ev.Usage.CompletionTokens,
		})
	}
	return cost, nil
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
