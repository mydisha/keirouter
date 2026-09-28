// Package dispatch selects which provider account serves a request and, on
// failure, advances through fallback candidates.
//
// A Target names a provider+model. The dispatcher resolves the live accounts
// for a target's provider, skips accounts on cooldown or lacking the required
// capabilities, and tries them in priority order. When every account for a
// target is exhausted, it advances to the next target in the chain. Errors that
// are not fallbackable (e.g. a malformed request) short-circuit immediately.
//
// Strategy variants:
//   - fallback (default): try targets sequentially until one succeeds.
//   - round-robin: rotate the starting target on each call so load spreads
//     evenly across models. A "sticky limit" controls how many consecutive
//     requests land on the same target before advancing the cursor.
package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/capability"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/proxy"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// Exponential backoff constants.
const (
	// BackoffBase is the base cooldown duration at backoff level 1.
	BackoffBase = 2 * time.Second
	// BackoffMax caps the maximum cooldown produced by exponential backoff.
	BackoffMax = 5 * time.Minute
	// BackoffMaxLevel is the ceiling for the backoff exponent.
	BackoffMaxLevel = 15
	// TransientCooldown is the default cooldown for transient/upstream errors
	// that have no explicit Retry-After.
	TransientCooldown = 30 * time.Second
	// ModelCooldownMultiplier scales the per-model cooldown relative to the
	// account-level cooldown (same duration — model locks are independent).
	ModelCooldownMultiplier = 1
	// DefaultStickyLimit is the number of consecutive requests served by one
	// target before round-robin advances to the next.
	DefaultStickyLimit = 1
	// ProviderCircuitFailureThreshold opens a provider circuit after this many
	// consecutive network/upstream failures.
	ProviderCircuitFailureThreshold = 3
	// ProviderCircuitBaseCooldown is the first open interval.
	ProviderCircuitBaseCooldown = 5 * time.Second
	// ProviderCircuitMaxCooldown caps repeated provider outages.
	ProviderCircuitMaxCooldown = 2 * time.Minute
	// ProviderCircuitResetWindow forgets isolated failures after a quiet period.
	ProviderCircuitResetWindow = time.Minute
	// FailureDedupWindow collapses concurrent failures on one account into a
	// single cooldown escalation. Without it, N in-flight requests failing on
	// the same credential advance the backoff level N steps at once.
	FailureDedupWindow = 5 * time.Second
	// BackoffJitterFraction is the maximum random extension applied to locally
	// computed cooldowns so accounts that failed together do not all become
	// eligible again at the same instant.
	BackoffJitterFraction = 0.25
	// CreditsExhaustedCooldown parks an account whose paid balance is
	// depleted. A dry balance only recovers when the user tops up or the plan
	// renews, so probing every half hour just burns requests — one automatic
	// retry per day plus manual reset via the dashboard covers recovery.
	CreditsExhaustedCooldown = 24 * time.Hour
)

// maxRecentFailureEntries bounds the failure-dedup map; expired entries are
// swept lazily once the map reaches this size.
const maxRecentFailureEntries = 4096

// Target is one candidate in a fallback chain.
type Target struct {
	Provider string
	Model    string

	// Optional per-step price override carried from the source chain. All zero
	// means the meter must resolve the catalog price instead.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64
}

// Attempt describes a single resolved try: the connector, credentials, and the
// account it came from. The pipeline executes attempts via the connector.
type Attempt struct {
	Target  Target
	Conn    core.Connector
	Creds   core.Credentials
	Account store.Account
}

// Strategy controls how targets within a chain are ordered.
type Strategy string

const (
	// StrategyFallback tries targets in declared order (the default).
	StrategyFallback Strategy = "fallback"
	// StrategyRoundRobin rotates the starting target per call.
	StrategyRoundRobin Strategy = "round-robin"
	// StrategySmartRoundRobin rotates new sessions but keeps an affinity key on
	// the same account so account-local provider context is preserved.
	StrategySmartRoundRobin Strategy = "smart-round-robin"
)

// ConnectorSource resolves a connector by provider id.
type ConnectorSource interface {
	Get(provider string) (core.Connector, error)
}

// TokenRefresher refreshes an account's OAuth access token just-in-time when it
// is expired or about to expire. It is optional; a nil refresher means accounts
// are used as-is. The oauth.TokenManager implements this.
type TokenRefresher interface {
	EnsureFresh(ctx context.Context, acc store.Account) (store.Account, error)
	ForceRefresh(ctx context.Context, acc store.Account) (store.Account, error)
}

// HealthSource reports and updates background account/model health state.
type HealthSource interface {
	IsUnhealthy(ctx context.Context, accountID, model string) (bool, error)
	MarkHealthy(ctx context.Context, accountID, model string) error
	UnhealthyAccounts(ctx context.Context, accountIDs []string, model string) (map[string]bool, error)
}

// RoutingSource provides model-level cooldowns and chain rotation state.
type RoutingSource interface {
	SetModelCooldown(ctx context.Context, accountID, model string, until time.Time) error
	ClearModelCooldown(ctx context.Context, accountID, model string) error
	IsModelCooldownActive(ctx context.Context, accountID, model string) (bool, error)
	ActiveCooldowns(ctx context.Context, accountIDs []string, model string) (map[string]bool, error)
	ActiveCooldownExpirations(ctx context.Context, accountIDs []string, model string) (map[string]time.Time, error)
	GetChainRotationState(ctx context.Context, chainID string) (store.ChainRotation, error)
	SetChainRotationState(ctx context.Context, state store.ChainRotation) error
	GetTargetRotationState(ctx context.Context, scopeKey string) (store.TargetRotation, error)
	SetTargetRotationState(ctx context.Context, state store.TargetRotation) error
	GetAccountAffinity(ctx context.Context, scopeKey string) (store.AccountAffinity, error)
	SetAccountAffinity(ctx context.Context, state store.AccountAffinity) error
}

// GlobalProxyReader provides dynamic global outbound proxy configuration.
// The dispatcher consults this as a fallback when an account's credentials
// carry no per-account proxy (from proxy pool bindings).
type GlobalProxyReader interface {
	ProxyURL() string
	NoProxy() string
}

// Dispatcher walks fallback chains, yielding resolved attempts.
type Dispatcher struct {
	conns       ConnectorSource
	accounts    *store.AccountRepo
	vault       *vault.Vault
	pools       proxy.PoolSource
	refresher   TokenRefresher
	routing     RoutingSource
	health      HealthSource
	proxyReader GlobalProxyReader
	// selectionLocks serializes rotation/affinity selection per provider so
	// concurrent requests do not all observe and choose the same cursor.
	selectionLocks sync.Map
	circuitMu      sync.Mutex
	circuits       map[string]providerCircuit
	// recentFailures tracks the last cooldown escalation per account (or
	// account+model) so a burst of parallel failures counts once.
	failureMu      sync.Mutex
	recentFailures map[string]time.Time
	// defaultCooldown is applied to an account when an error carries no
	// upstream-specified Retry-After.
	defaultCooldown time.Duration
}

type providerCircuit struct {
	Failures    int
	LastFailure time.Time
	OpenUntil   time.Time
}

// New builds a Dispatcher.
func New(conns ConnectorSource, accounts *store.AccountRepo, v *vault.Vault) *Dispatcher {
	return &Dispatcher{
		conns:           conns,
		accounts:        accounts,
		vault:           v,
		defaultCooldown: 60 * time.Second,
		circuits:        make(map[string]providerCircuit),
		recentFailures:  make(map[string]time.Time),
	}
}

// SetTokenRefresher installs an OAuth token refresher, consulted before opening
// each account's credentials.
func (d *Dispatcher) SetTokenRefresher(r TokenRefresher) { d.refresher = r }

// SetPoolSource installs a proxy pool resolver, consulted when an account has a
// proxy_pool_id binding.
func (d *Dispatcher) SetPoolSource(p proxy.PoolSource) { d.pools = p }

// SetRoutingSource installs the model-cooldown and chain-rotation backend.
func (d *Dispatcher) SetRoutingSource(r RoutingSource) { d.routing = r }

// SetHealthSource installs background account/model health state.
func (d *Dispatcher) SetHealthSource(h HealthSource) { d.health = h }

// SetGlobalProxy installs a global outbound proxy reader, consulted as a
// fallback when an account has no per-account proxy pool binding.
func (d *Dispatcher) SetGlobalProxy(r GlobalProxyReader) { d.proxyReader = r }

// PlanOptions carries per-request strategy configuration.
type PlanOptions struct {
	// Strategy is "fallback" (default) or "round-robin".
	Strategy Strategy
	// ChainID is the persisted chain identifier, used by round-robin to
	// store/retrieve the rotation cursor. Empty for inline targets.
	ChainID string
	// StickyLimit is the number of consecutive requests per target before
	// round-robin advances. Zero defaults to DefaultStickyLimit.
	StickyLimit int
	// AccountStrategy controls how accounts inside one provider/model target
	// are ordered. "fallback" keeps priority order; "round-robin" rotates the
	// starting account while preserving cooldown/fallback behavior.
	AccountStrategy Strategy
	// AccountStickyLimit is the number of consecutive requests per account
	// before account round-robin advances. Zero defaults to DefaultStickyLimit.
	AccountStickyLimit int
	// AccountAffinityKey pins smart round-robin requests to an account when
	// the same conversation/session key is seen again.
	AccountAffinityKey string
	// AccountAffinityTTL controls how long a smart round-robin pin lives.
	// Zero defaults to DefaultAffinityTTL.
	AccountAffinityTTL time.Duration
	// ProviderAccountStrategies overrides account routing per provider.
	ProviderAccountStrategies map[string]AccountRoutingOptions
	// ExcludedAccountIDs prevents failed credentials from being selected again
	// while a request dynamically re-plans its next attempt.
	ExcludedAccountIDs map[string]struct{}
	// ExcludedAttempts prevents one provider/model/account combination from
	// being selected twice during dynamic re-planning.
	ExcludedAttempts map[AttemptKey]struct{}
	// AllowedAccountIDs pins account-bound operations to a known credential.
	// Empty means every otherwise eligible account is allowed.
	AllowedAccountIDs map[string]struct{}
}

// AttemptKey uniquely identifies one routed provider/model/account candidate.
type AttemptKey struct {
	Provider  string
	Model     string
	AccountID string
}

// Key returns the stable identity used to exclude an already-tried attempt.
func (a Attempt) Key() AttemptKey {
	return AttemptKey{
		Provider:  a.Target.Provider,
		Model:     a.Target.Model,
		AccountID: a.Account.ID,
	}
}

// AccountRoutingOptions is the provider-scoped subset of PlanOptions used for
// account ordering inside one provider/model target.
type AccountRoutingOptions struct {
	Strategy    Strategy
	StickyLimit int
	AffinityKey string
	AffinityTTL time.Duration
}

// DefaultAffinityTTL keeps context affinity across a typical work session
// without pinning abandoned sessions forever.
const DefaultAffinityTTL = 24 * time.Hour

// Plan resolves the ordered list of attempts for a chain of targets, scoped to
// a tenant and constrained to the given required capabilities. It returns an
// error only when no attempt could be resolved at all (no usable account for
// any target); otherwise the pipeline tries attempts in order.
func (d *Dispatcher) Plan(ctx context.Context, tenantID string, targets []Target, required core.CapabilitySet) ([]Attempt, error) {
	return d.PlanWith(ctx, tenantID, targets, required, PlanOptions{})
}

// PlanWith is like Plan but accepts strategy options.
func (d *Dispatcher) PlanWith(ctx context.Context, tenantID string, targets []Target, required core.CapabilitySet, opts PlanOptions) ([]Attempt, error) {
	unlock := d.lockSelection(targets)
	defer unlock()

	// Apply round-robin rotation if requested.
	ordered := d.applyRotation(ctx, targets, opts)
	hardRequired := capability.NonStrippable(required)

	// Resolve all provider accounts in one store query. A chain commonly has
	// multiple models backed by the same provider, and querying/decrypting per
	// target made planning cost grow with targets × accounts.
	eligible := make([]bool, len(ordered))
	providers := make([]string, 0, len(ordered))
	seenProviders := make(map[string]struct{}, len(ordered))
	for i, target := range ordered {
		eligible[i] = connectors.IsCustomProviderID(target.Provider) ||
			capability.SupportsProvider(target.Provider, target.Model, hardRequired)
		if !eligible[i] {
			continue
		}
		if _, ok := seenProviders[target.Provider]; !ok {
			seenProviders[target.Provider] = struct{}{}
			providers = append(providers, target.Provider)
		}
	}

	providerAccounts, err := d.accounts.ListByProviders(ctx, tenantID, providers)
	if err != nil {
		return nil, fmt.Errorf("dispatch: list provider accounts: %w", err)
	}
	accountsByProvider := make(map[string][]store.Account, len(providers))
	accountIDsByProvider := make(map[string][]string, len(providers))
	for _, acc := range providerAccounts {
		accountsByProvider[acc.Provider] = append(accountsByProvider[acc.Provider], acc)
		accountIDsByProvider[acc.Provider] = append(accountIDsByProvider[acc.Provider], acc.ID)
	}

	// Connector resolution, OAuth refresh, vault decryption, and proxy-pool
	// selection are invariant for an account during one plan. Keep them only
	// for this request so credentials are never cached across requests.
	type connectorResolution struct {
		conn core.Connector
		err  error
	}
	type preparedAccount struct {
		account store.Account
		creds   core.Credentials
		err     error
	}
	connections := make(map[string]connectorResolution, len(providers))
	preparedAccounts := make(map[string]preparedAccount, len(providerAccounts))

	var globalProxyURL, globalNoProxy string
	if d.proxyReader != nil {
		globalProxyURL = d.proxyReader.ProxyURL()
		globalNoProxy = d.proxyReader.NoProxy()
	}

	now := time.Now()
	attempts := make([]Attempt, 0, len(ordered))
	unhealthyAttempts := make([]Attempt, 0, len(ordered))
	var lastReason string
	// nonCooldownReason remembers accounts skipped for actionable reasons
	// (needs-reconnect, token refresh failure). Without it, one account on
	// cooldown makes the final error read as a pure rate limit and masks the
	// real problem on the remaining accounts.
	var nonCooldownReason string
	var earliestRetryAt time.Time
	blockedScope := core.FailureScopeAccount

	for i, target := range ordered {
		// Capability guard: never fall back to a model that cannot honor the
		// request's hard (non-strippable) requirements. Custom providers skip
		// the guard because their upstream capabilities are unknown.
		if !eligible[i] {
			lastReason = fmt.Sprintf("model %q lacks required capabilities", target.Model)
			continue
		}
		if remaining := d.providerCircuitRemaining(target.Provider, now); remaining > 0 {
			retryAt := now.Add(remaining)
			if earliestRetryAt.IsZero() || retryAt.Before(earliestRetryAt) {
				earliestRetryAt = retryAt
				blockedScope = core.FailureScopeProvider
			}
			lastReason = fmt.Sprintf("provider %s temporarily unavailable", target.Provider)
			continue
		}

		resolved, ok := connections[target.Provider]
		if !ok {
			resolved.conn, resolved.err = d.conns.Get(target.Provider)
			connections[target.Provider] = resolved
		}
		if resolved.err != nil {
			lastReason = resolved.err.Error()
			continue
		}

		accs := accountsByProvider[target.Provider]
		if len(accs) == 0 {
			lastReason = fmt.Sprintf("no accounts configured for provider %q", target.Provider)
			continue
		}
		accs = d.applyAccountRouting(ctx, tenantID, target, accs, opts.accountRoutingForTarget(target.Provider))
		accountIDs := accountIDsByProvider[target.Provider]

		var cooldownExpirations map[string]time.Time
		if d.routing != nil && len(accountIDs) > 0 {
			cooldownExpirations, _ = d.routing.ActiveCooldownExpirations(ctx, accountIDs, target.Model)
		}
		var unhealthySet map[string]bool
		if d.health != nil && len(accountIDs) > 0 {
			unhealthySet, _ = d.health.UnhealthyAccounts(ctx, accountIDs, target.Model)
		}

		for _, acc := range accs {
			key := AttemptKey{Provider: target.Provider, Model: target.Model, AccountID: acc.ID}
			if _, excluded := opts.ExcludedAttempts[key]; excluded {
				lastReason = fmt.Sprintf("account %s already attempted for model %s", acc.ID, target.Model)
				continue
			}
			if _, excluded := opts.ExcludedAccountIDs[acc.ID]; excluded {
				lastReason = fmt.Sprintf("account %s already attempted", acc.ID)
				continue
			}
			if len(opts.AllowedAccountIDs) > 0 {
				if _, allowed := opts.AllowedAccountIDs[acc.ID]; !allowed {
					lastReason = fmt.Sprintf("account %s is not selected for this operation", acc.ID)
					continue
				}
			}
			// Account-level cooldown (global cooldown from NoteFailure).
			if acc.CooldownUntil != nil && acc.CooldownUntil.After(now) {
				if earliestRetryAt.IsZero() || acc.CooldownUntil.Before(earliestRetryAt) {
					earliestRetryAt = *acc.CooldownUntil
					blockedScope = core.FailureScopeAccount
				}
				lastReason = fmt.Sprintf("account %s on cooldown", acc.ID)
				continue
			}
			// Skip accounts whose OAuth refresh token was permanently rejected;
			// they need the user to re-authenticate before serving traffic.
			if acc.NeedsReconnect {
				lastReason = fmt.Sprintf("account %s needs reconnection (refresh token revoked)", acc.ID)
				nonCooldownReason = lastReason
				continue
			}
			// Model-level cooldown: skip this account only for this model.
			if until, cooling := cooldownExpirations[acc.ID]; cooling {
				if earliestRetryAt.IsZero() || until.Before(earliestRetryAt) {
					earliestRetryAt = until
					blockedScope = core.FailureScopeModel
				}
				lastReason = fmt.Sprintf("account %s model %s on cooldown", acc.ID, target.Model)
				continue
			}

			isUnhealthy := unhealthySet != nil && unhealthySet[acc.ID]
			prepared, ok := preparedAccounts[acc.ID]
			if !ok {
				prepared.account = acc
				if d.refresher != nil {
					prepared.account, prepared.err = d.refresher.EnsureFresh(ctx, prepared.account)
				}
				if prepared.err == nil {
					prepared.creds, prepared.err = d.vault.Open(prepared.account)
				}
				if prepared.err == nil && d.pools != nil && prepared.account.ProxyPoolID != "" {
					prepared.err = proxy.ResolvePool(ctx, d.pools, prepared.account.ProxyPoolID, &prepared.creds)
				}
				if prepared.err == nil && prepared.creds.ProxyURL == "" && prepared.creds.RelayURL == "" && globalProxyURL != "" {
					prepared.creds.ProxyURL = globalProxyURL
					prepared.creds.NoProxy = globalNoProxy
				}
				preparedAccounts[acc.ID] = prepared
			}
			if prepared.err != nil {
				lastReason = prepared.err.Error()
				nonCooldownReason = fmt.Sprintf("account %s: %v", acc.ID, prepared.err)
				continue
			}

			attempt := Attempt{
				Target:  target,
				Conn:    resolved.conn,
				Creds:   prepared.creds,
				Account: prepared.account,
			}
			if isUnhealthy {
				unhealthyAttempts = append(unhealthyAttempts, attempt)
			} else {
				attempts = append(attempts, attempt)
			}
		}
	}

	// Fall back to unhealthy accounts when no healthy candidate exists.
	// This prevents a total outage when the background probe incorrectly
	// marks all accounts unhealthy, and gives NoteSuccess a chance to
	// recover the health row via real production traffic.
	if len(attempts) == 0 && len(unhealthyAttempts) > 0 {
		attempts = unhealthyAttempts
		lastReason = ""
	}

	if len(attempts) == 0 {
		if !earliestRetryAt.IsZero() {
			retryAfter := time.Until(earliestRetryAt)
			if retryAfter < 0 {
				retryAfter = 0
			}
			msg := "dispatch: all matching candidates are temporarily unavailable"
			// Surface non-cooldown skips so a dead account (revoked refresh
			// token, vault error) is not misdiagnosed as a plain rate limit.
			if nonCooldownReason != "" {
				msg += "; also skipped: " + nonCooldownReason
			}
			return nil, &core.ProviderError{
				Kind:       core.ErrRateLimit,
				Scope:      blockedScope,
				Message:    msg,
				RetryAfter: retryAfter,
			}
		}
		if len(opts.AllowedAccountIDs) > 0 {
			return nil, &core.ProviderError{
				Kind:    core.ErrBadRequest,
				Scope:   core.FailureScopeRequest,
				Message: "dispatch: selected account is unavailable for this route",
			}
		}
		if lastReason == "" {
			lastReason = "no usable targets in chain"
		}
		return nil, &core.ProviderError{Kind: core.ErrInternal, Message: "dispatch: " + lastReason}
	}
	return attempts, nil
}

// NoteFailure applies cooldowns to an account (and optionally a model) based on
// a provider error. Exponential backoff increases the cooldown on repeated
// failures for rate-limit / quota errors. Concurrent failures on the same
// account within FailureDedupWindow escalate once, and locally computed
// cooldowns carry jitter so cooled accounts recover at staggered times.
//
// Two categories of error are explicitly NOT cooled down:
//   - Client cancellations (user pressed Esc, client closed connection): the
//     provider may be perfectly healthy; penalizing it causes false fallbacks.
//   - Self-inflicted timeouts (our own deadline fired while the upstream was
//     still processing): again, the provider is healthy — we gave up, not them.
func (d *Dispatcher) NoteFailure(ctx context.Context, accountID string, err *core.ProviderError) {
	if err == nil {
		return
	}

	scope := err.EffectiveScope()
	if err.Kind == core.ErrClientCanceled || scope == core.FailureScopeRequest {
		return
	}

	if (scope == core.FailureScopeProvider || scope == core.FailureScopeNetwork) &&
		(err.Kind == core.ErrUpstream || err.Kind == core.ErrTimeout) {
		d.recordProviderFailure(err.Provider)
	}

	if scope == core.FailureScopeModel {
		cooldown := 5 * time.Minute
		if err.RetryAfter > cooldown {
			cooldown = err.RetryAfter
		}
		err.RetryAfter = cooldown
		if d.routing != nil && err.Model != "" {
			if !d.shouldEscalateFailure(accountID+"\x00"+err.Model, time.Now()) {
				// A concurrent failure already locked this model; report the
				// cooldown it set instead of extending the lock again.
				if exps, rerr := d.routing.ActiveCooldownExpirations(ctx, []string{accountID}, err.Model); rerr == nil {
					if until, ok := exps[accountID]; ok {
						err.RetryAfter = max(time.Until(until), 0)
					}
				}
				return
			}
			_ = d.routing.SetModelCooldown(ctx, accountID, err.Model, time.Now().Add(cooldown))
		}
		return
	}

	// Free providers (auth_kind: "none") have no credentials to protect and
	// only one auto-seeded account, so cooldowns would lock out the only
	// routing path. Skip cooldowns for these accounts.
	if acc, aerr := d.accounts.Get(ctx, accountID); aerr == nil && acc.AuthKind == store.AuthNone {
		return
	}

	// Classify first so errors that never cool down cannot consume the dedup
	// window and suppress a real cooldown moments later.
	switch err.Kind {
	case core.ErrRateLimit, core.ErrQuotaExhausted, core.ErrAuth:
	case core.ErrUpstream, core.ErrTimeout:
		// Self-inflicted timeout: our own request deadline fired while the
		// upstream was still processing. The provider is healthy — skip
		// cooldown to avoid penalizing a working account.
		if err.Kind == core.ErrTimeout && err.StatusCode == 0 &&
			errors.Is(err.Cause, context.DeadlineExceeded) {
			return
		}
	default:
		return
	}

	// A depleted paid balance is terminal: the 24h park and credits flag are
	// semantically stronger than whatever short cooldown a concurrent failure
	// in the same burst may have set, so it must not be deduped away.
	creditsExhausted := err.Kind == core.ErrQuotaExhausted && err.CreditsExhausted
	if !d.shouldEscalateFailure(accountID, time.Now()) && !creditsExhausted {
		// A concurrent failure already cooled this account down; report the
		// cooldown it set instead of advancing the backoff level again.
		if acc, aerr := d.accounts.Get(ctx, accountID); aerr == nil && acc.CooldownUntil != nil {
			if remaining := time.Until(*acc.CooldownUntil); remaining > err.RetryAfter {
				err.RetryAfter = remaining
			}
		}
		return
	}

	var cooldown time.Duration
	jitter := true
	switch err.Kind {
	case core.ErrRateLimit:
		cooldown = d.exponentialCooldown(ctx, accountID)
	case core.ErrQuotaExhausted:
		cooldown = 30 * time.Minute
		if err.CreditsExhausted {
			// A depleted paid balance is terminal until the user tops up.
			// Park the account for a day instead of thrashing through
			// half-hour cooldowns; jitter is pointless at this horizon.
			cooldown = CreditsExhaustedCooldown
			jitter = false
		}
	case core.ErrAuth:
		cooldown = 5 * time.Minute
	default:
		// Transient upstream errors: apply a short cooldown so the account
		// gets a breather without being locked out for too long.
		cooldown = TransientCooldown
	}
	if jitter {
		cooldown = withJitter(cooldown)
	}

	// An upstream reset time is a lower bound. Never replace it with the much
	// shorter local exponential delay, and never shorten a stricter local
	// cooldown when an upstream sends a small Retry-After value.
	if err.RetryAfter > cooldown {
		cooldown = err.RetryAfter
	}
	err.RetryAfter = cooldown

	_ = d.accounts.SetCooldown(ctx, accountID, time.Now().Add(cooldown))

	// A depleted balance is terminal until the user tops up: flag the account
	// so the dashboard can surface it and a manual reset can clear it. The
	// flag also clears automatically on the next successful request.
	if creditsExhausted {
		_ = d.accounts.SetCreditsExhausted(ctx, accountID, true)
	}

	// Also set a model-level cooldown when a model is specified, so other
	// models on the same account remain available.
	if d.routing != nil && err.Model != "" {
		modelCooldown := time.Duration(int64(cooldown) * ModelCooldownMultiplier)
		_ = d.routing.SetModelCooldown(ctx, accountID, err.Model, time.Now().Add(modelCooldown))
	}
}

// NoteSuccess resets the backoff level for an account and clears any model
// cooldown. Called by the pipeline after a successful upstream response.
func (d *Dispatcher) NoteSuccess(ctx context.Context, provider, accountID, model string) {
	_ = d.accounts.ResetBackoffLevel(ctx, accountID)
	// A success proves the account recovered, so a later failure is a fresh
	// incident rather than part of the burst that set the dedup marker.
	d.failureMu.Lock()
	delete(d.recentFailures, accountID)
	if model != "" {
		delete(d.recentFailures, accountID+"\x00"+model)
	}
	d.failureMu.Unlock()
	if d.routing != nil && model != "" {
		_ = d.routing.ClearModelCooldown(ctx, accountID, model)
	}
	if d.health != nil && model != "" {
		_ = d.health.MarkHealthy(ctx, accountID, model)
	}
	d.recordProviderSuccess(provider)
}

// shouldEscalateFailure reports whether this failure should escalate cooldown
// state for key, recording it when so. Failures arriving within
// FailureDedupWindow of a recorded one are collapsed: a burst of parallel
// requests dying on the same credential is one incident, not N, so the backoff
// level advances a single step and the cooldown horizon is set once.
func (d *Dispatcher) shouldEscalateFailure(key string, now time.Time) bool {
	d.failureMu.Lock()
	defer d.failureMu.Unlock()
	if last, ok := d.recentFailures[key]; ok && now.Sub(last) < FailureDedupWindow {
		return false
	}
	if len(d.recentFailures) >= maxRecentFailureEntries {
		for k, ts := range d.recentFailures {
			if now.Sub(ts) >= FailureDedupWindow {
				delete(d.recentFailures, k)
			}
		}
	}
	d.recentFailures[key] = now
	return true
}

// withJitter extends a locally computed cooldown by a random amount up to
// BackoffJitterFraction of its length. Accounts that fail together (e.g. a
// shared quota window closing) then recover at staggered times instead of
// receiving the retry burst simultaneously. Jitter is never applied to
// upstream-specified Retry-After values, which remain exact lower bounds.
func withJitter(cooldown time.Duration) time.Duration {
	if cooldown <= 0 {
		return cooldown
	}
	span := int64(float64(cooldown) * BackoffJitterFraction)
	if span <= 0 {
		return cooldown
	}
	return cooldown + time.Duration(rand.Int64N(span+1))
}

// exponentialCooldown computes the cooldown duration using exponential backoff.
// Level 1: 2s, Level 2: 4s, Level 3: 8s... up to BackoffMax (5min).
func (d *Dispatcher) exponentialCooldown(ctx context.Context, accountID string) time.Duration {
	// Try to read current backoff level from the account.
	acc, err := d.accounts.Get(ctx, accountID)
	if err != nil {
		return d.defaultCooldown
	}

	newLevel := acc.BackoffLevel + 1
	if newLevel > BackoffMaxLevel {
		newLevel = BackoffMaxLevel
	}

	// Persist the new backoff level.
	_ = d.accounts.SetBackoffLevel(ctx, accountID, newLevel)

	cooldown := time.Duration(float64(BackoffBase) * math.Pow(2, float64(newLevel-1)))
	if cooldown > BackoffMax {
		cooldown = BackoffMax
	}
	return cooldown
}

// applyRotation reorders targets according to the round-robin strategy.
// For "fallback" (or when routing is not configured), targets are returned
// as-is. For "round-robin", the persisted cursor is advanced and the targets
// are rotated so the cursor index comes first.
func (d *Dispatcher) applyRotation(ctx context.Context, targets []Target, opts PlanOptions) []Target {
	if opts.Strategy != StrategyRoundRobin || len(targets) <= 1 || d.routing == nil || opts.ChainID == "" {
		return targets
	}

	sticky := opts.StickyLimit
	if sticky <= 0 {
		sticky = DefaultStickyLimit
	}
	state, _ := d.routing.GetChainRotationState(ctx, opts.ChainID)
	cursor, nextCursor, nextHitCount := advanceRotationState(len(targets), state.LastIndex, state.HitCount, sticky)

	rotated := make([]Target, len(targets))
	for i := range targets {
		rotated[i] = targets[(cursor+i)%len(targets)]
	}

	_ = d.routing.SetChainRotationState(ctx, store.ChainRotation{
		ChainID:   opts.ChainID,
		LastIndex: nextCursor,
		HitCount:  nextHitCount,
	})

	return rotated
}

func (opts PlanOptions) accountRoutingForTarget(provider string) AccountRoutingOptions {
	if opts.ProviderAccountStrategies != nil {
		if override, ok := opts.ProviderAccountStrategies[provider]; ok {
			return override
		}
	}
	return AccountRoutingOptions{
		Strategy:    opts.AccountStrategy,
		StickyLimit: opts.AccountStickyLimit,
		AffinityKey: opts.AccountAffinityKey,
		AffinityTTL: opts.AccountAffinityTTL,
	}
}

func (d *Dispatcher) applyAccountRouting(ctx context.Context, tenantID string, target Target, accounts []store.Account, opts AccountRoutingOptions) []store.Account {
	switch opts.Strategy {
	case StrategyRoundRobin:
		return d.applyAccountRoundRobin(ctx, tenantID, target, accounts, opts)
	case StrategySmartRoundRobin:
		return d.applySmartAccountRoundRobin(ctx, tenantID, target, accounts, opts)
	default:
		return accounts
	}
}

// applyAccountRoundRobin reorders accounts within one target according to the
// account round-robin strategy. The persisted key is tenant/provider/model so
// direct model routes and combo steps share fair account distribution.
func (d *Dispatcher) applyAccountRoundRobin(ctx context.Context, tenantID string, target Target, accounts []store.Account, opts AccountRoutingOptions) []store.Account {
	if len(accounts) <= 1 || d.routing == nil {
		return accounts
	}

	sticky := opts.StickyLimit
	if sticky <= 0 {
		sticky = DefaultStickyLimit
	}
	scopeKey := accountRotationKey(tenantID, target)
	state, _ := d.routing.GetTargetRotationState(ctx, scopeKey)
	cursor, nextCursor, nextHitCount := advanceRotationState(len(accounts), state.LastIndex, state.HitCount, sticky)

	rotated := make([]store.Account, len(accounts))
	for i := range accounts {
		rotated[i] = accounts[(cursor+i)%len(accounts)]
	}

	_ = d.routing.SetTargetRotationState(ctx, store.TargetRotation{
		ScopeKey:  scopeKey,
		LastIndex: nextCursor,
		HitCount:  nextHitCount,
	})
	return rotated
}

// applySmartAccountRoundRobin is round-robin for new affinity keys and sticky
// for known keys. It mirrors load-balancer session affinity: the first request
// chooses an account using round-robin, then follow-up requests for the same
// affinity key start with that account while keeping the rest as fallbacks.
func (d *Dispatcher) applySmartAccountRoundRobin(ctx context.Context, tenantID string, target Target, accounts []store.Account, opts AccountRoutingOptions) []store.Account {
	if len(accounts) <= 1 || d.routing == nil {
		return accounts
	}
	if opts.AffinityKey == "" {
		return d.applyAccountRoundRobin(ctx, tenantID, target, accounts, opts)
	}

	ttl := opts.AffinityTTL
	if ttl <= 0 {
		ttl = DefaultAffinityTTL
	}
	now := time.Now()
	scopeKey := accountAffinityKey(tenantID, target, opts.AffinityKey)
	affinity, _ := d.routing.GetAccountAffinity(ctx, scopeKey)
	if affinity.AccountID != "" && affinity.ExpiresAt.After(now) {
		if reordered, ok := moveAccountToFront(accounts, affinity.AccountID); ok {
			_ = d.routing.SetAccountAffinity(ctx, store.AccountAffinity{
				ScopeKey:  scopeKey,
				AccountID: affinity.AccountID,
				ExpiresAt: now.Add(ttl),
			})
			return reordered
		}
	}

	rotated := d.applyAccountRoundRobin(ctx, tenantID, target, accounts, opts)
	if len(rotated) > 0 {
		_ = d.routing.SetAccountAffinity(ctx, store.AccountAffinity{
			ScopeKey:  scopeKey,
			AccountID: rotated[0].ID,
			ExpiresAt: now.Add(ttl),
		})
	}
	return rotated
}

func accountRotationKey(tenantID string, target Target) string {
	return tenantID + "\x00" + target.Provider + "\x00" + target.Model
}

func accountAffinityKey(tenantID string, target Target, affinityKey string) string {
	sum := sha256.Sum256([]byte(affinityKey))
	return tenantID + "\x00" + target.Provider + "\x00" + target.Model + "\x00affinity\x00" + hex.EncodeToString(sum[:])
}

func moveAccountToFront(accounts []store.Account, accountID string) ([]store.Account, bool) {
	idx := -1
	for i, acc := range accounts {
		if acc.ID == accountID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return accounts, false
	}
	out := make([]store.Account, 0, len(accounts))
	out = append(out, accounts[idx])
	out = append(out, accounts[:idx]...)
	out = append(out, accounts[idx+1:]...)
	return out, true
}

// EvictAccountAffinity expires a smart-routing pin after its account fails so
// the next request is not sent straight back to a cooling credential.
func (d *Dispatcher) EvictAccountAffinity(ctx context.Context, tenantID string, target Target, opts PlanOptions, accountID string) {
	if d.routing == nil || accountID == "" {
		return
	}
	accountOpts := opts.accountRoutingForTarget(target.Provider)
	if accountOpts.Strategy != StrategySmartRoundRobin || accountOpts.AffinityKey == "" {
		return
	}
	scopeKey := accountAffinityKey(tenantID, target, accountOpts.AffinityKey)
	affinity, err := d.routing.GetAccountAffinity(ctx, scopeKey)
	if err != nil || affinity.AccountID != accountID {
		return
	}
	_ = d.routing.SetAccountAffinity(ctx, store.AccountAffinity{
		ScopeKey:  scopeKey,
		AccountID: "",
		ExpiresAt: time.Unix(0, 0),
	})
}

func (d *Dispatcher) lockSelection(targets []Target) func() {
	providers := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.Provider == "" {
			continue
		}
		if _, ok := seen[target.Provider]; ok {
			continue
		}
		seen[target.Provider] = struct{}{}
		providers = append(providers, target.Provider)
	}
	sort.Strings(providers)
	locks := make([]*sync.Mutex, 0, len(providers))
	for _, provider := range providers {
		value, _ := d.selectionLocks.LoadOrStore(provider, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		lock.Lock()
		locks = append(locks, lock)
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}
}

func (d *Dispatcher) recordProviderFailure(provider string) {
	if provider == "" {
		return
	}
	now := time.Now()
	d.circuitMu.Lock()
	defer d.circuitMu.Unlock()
	state := d.circuits[provider]
	if !state.LastFailure.IsZero() && now.Sub(state.LastFailure) > ProviderCircuitResetWindow {
		state.Failures = 0
		state.OpenUntil = time.Time{}
	}
	state.Failures++
	state.LastFailure = now
	if state.Failures >= ProviderCircuitFailureThreshold {
		exponent := state.Failures - ProviderCircuitFailureThreshold
		if exponent > 10 {
			exponent = 10
		}
		cooldown := ProviderCircuitBaseCooldown * time.Duration(1<<exponent)
		if cooldown > ProviderCircuitMaxCooldown {
			cooldown = ProviderCircuitMaxCooldown
		}
		state.OpenUntil = now.Add(cooldown)
	}
	d.circuits[provider] = state
}

func (d *Dispatcher) recordProviderSuccess(provider string) {
	if provider == "" {
		return
	}
	d.circuitMu.Lock()
	delete(d.circuits, provider)
	d.circuitMu.Unlock()
}

func (d *Dispatcher) providerCircuitRemaining(provider string, now time.Time) time.Duration {
	if provider == "" {
		return 0
	}
	d.circuitMu.Lock()
	defer d.circuitMu.Unlock()
	state, ok := d.circuits[provider]
	if !ok || !state.OpenUntil.After(now) {
		return 0
	}
	return state.OpenUntil.Sub(now)
}

// advanceRotationState returns the cursor to use for this request, plus the
// cursor/hit-count state to persist for the next request. lastIndex is the next
// starting index.
func advanceRotationState(length, lastIndex, hitCount, stickyLimit int) (cursor int, nextCursor int, nextHitCount int) {
	if length <= 0 {
		return 0, 0, 0
	}
	if stickyLimit <= 0 {
		stickyLimit = DefaultStickyLimit
	}
	cursor = lastIndex % length
	if cursor < 0 {
		cursor += length
	}
	nextCursor = cursor
	nextHitCount = hitCount + 1
	if nextHitCount >= stickyLimit {
		nextCursor = (cursor + 1) % length
		nextHitCount = 0
	}
	return cursor, nextCursor, nextHitCount
}

// TargetsFromChain flattens a stored chain into ordered targets.
func TargetsFromChain(chain store.Chain) []Target {
	out := make([]Target, 0, len(chain.Steps))
	for _, s := range chain.Steps {
		out = append(out, Target{
			Provider: s.Provider, Model: s.Model,
			InputPerM: s.InputPerM, OutputPerM: s.OutputPerM,
			CacheWritePerM: s.CacheWritePerM, CacheReadPerM: s.CacheReadPerM,
		})
	}
	return out
}
