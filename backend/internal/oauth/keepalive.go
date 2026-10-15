package oauth

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

const (
	// DefaultKeepAliveInterval is how often the background refresher checks
	// for near-expiry OAuth tokens.
	DefaultKeepAliveInterval = 5 * time.Minute
	// BackgroundRefreshLead moves refresh work out of the request path. It is
	// deliberately wider than TokenManager's request-time refreshSkew.
	BackgroundRefreshLead = 30 * time.Minute
	// DefaultKeepAliveMaxParallel bounds simultaneous token endpoint calls.
	DefaultKeepAliveMaxParallel = 4
)

// KeepAlive runs a background loop that proactively refreshes near-expiry
// OAuth access tokens. It prevents request-time latency from just-in-time
// refresh and detects expired refresh tokens early so the dashboard can show
// a "Reconnect" prompt.
type KeepAlive struct {
	interval    time.Duration
	tokenMgr    *TokenManager
	accounts    *store.AccountRepo
	tenantID    string
	log         *slog.Logger
	maxParallel int

	// lastFailSig fingerprints the previous pass's failures so an unchanged,
	// persistent failure set is logged once instead of every interval.
	// Only touched from the serial refresh path.
	lastFailSig    string
	failedRepeated bool

	// egress, when set, routes each refresh through the account's proxy
	// pool so background refreshes leave from the same IP as its traffic.
	egress func(ctx context.Context, acc store.Account) (context.Context, error)
}

// SetEgress installs the per-account egress resolver (the dispatcher's
// EgressContext).
func (k *KeepAlive) SetEgress(fn func(ctx context.Context, acc store.Account) (context.Context, error)) {
	k.egress = fn
}

// NewKeepAlive builds a KeepAlive.
func NewKeepAlive(tm *TokenManager, accounts *store.AccountRepo, tenantID string, log *slog.Logger) *KeepAlive {
	return &KeepAlive{
		interval:    DefaultKeepAliveInterval,
		tokenMgr:    tm,
		accounts:    accounts,
		tenantID:    tenantID,
		log:         log,
		maxParallel: DefaultKeepAliveMaxParallel,
	}
}

// SetMaxParallel overrides the background refresh concurrency bound.
func (k *KeepAlive) SetMaxParallel(n int) {
	if n > 0 {
		k.maxParallel = n
	}
}

// SetInterval overrides the default check interval.
func (k *KeepAlive) SetInterval(d time.Duration) {
	if d > 0 {
		k.interval = d
	}
}

// Run starts the keepalive loop. It blocks until ctx is cancelled. Callers
// should launch it as a goroutine tied to the application context.
func (k *KeepAlive) Run(ctx context.Context) {
	k.log.Info("oauth keepalive started", "interval", k.interval)

	// Run once immediately on startup to catch stale tokens early.
	k.refreshAll(ctx)

	ticker := time.NewTicker(k.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			k.log.Info("oauth keepalive stopped")
			return
		case <-ticker.C:
			k.refreshAll(ctx)
		}
	}
}

// refreshAll lists all OAuth accounts for the tenant and refreshes those that
// are near expiry. Failures are logged but do not stop the loop.
func (k *KeepAlive) refreshAll(ctx context.Context) {
	accs, err := k.accounts.ListByTenant(ctx, k.tenantID)
	if err != nil {
		k.log.Error("oauth keepalive: list accounts", "err", err)
		return
	}

	refreshed, skipped, failed, reconnect := k.refreshAccounts(ctx, accs)

	if refreshed > 0 || failed > 0 || reconnect > 0 {
		level := slog.LevelInfo
		if refreshed == 0 && k.failedRepeated {
			level = slog.LevelDebug
		}
		k.log.Log(ctx, level, "oauth keepalive pass complete",
			"refreshed", refreshed,
			"skipped", skipped,
			"failed", failed,
			"needs_reconnect", reconnect,
		)
	}
}

func (k *KeepAlive) refreshAccounts(ctx context.Context, accs []store.Account) (refreshed, skipped, failed, reconnect int) {
	maxParallel := k.maxParallel
	if maxParallel <= 0 {
		maxParallel = DefaultKeepAliveMaxParallel
	}
	sem := make(chan struct{}, maxParallel)
	results := make(chan refreshResult, len(accs))
	var wg sync.WaitGroup

	for _, acc := range accs {
		if acc.AuthKind != store.AuthOAuth {
			skipped++
			continue
		}
		if acc.Disabled {
			skipped++
			continue
		}
		// Already flagged for reconnection; skip until the user re-authenticates.
		if acc.NeedsReconnect {
			reconnect++
			continue
		}
		// Expiry-less imports cannot be proactively scheduled. Their request path
		// can still force a refresh after an upstream auth rejection.
		if acc.TokenExpiresAt == nil || time.Until(*acc.TokenExpiresAt) > BackgroundRefreshLead {
			skipped++
			continue
		}

		acc := acc
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results <- refreshResult{acc: acc, err: ctx.Err()}
				return
			}

			refreshCtx := ctx
			if k.egress != nil {
				ectx, err := k.egress(ctx, acc)
				if err != nil {
					results <- refreshResult{acc: acc, err: err}
					return
				}
				refreshCtx = ectx
			}
			_, err := k.tokenMgr.ForceRefresh(refreshCtx, acc)
			if err == nil {
				k.log.Debug("oauth keepalive: refreshed", "account", acc.ID, "provider", acc.Provider)
			}
			results <- refreshResult{acc: acc, err: err}
		}()
	}
	wg.Wait()
	close(results)
	var fails []refreshResult
	for res := range results {
		if res.err == nil {
			refreshed++
		} else {
			failed++
			fails = append(fails, res)
		}
	}
	k.reportFailures(fails)
	return refreshed, skipped, failed, reconnect
}

type refreshResult struct {
	acc store.Account
	err error
}

// reportFailures logs one line per distinct failure cause instead of one per
// account, so a shared root cause (e.g. a rotated master key) doesn't flood the
// console. A failure set identical to the previous pass is demoted to debug.
func (k *KeepAlive) reportFailures(fails []refreshResult) {
	type group struct {
		cause     string
		accounts  []string
		providers []string
	}
	byCause := map[string]*group{}
	var order []string
	for _, f := range fails {
		cause := failureCause(f.acc, f.err)
		g, ok := byCause[cause]
		if !ok {
			g = &group{cause: cause}
			byCause[cause] = g
			order = append(order, cause)
		}
		g.accounts = append(g.accounts, f.acc.ID)
		if !containsString(g.providers, f.acc.Provider) {
			g.providers = append(g.providers, f.acc.Provider)
		}
	}
	sort.Strings(order)

	var sig strings.Builder
	for _, c := range order {
		g := byCause[c]
		sort.Strings(g.accounts)
		sig.WriteString(c)
		sig.WriteString(strings.Join(g.accounts, ","))
		sig.WriteByte(';')
	}
	k.failedRepeated = len(fails) > 0 && sig.String() == k.lastFailSig
	k.lastFailSig = sig.String()

	level := slog.LevelWarn
	if k.failedRepeated {
		level = slog.LevelDebug
	}
	for _, c := range order {
		g := byCause[c]
		sort.Strings(g.providers)
		attrs := []any{"provider", strings.Join(g.providers, ",")}
		if len(g.accounts) == 1 {
			attrs = append(attrs, "account", g.accounts[0])
		} else {
			attrs = append(attrs, "accounts", len(g.accounts))
		}
		attrs = append(attrs, "err", g.cause)
		k.log.Log(context.Background(), level, "oauth keepalive: refresh failed", attrs...)
	}
}

// failureCause turns a refresh error into an account-independent, readable
// cause so failures sharing a root cause group together.
func failureCause(acc store.Account, err error) string {
	if errors.Is(err, crypto.ErrMalformedCiphertext) {
		return "stored refresh token cannot be decrypted (master key changed?) — reconnect the account"
	}
	msg := strings.ReplaceAll(err.Error(), " for account "+acc.ID, "")
	return strings.ReplaceAll(msg, acc.ID, "<account>")
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
