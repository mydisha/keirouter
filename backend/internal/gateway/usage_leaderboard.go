package gateway

import (
	"context"
	"net/http"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// leaderboardSize is how many keys each leaderboard category returns.
const leaderboardSize = 10

// portalEmailsByKey maps API key ids to the portal user's email. A key with no
// portal binding (operator or Bansos keys) is absent.
func (s *Server) portalEmailsByKey(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.db == nil {
		return out
	}
	users, err := s.db.PortalUsers().List(ctx)
	if err != nil {
		return out
	}
	for _, u := range users {
		out[u.KeyID] = u.Email
	}
	return out
}

// leaderboardEntry is one API key with every ranked metric, so each category
// row can show the other figures as context.
type leaderboardEntry struct {
	KeyID       string  `json:"key_id"`
	KeyName     string  `json:"key_name"`
	Email       string  `json:"email"`
	Requests    int64   `json:"requests"`
	Tokens      int64   `json:"tokens"`
	SpendUSD    float64 `json:"spend_usd"`
	TopupCount  int64   `json:"topup_count"`
	TopupIDR    int64   `json:"topup_idr"`
	TopupUSD    float64 `json:"topup_usd"`
	spendNanos  int64
	topupMicros int64
}

// adminUsageLeaderboard ranks API keys (portal users by email) for the period
// by settled top-ups, billed spend, tokens and requests.
func (s *Server) adminUsageLeaderboard(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	tz := r.URL.Query().Get("tz")
	cacheKey := "leaderboard|" + period + "|" + tz
	if s.cacheHit(w, cacheKey) {
		return
	}
	ctx := r.Context()
	since := sinceForPeriod(period, tz)

	var (
		usage  []store.KeyUsageTotals
		topups []store.KeyPaidTotals
	)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		var err error
		usage, err = s.usage.UsageByKeySince(groupCtx, adminTenant, since)
		return err
	})
	if s.db != nil {
		group.Go(func() error {
			var err error
			topups, err = s.db.PaymentOrders().PaidTotalsByKeySince(groupCtx, since)
			return err
		})
	}
	if err := group.Wait(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	entries := map[string]*leaderboardEntry{}
	entry := func(id string) *leaderboardEntry {
		e, ok := entries[id]
		if !ok {
			e = &leaderboardEntry{KeyID: id}
			entries[id] = e
		}
		return e
	}
	for _, u := range usage {
		e := entry(u.KeyID)
		e.Requests = u.Requests
		e.Tokens = u.PromptTokens + u.CompletionTokens
		e.spendNanos = u.CostNanos
		e.SpendUSD = nanosToUSD(u.CostNanos)
	}
	for _, t := range topups {
		e := entry(t.KeyID)
		e.TopupCount = t.Count
		e.TopupIDR = t.AmountIDR
		e.topupMicros = t.CreditMicros
		e.TopupUSD = float64(t.CreditMicros) / 1_000_000
	}

	emails := s.portalEmailsByKey(ctx)
	names := map[string]string{}
	if keys, err := s.identity.List(ctx, adminTenant); err == nil {
		for _, k := range keys {
			names[k.ID] = k.Name
		}
	}
	all := make([]*leaderboardEntry, 0, len(entries))
	for id, e := range entries {
		e.Email = emails[id]
		e.KeyName = names[id]
		all = append(all, e)
	}

	top := func(metric func(*leaderboardEntry) int64) []leaderboardEntry {
		ranked := make([]*leaderboardEntry, 0, len(all))
		for _, e := range all {
			if metric(e) > 0 {
				ranked = append(ranked, e)
			}
		}
		sort.Slice(ranked, func(i, j int) bool {
			if a, b := metric(ranked[i]), metric(ranked[j]); a != b {
				return a > b
			}
			return ranked[i].KeyID < ranked[j].KeyID
		})
		if len(ranked) > leaderboardSize {
			ranked = ranked[:leaderboardSize]
		}
		out := make([]leaderboardEntry, len(ranked))
		for i, e := range ranked {
			out[i] = *e
		}
		return out
	}

	writeJSONCached(w, s.insightsCache, cacheKey, map[string]any{
		"period":       period,
		"since":        since,
		"generated_at": time.Now().UTC(),
		"topups":       top(func(e *leaderboardEntry) int64 { return e.topupMicros }),
		"spend":        top(func(e *leaderboardEntry) int64 { return e.spendNanos }),
		"tokens":       top(func(e *leaderboardEntry) int64 { return e.Tokens }),
		"requests":     top(func(e *leaderboardEntry) int64 { return e.Requests }),
	})
}
