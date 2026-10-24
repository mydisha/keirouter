package app

import (
	"context"
	"time"

	"github.com/mydisha/keirouter/backend/internal/dokploy"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// autoStopReason marks instances the expiry job stopped, so a later renewal
// can start them again without an admin.
const autoStopReason = "auto-stopped: paid period ended"

// runHostingExpiry enforces the paid period of hosted 9router instances:
//   - active instances past paid_until become expired;
//   - expired instances past the grace period are stopped on Dokploy;
//   - auto-stopped instances that were renewed are started again;
//   - instances stuck in provisioning after a crash become failed;
//   - pending hosting orders past the checkout window become expired.
func (a *App) runHostingExpiry(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		a.hostingExpiryPass(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) hostingExpiryPass(ctx context.Context, now time.Time) {
	repo := a.db.Hosting()
	if n, err := repo.FailStaleProvisioning(ctx, now.Add(-5*time.Minute)); err != nil {
		a.log.Warn("hosting: stale provisioning sweep failed", "err", err)
	} else if n > 0 {
		a.log.Warn("hosting: instances interrupted mid-provisioning marked failed", "count", n)
	}
	if ids, err := a.db.PaymentOrders().ExpireHostingCheckouts(ctx, now.Add(-hosting.CheckoutWindow)); err != nil {
		a.log.Warn("hosting: expire lapsed checkouts failed", "err", err)
	} else if len(ids) > 0 {
		a.log.Info("hosting: lapsed checkouts expired", "count", len(ids))
	}
	if n, err := a.db.HostingCoupons().ReleaseExpired(ctx, "", now); err != nil {
		a.log.Warn("hosting: release lapsed coupon holds failed", "err", err)
	} else if n > 0 {
		a.log.Info("hosting: lapsed coupon holds released", "count", n)
	}
	if n, err := repo.ExpireDue(ctx, now); err != nil {
		a.log.Warn("hosting expiry pass failed", "err", err)
		return
	} else if n > 0 {
		a.log.Info("hosting instances expired", "count", n)
	}

	due, err := repo.ListExpiredBefore(ctx, now.AddDate(0, 0, -hosting.GraceDays))
	if err != nil {
		a.log.Warn("hosting: list instances past grace failed", "err", err)
		return
	}
	stopped, err := repo.ListAll(ctx, store.HostingStopped)
	if err != nil {
		a.log.Warn("hosting: list stopped instances failed", "err", err)
		return
	}
	var renewed []store.HostingInstance
	for _, h := range stopped {
		if h.LastError == autoStopReason && h.PaidUntil.After(now) {
			renewed = append(renewed, h)
		}
	}
	if len(due) == 0 && len(renewed) == 0 {
		return
	}
	st, err := hosting.Load(ctx, a.db.Settings().Get, a.financeBox)
	if err != nil || st.DokployURL == "" || st.APIKey == "" {
		a.log.Warn("hosting: cannot enforce expiry without Dokploy settings", "err", err)
		return
	}
	api := dokploy.NewClient(st.DokployURL, st.APIKey)
	for _, h := range due {
		// Claim the row first: a renewal that lands now moves it out of
		// expired, so the claim fails and the paid instance keeps running.
		// A renewal after the claim extends paid_until on the stopped row,
		// and the restart loop below starts it again.
		moved, err := repo.Transition(ctx, h.ID, []store.HostingStatus{store.HostingExpired}, store.HostingStopped, autoStopReason)
		if err != nil {
			a.log.Warn("hosting: mark auto-stopped failed", "instance", h.ID, "err", err)
			continue
		}
		if !moved || h.DokployAppID == "" {
			continue
		}
		if err := api.Stop(ctx, h.DokployAppID); err != nil {
			a.log.Warn("hosting: auto-stop failed", "instance", h.ID, "err", err)
			if _, rerr := repo.Transition(ctx, h.ID, []store.HostingStatus{store.HostingStopped}, store.HostingExpired, ""); rerr != nil {
				a.log.Warn("hosting: undo auto-stop mark failed", "instance", h.ID, "err", rerr)
			}
		}
	}
	for _, h := range renewed {
		if h.DokployAppID != "" {
			if err := api.Start(ctx, h.DokployAppID); err != nil {
				a.log.Warn("hosting: restart after renewal failed", "instance", h.ID, "err", err)
				continue
			}
		}
		if _, err := repo.Transition(ctx, h.ID, []store.HostingStatus{store.HostingStopped}, store.HostingActive, ""); err != nil {
			a.log.Warn("hosting: mark restarted failed", "instance", h.ID, "err", err)
		}
	}
}
