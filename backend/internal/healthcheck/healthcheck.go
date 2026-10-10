package healthcheck

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// ConnectorSource resolves provider connectors.
type ConnectorSource interface {
	Get(provider string) (core.Connector, error)
}

// Config controls the background health checker.
type Config struct {
	Enabled              bool
	Interval             time.Duration
	Timeout              time.Duration
	MaxParallel          int
	FailureThreshold     int
	SuccessThreshold     int
	RecentModelWindow    time.Duration
	MaxModelsPerProvider int
}

// Checker probes configured provider accounts and records account/model health.
type Checker struct {
	cfg      Config
	log      *slog.Logger
	accounts *store.AccountRepo
	health   *store.HealthRepo
	conns    ConnectorSource
	vault    *vault.Vault
}

// New builds a Checker.
func New(cfg Config, log *slog.Logger, accounts *store.AccountRepo, health *store.HealthRepo, conns ConnectorSource, vault *vault.Vault) *Checker {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = 8
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 1
	}
	if cfg.RecentModelWindow <= 0 {
		cfg.RecentModelWindow = 24 * time.Hour
	}
	if cfg.MaxModelsPerProvider <= 0 {
		cfg.MaxModelsPerProvider = 8
	}
	if log == nil {
		log = slog.Default()
	}
	return &Checker{cfg: cfg, log: log, accounts: accounts, health: health, conns: conns, vault: vault}
}

// Run starts periodic probes until ctx is cancelled.
func (c *Checker) Run(ctx context.Context, tenantID string) {
	if c == nil || !c.cfg.Enabled {
		return
	}
	c.CheckOnce(ctx, tenantID)
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.CheckOnce(ctx, tenantID)
		}
	}
}

// CheckOnce probes all enabled accounts for a tenant.
func (c *Checker) CheckOnce(ctx context.Context, tenantID string) {
	if c == nil || c.accounts == nil || c.health == nil || c.conns == nil || c.vault == nil {
		return
	}
	accounts, err := c.accounts.ListByTenant(ctx, tenantID)
	if err != nil {
		c.log.Warn("health check: list accounts failed", "err", err)
		return
	}
	recent, _ := c.health.RecentAccountModels(ctx, tenantID, time.Now().Add(-c.cfg.RecentModelWindow), len(accounts)*c.cfg.MaxModelsPerProvider)
	// Real traffic is the strongest health signal: a pair that served a
	// successful request within the staleness window needs no synthetic
	// probe; only pairs that have gone quiet are probed.
	provenHealthy, _ := c.health.RecentSuccessfulAccountModels(ctx, tenantID, time.Now().Add(-c.stalenessWindow()))

	modelsByAccount := map[string][]string{}
	for _, h := range recent {
		if len(modelsByAccount[h.AccountID]) >= c.cfg.MaxModelsPerProvider {
			continue
		}
		if _, proven := provenHealthy[h.AccountID+"\x00"+h.Model]; proven {
			continue
		}
		modelsByAccount[h.AccountID] = appendUnique(modelsByAccount[h.AccountID], h.Model)
	}

	sem := make(chan struct{}, c.cfg.MaxParallel)
	var wg sync.WaitGroup
	now := time.Now()
	for _, acc := range accounts {
		if acc.Disabled || acc.NeedsReconnect {
			continue
		}
		// An account on cooldown is already known to be unavailable; probing
		// it burns quota and only extends the cooldown.
		if acc.CooldownUntil != nil && acc.CooldownUntil.After(now) {
			continue
		}
		models := modelsByAccount[acc.ID]
		if len(models) == 0 {
			if _, proven := provenHealthy[acc.ID+"\x00__all__"]; proven || c.accountProvenHealthy(acc.ID, provenHealthy) {
				continue
			}
			models = []string{"__all__"}
		}
		for _, model := range models {
			acc := acc
			model := model
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Spread probes over the cycle instead of bursting them at
				// the tick, which both smooths provider load and keeps
				// several replicas from probing in lockstep.
				if !sleepJitter(ctx, c.probeSpread()) {
					return
				}
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				c.probe(ctx, acc, model)
			}()
		}
	}
	wg.Wait()
}

// stalenessWindow is how long a real-traffic success keeps a pair exempt from
// probing: two intervals.
func (c *Checker) stalenessWindow() time.Duration {
	return 2 * c.cfg.Interval
}

// probeSpread is the maximum random delay before a probe starts.
func (c *Checker) probeSpread() time.Duration {
	spread := c.cfg.Interval / 4
	if spread > 30*time.Second {
		spread = 30 * time.Second
	}
	return spread
}

// accountProvenHealthy reports whether any model on the account served
// successful traffic recently, which makes a credential-only probe redundant.
func (c *Checker) accountProvenHealthy(accountID string, proven map[string]time.Time) bool {
	prefix := accountID + "\x00"
	for key := range proven {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func sleepJitter(ctx context.Context, max time.Duration) bool {
	if max <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(time.Duration(rand.Int64N(int64(max))))
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// probeMaxTokens picks the smallest completion budget the model family
// accepts: OpenAI reasoning models reject anything below 16.
func probeMaxTokens(provider, model string) int {
	m := strings.ToLower(model)
	if strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4") || provider == "openai" || provider == "azure" {
		return 16
	}
	return 8
}

func (c *Checker) probe(ctx context.Context, acc store.Account, model string) {
	conn, err := c.conns.Get(acc.Provider)
	if err != nil {
		c.record(ctx, acc, model, 0, err)
		return
	}
	creds, err := c.vault.Open(acc)
	if err != nil {
		c.record(ctx, acc, model, 0, err)
		return
	}

	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	start := time.Now()
	if model == "__all__" {
		if v, ok := conn.(core.Validator); ok {
			err = v.Validate(probeCtx, creds)
		} else {
			err = nil
		}
	} else {
		max := probeMaxTokens(acc.Provider, model)
		req := &core.ChatRequest{
			Model: model,
			Messages: []core.Message{{
				Role: core.RoleUser,
				Content: []core.ContentPart{{
					Type: core.PartText,
					Text: "ping",
				}},
			}},
			MaxTokens: &max,
			Metadata: core.RequestMetadata{
				TenantID: acc.TenantID,
				Provider: acc.Provider,
			},
		}
		_, err = conn.Chat(probeCtx, req, creds)
	}
	c.record(ctx, acc, model, int(time.Since(start).Milliseconds()), err)
}

// neutralProbeError reports whether a probe failure says nothing about the
// account's health: throttling (the probe itself may have caused it), our own
// probe deadline on a slow-but-working model, or shutdown cancellation.
func neutralProbeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch core.AsProviderError(err).Kind {
	case core.ErrRateLimit, core.ErrClientCanceled, core.ErrContextWindow, core.ErrContentFilter:
		return true
	case core.ErrTimeout:
		return true
	case core.ErrModelUnavailable, core.ErrCapability, core.ErrBadRequest:
		// A model this account cannot serve, or a probe payload the model
		// rejects, says nothing about the credential's health. The dispatcher
		// handles model availability per request.
		return true
	}
	return false
}

func (c *Checker) record(ctx context.Context, acc store.Account, model string, latencyMS int, probeErr error) {
	if neutralProbeError(probeErr) {
		c.log.Debug("health check: inconclusive probe", "account", acc.ID, "model", model, "err", probeErr)
		return
	}
	now := time.Now()
	prev, err := c.health.Get(ctx, acc.ID, model)
	if err != nil && err != store.ErrNotFound {
		c.log.Debug("health check: read previous state failed", "account", acc.ID, "model", model, "err", err)
	}
	h := prev
	if h.ID == "" {
		h.ID = uuid.NewString()
		h.TenantID = acc.TenantID
		h.AccountID = acc.ID
		h.Provider = acc.Provider
		h.Model = model
	}
	h.LatencyMS = latencyMS
	h.LastCheckedAt = now
	h.UpdatedAt = now
	if probeErr == nil {
		h.ConsecutiveSuccesses++
		h.ConsecutiveFailures = 0
		h.LastError = ""
		h.LastOKAt = &now
		if h.ConsecutiveSuccesses >= c.cfg.SuccessThreshold {
			h.Status = "healthy"
		} else if h.Status == "" {
			h.Status = "degraded"
		}
	} else {
		h.ConsecutiveFailures++
		h.ConsecutiveSuccesses = 0
		h.LastError = probeErr.Error()
		if h.ConsecutiveFailures >= c.cfg.FailureThreshold {
			h.Status = "unhealthy"
		} else {
			h.Status = "degraded"
		}
	}
	if err := c.health.Upsert(ctx, h); err != nil {
		c.log.Warn("health check: upsert failed", "account", acc.ID, "model", model, "err", err)
	}
}

func appendUnique(in []string, value string) []string {
	for _, existing := range in {
		if existing == value {
			return in
		}
	}
	return append(in, value)
}
