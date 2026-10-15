package app

import (
	"context"
	"time"

	"github.com/mydisha/keirouter/backend/internal/finance"
)

// runFinanceAlerts posts a Discord message when an active upstream account
// drops below its low-balance threshold, once per dip.
func (a *App) runFinanceAlerts(ctx context.Context) {
	l := &finance.Loader{DB: a.db, Settings: a.db.Settings().Get}
	if a.inflight != nil {
		l.InFlight = a.inflight.Snapshot
	}
	deps := finance.AlertDeps{
		Load: func(ctx context.Context) (finance.AlertSettings, error) {
			return finance.LoadAlertSettings(ctx, a.db.Settings().Get)
		},
		Balances: l.Balances,
		SetFired: a.db.Upstream().SetAlertFiredAt,
		Now:      time.Now,
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := finance.CheckAlerts(ctx, deps); err != nil {
				a.log.Warn("finance alert pass failed", "err", err)
			}
		}
	}
}
