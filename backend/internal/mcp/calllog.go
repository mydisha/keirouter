package mcp

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// Call log statuses.
const (
	StatusSuccess       = "success"
	StatusToolError     = "tool_error"
	StatusDenied        = "denied"
	StatusBlocked       = "blocked"
	StatusRateLimited   = "rate_limited"
	StatusUpstreamError = "upstream_error"
	StatusTimeout       = "timeout"
)

const (
	logQueueSize  = 4096
	logBatchSize  = 200
	logFlushEvery = time.Second
)

// callLogger writes call logs in batches off the request path. A full queue
// drops the entry and counts it rather than slowing tool calls down.
type callLogger struct {
	repo    *store.MCPRepo
	log     *slog.Logger
	queue   chan store.MCPCallLog
	dropped atomic.Int64

	once sync.Once
	done chan struct{}
}

func newCallLogger(repo *store.MCPRepo, log *slog.Logger) *callLogger {
	return &callLogger{repo: repo, log: log, queue: make(chan store.MCPCallLog, logQueueSize), done: make(chan struct{})}
}

func (l *callLogger) record(entry store.MCPCallLog) {
	select {
	case l.queue <- entry:
	default:
		if l.dropped.Add(1)%100 == 1 {
			l.log.Warn("mcp: call log queue full; dropping entries", "dropped", l.dropped.Load())
		}
	}
}

// run writes batches until ctx ends, then drains what is queued.
func (l *callLogger) run(ctx context.Context) {
	defer close(l.done)
	tick := time.NewTicker(logFlushEvery)
	defer tick.Stop()
	batch := make([]store.MCPCallLog, 0, logBatchSize)
	flush := func(c context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := l.repo.InsertCallLogs(c, batch); err != nil {
			l.log.Warn("mcp: write call logs", "rows", len(batch), "err", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case e := <-l.queue:
			batch = append(batch, e)
			if len(batch) >= logBatchSize {
				flush(ctx)
			}
		case <-tick.C:
			flush(ctx)
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for {
				select {
				case e := <-l.queue:
					batch = append(batch, e)
					if len(batch) >= logBatchSize {
						flush(drainCtx)
					}
				default:
					flush(drainCtx)
					return
				}
			}
		}
	}
}
