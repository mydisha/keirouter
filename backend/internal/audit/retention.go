package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type retentionStore interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Sweeper deletes audit rows older than the retention window once an hour.
// Every replica may run one: the DELETE is idempotent.
type Sweeper struct {
	store     retentionStore
	log       *slog.Logger
	retention time.Duration
	interval  time.Duration
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewSweeper returns nil when retention <= 0 (keep forever); the nil sweeper's
// Start and Stop are no-ops.
func NewSweeper(s retentionStore, log *slog.Logger, retention time.Duration) *Sweeper {
	if retention <= 0 || s == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{store: s, log: log, retention: retention, interval: time.Hour,
		stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

// Start sweeps once now, then hourly.
func (s *Sweeper) Start() {
	if s == nil {
		return
	}
	go func() {
		defer close(s.doneCh)
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			s.sweep()
			select {
			case <-s.stopCh:
				return
			case <-t.C:
			}
		}
	}()
}

// Stop ends the sweeper, waiting up to deadline for an in-flight sweep.
func (s *Sweeper) Stop(deadline time.Duration) {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
	case <-time.After(deadline):
	}
}

func (s *Sweeper) sweep() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	n, err := s.store.DeleteOlderThan(ctx, time.Now().Add(-s.retention))
	if err != nil {
		s.log.Warn("audit retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		s.log.Info("audit retention sweep", "deleted", n)
	}
}
