// Package app wires KeiRouter's components into a runnable application: it
// opens the store, runs migrations, loads or generates the master key, and
// constructs the gateway with its full dependency graph.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/cache"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/crypto"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/gateway"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/guardrails/bias"
	"github.com/mydisha/keirouter/backend/internal/guardrails/injection"
	"github.com/mydisha/keirouter/backend/internal/guardrails/pii"
	"github.com/mydisha/keirouter/backend/internal/guardrails/topics"
	"github.com/mydisha/keirouter/backend/internal/guardrails/toxicity"
	"github.com/mydisha/keirouter/backend/internal/health"
	"github.com/mydisha/keirouter/backend/internal/healthcheck"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/limits"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/oauth"
	"github.com/mydisha/keirouter/backend/internal/observ"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
	"github.com/mydisha/keirouter/backend/internal/slimmer"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/transform"
	"github.com/mydisha/keirouter/backend/internal/tunnel/cloudflare"
	"github.com/mydisha/keirouter/backend/internal/tunnel/tailscale"
	"github.com/mydisha/keirouter/backend/internal/update"
	"github.com/mydisha/keirouter/backend/internal/usagehub"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// App is the assembled application, ready to serve.
type App struct {
	cfg                config.Config
	log                *slog.Logger
	db                 *store.DB
	accounts           *store.AccountRepo
	server             *http.Server
	keepAlive          *oauth.KeepAlive
	guardrailAudit     *guardrails.AuditWriter
	guardrailRetention *guardrails.RetentionSweeper
	meter              *meter.Meter
	healthChecker      *healthcheck.Checker
	providerHealth     *health.Service
	probeRunner        *health.ProbeRunner

	// bg tracks long-lived background workers that touch the DB (oauth
	// keepalive, health checker, cooldown sweeper) so shutdown can wait for
	// them to return before closing the store, avoiding a use-after-close race.
	bg sync.WaitGroup
}

// Build constructs the application from configuration. It opens the database,
// applies migrations, initializes the crypto root, and wires the gateway.
func Build(ctx context.Context, cfg config.Config, log *slog.Logger, version string) (*App, error) {
	applyMemoryLimit(log)
	httputil.SetAllowPrivateBaseURL(cfg.Security.AllowPrivateBaseURL)

	dataDir, err := resolveDataDir(cfg)
	if err != nil {
		return nil, err
	}

	db, err := store.Open(ctx, cfg.Database, dataDir)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("app: migrate: %w", err)
	}
	if err := db.Tenants().EnsureDefault(ctx); err != nil {
		return nil, fmt.Errorf("app: ensure default tenant: %w", err)
	}

	// Clear stale cooldowns left over from a previous session so accounts
	// are immediately usable after a restart.
	if cleared, cerr := db.Accounts().ClearExpiredCooldowns(ctx); cerr != nil {
		log.Warn("failed to clear expired cooldowns", "err", cerr)
	} else if cleared > 0 {
		log.Info("cleared stale account cooldowns from previous session", "count", cleared)
	}

	masterKey, err := loadOrCreateMasterKey(cfg, dataDir, log)
	if err != nil {
		return nil, err
	}
	sealer, err := crypto.NewSealer(masterKey)
	if err != nil {
		return nil, fmt.Errorf("app: build sealer: %w", err)
	}

	// Construct services.
	v := vault.New(sealer)
	connRegistry := connectors.DefaultRegistry()
	// Load user-defined dynamic custom providers and their custom models from
	// the database so they are routable and discoverable immediately at startup.
	loadCustomProviders(ctx, db, log)
	codecs := transform.DefaultRegistry()

	idSvc := identity.New(db.APIKeys())

	authSvc := auth.New(db.Settings(), cfg.Security.JWTSecret, cfg.Security.SessionTTL)
	seeded, err := authSvc.EnsureDefaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("app: init auth: %w", err)
	}
	if seeded {
		log.Warn("seeded default dashboard password",
			"password", auth.DefaultPassword,
			"note", "change it on first login via the onboarding flow")
	}

	pricing := buildPricing()
	modelPrices := buildModelPrices(ctx, db, log)
	mtr := meter.New(db.Usage(), pricing, modelPrices)
	// Reprice deterministic legacy rows in the background so a large usage
	// history (e.g. after a database restore) never delays the server from
	// listening. Unknown or subscription-only models remain visibly unpriced
	// instead of silently free.
	go func() {
		started := time.Now()
		if updated, backfillErr := mtr.BackfillUnpriced(ctx); backfillErr != nil {
			if ctx.Err() == nil {
				log.Warn("usage price backfill failed", "err", backfillErr, "updated", updated)
			}
		} else if updated > 0 {
			log.Info("usage price backfill completed", "updated", updated, "took", time.Since(started).Round(time.Millisecond))
		}
	}()
	mtr.EnableAsync(meter.AsyncConfig{
		Enabled:              cfg.Meter.Async,
		BatchSize:            cfg.Meter.BatchSize,
		FlushInterval:        cfg.Meter.FlushInterval,
		QueueSize:            cfg.Meter.QueueSize,
		FullQueuePolicy:      cfg.Meter.FullQueuePolicy,
		ShutdownFlushTimeout: cfg.Meter.ShutdownFlushTimeout,
		Logger:               log,
	})
	uh := usagehub.New()
	mtr.SetHub(uh)
	bud := budget.New(db.Budgets(), db.Usage())
	disp := dispatch.New(connRegistry, db.Accounts(), v)
	// Refresh expiring OAuth access tokens just-in-time before each upstream
	// call, persisting the rotated tokens.
	tokenRefresher := oauth.NewTokenManager(v, db.Accounts())
	disp.SetTokenRefresher(tokenRefresher)
	// Resolve proxy pool bindings for accounts that have one.
	disp.SetPoolSource(db.ProxyPools())
	// Model-level cooldowns, round-robin chain rotation, and background health.
	disp.SetRoutingSource(db.Routing())
	disp.SetHealthSource(db.Health())
	slim := slimmer.Default()
	metrics := observ.New()

	// Semantic cache. Defaults to a local in-memory store keyed by a
	// deterministic hash embedder (exact-prompt caching). When configured with
	// backend=redis + embedding_provider=api, uses Redis for persistence and
	// an embedding API for true semantic near-match caching.
	var cacheStore cache.Store
	if cfg.Cache.Backend == "redis" && cfg.Cache.RedisURL != "" {
		rs, err := cache.NewRedisStore(cache.RedisStoreConfig{
			URL:    cfg.Cache.RedisURL,
			TTL:    cfg.Cache.TTL,
			Logger: log,
		})
		if err != nil {
			return nil, fmt.Errorf("app: redis cache store: %w", err)
		}
		cacheStore = rs
		log.Info("semantic cache: redis backend", "url", cfg.Cache.RedisURL)
	}

	var embedder cache.Embedder
	if cfg.Cache.EmbeddingProvider == "api" && cfg.Cache.EmbeddingAPIKey != "" {
		embedder = cache.NewAPIEmbedder(cache.APIEmbedderConfig{
			BaseURL: cfg.Cache.EmbeddingAPIURL,
			APIKey:  cfg.Cache.EmbeddingAPIKey,
			Model:   cfg.Cache.EmbeddingModel,
			Dims:    cfg.Cache.EmbeddingDims,
		})
		log.Info("semantic cache: API embedder", "model", cfg.Cache.EmbeddingModel)
	} else {
		embedder = cache.NewHashEmbedder(32)
		if cfg.Cache.Enabled {
			log.Info("semantic cache: hash embedder (exact-match only)")
		}
	}

	semanticCache := cache.New(cache.Config{
		Enabled:             cfg.Cache.Enabled,
		SimilarityThreshold: cfg.Cache.SimilarityThreshold,
		TTL:                 cfg.Cache.TTL,
		MaxEntries:          10000,
	}, cacheStore)

	// Timeout notifier: initialize from persisted dashboard settings, then keep
	// accepting live updates without a restart. Previously startup ignored the
	// persisted values and silently fell back to the shorter config timeout.
	stallTimeout, responseHeaderTimeout, requestTimeout := initialTimeoutValues(
		ctx,
		db.Settings(),
		cfg.Server.StreamStallTimeout,
		60*time.Second,
		cfg.Server.RequestTimeout,
	)
	timeoutNotifier := gateway.NewTimeoutNotifier(stallTimeout, responseHeaderTimeout, requestTimeout)

	// Proxy notifier: atomic cache of outbound proxy config that can be updated
	// at runtime from the dashboard settings without restarting.
	var proxyEnabled bool
	var proxyURL, noProxy string
	if raw, err := db.Settings().Get(ctx, "endpoint_settings"); err == nil && raw != "" {
		var es struct {
			OutboundProxyEnabled bool   `json:"outbound_proxy_enabled"`
			OutboundProxyURL     string `json:"outbound_proxy_url"`
			OutboundNoProxy      string `json:"outbound_no_proxy"`
		}
		if json.Unmarshal([]byte(raw), &es) == nil {
			proxyEnabled = es.OutboundProxyEnabled
			proxyURL = es.OutboundProxyURL
			noProxy = es.OutboundNoProxy
		}
	}
	proxyNotifier := gateway.NewProxyNotifier(proxyEnabled, proxyURL, noProxy)
	disp.SetGlobalProxy(proxyNotifier)

	cfg.Limits.Enabled = true
	if raw, err := db.Settings().Get(ctx, "endpoint_settings"); err == nil && raw != "" {
		var es struct {
			RateLimitsEnabled *bool `json:"rate_limits_enabled"`
		}
		if json.Unmarshal([]byte(raw), &es) == nil && es.RateLimitsEnabled != nil {
			cfg.Limits.Enabled = *es.RateLimitsEnabled
		}
	}

	// Guardrails: content-safety policies layered global → provider → model →
	// chain → apikey. Resolver caches lookups for 30s; audit writer drains a
	// buffered channel to the guardrail_logs table.
	guardrailResolver := guardrails.NewResolver(db.Guardrails(), 30*time.Second)
	// Audit log hub: AuditWriter publishes successfully-flushed rows here so
	// the dashboard's Logs tab can subscribe via SSE for near-real-time
	// updates without polling.
	guardrailLogHub := guardrails.NewLogHub()
	guardrailAudit := guardrails.NewAuditWriter(db.GuardrailLogs(), log, guardrails.AuditWriterConfig{Hub: guardrailLogHub})
	// Toxicity: native engine ships unconditionally; OpenAI Moderation is
	// wired only when an API key is configured.
	toxCfg := toxicity.Config{}
	if cfg.Guardrails.Toxicity.OpenAIAPIKey != "" {
		toxCfg.OpenAI = &toxicity.OpenAIConfig{
			APIKey:  cfg.Guardrails.Toxicity.OpenAIAPIKey,
			BaseURL: cfg.Guardrails.Toxicity.OpenAIBaseURL,
			Model:   cfg.Guardrails.Toxicity.OpenAIModel,
			Timeout: cfg.Guardrails.Toxicity.OpenAITimeout,
		}
		log.Info("guardrails: toxicity OpenAI engine enabled")
	}
	// PII: native recognizers always ship; Presidio sidecar is wired only
	// when an analyzer URL is configured. Policies opt in per-tenant via
	// PIIConfig.Engine = "presidio".
	piiCfg := pii.Config{}
	if cfg.Guardrails.PII.PresidioAnalyzerURL != "" {
		piiCfg.Presidio = pii.NewPresidioEngine(pii.PresidioConfig{
			AnalyzerURL: cfg.Guardrails.PII.PresidioAnalyzerURL,
			Timeout:     cfg.Guardrails.PII.PresidioTimeout,
			Language:    cfg.Guardrails.PII.PresidioLanguage,
		})
		log.Info("guardrails: PII Presidio engine enabled", "analyzer", cfg.Guardrails.PII.PresidioAnalyzerURL)
	}
	// Per-tenant guardrails settings (currently just allow_external_engines).
	guardrailTenantPolicy := guardrails.NewSettingsTenantPolicy(db.Settings(), 30*time.Second)
	guardrailEngine := guardrails.NewEngine(guardrails.EngineConfig{
		Resolver: guardrailResolver,
		Audit:    guardrailAudit,
		Detectors: []guardrails.Detector{
			pii.NewWithConfig(piiCfg),
			injection.New(),
			topics.New(topics.Config{Embedder: embedder}),
			toxicity.New(toxCfg),
			bias.New(),
		},
		Logger:       log,
		Metrics:      metrics,
		TenantPolicy: guardrailTenantPolicy,
	})
	// Retention sweeper: deletes guardrail_logs older than N days.
	var guardrailRetention *guardrails.RetentionSweeper
	if cfg.Guardrails.AuditRetentionDays > 0 {
		guardrailRetention = guardrails.NewRetentionSweeper(db.GuardrailLogs(), log, guardrails.RetentionConfig{
			Retention: time.Duration(cfg.Guardrails.AuditRetentionDays) * 24 * time.Hour,
		})
		guardrailRetention.Start()
		log.Info("guardrails: audit retention sweeper started", "days", cfg.Guardrails.AuditRetentionDays)
	}

	limiter := limits.NewMemory(limits.MemoryConfig{
		Enabled:         cfg.Limits.Enabled,
		Window:          cfg.Limits.Window,
		CleanupInterval: cfg.Limits.CleanupInterval,
	})

	// Actionable provider health dashboard: real-traffic telemetry aggregation
	// + synthetic probe runner. Best-effort, never blocks the request path.
	healthSvc := health.New(health.Config{
		Enabled:              cfg.ProviderHealth.Enabled,
		QueueSize:            cfg.ProviderHealth.QueueSize,
		CurrentFlushInterval: cfg.ProviderHealth.CurrentFlushInterval,
		SnapshotInterval:     cfg.ProviderHealth.SnapshotInterval,
		RollingWindow:        cfg.ProviderHealth.RollingWindow,
		LatencyThresholds:    cfg.ProviderHealth.LatencyThresholds,
	}, log, db.ProviderHealth())
	probeRunner := health.NewProbeRunner(log, db.ProviderHealth(), db.Accounts(), connRegistry, v,
		cfg.ProviderHealth.LatencyThresholds, cfg.ProviderHealth.ProbeTimeout)

	pipe := pipeline.New(pipeline.Deps{
		Dispatcher:         disp,
		Meter:              mtr,
		Budget:             bud,
		Slimmer:            slim,
		Metrics:            metrics,
		Cache:              semanticCache,
		Embedder:           embedder,
		Guardrails:         guardrailEngine,
		Limiter:            limiter,
		Logger:             log,
		RequestTimeout:     cfg.Server.RequestTimeout,
		StreamStallTimeout: cfg.Server.StreamStallTimeout,
		TimeoutReader:      timeoutNotifier,
		Telemetry:          healthSvc,
	})

	// Resolve frontend dist directory. Check common install locations, then cwd.
	frontendDir := resolveFrontendDir()
	if frontendDir == "" {
		log.Warn("dashboard assets not found",
			"note", "API routes will still work, but the web dashboard will not be served")
	}

	// Tunnel managers for Cloudflare quick tunnel and Tailscale funnel.
	cfManager := cloudflare.NewManager(dataDir, cfg.Server.Port, log)
	tsManager := tailscale.NewManager(dataDir, cfg.Server.Port, log)

	// Background OAuth token keepalive. Proactively refreshes near-expiry
	// tokens every 30 minutes so requests never hit a stale token.
	keepAlive := oauth.NewKeepAlive(tokenRefresher, db.Accounts(), store.DefaultTenantID, log)

	healthChecker := healthcheck.New(healthcheck.Config{
		Enabled:              cfg.Health.Enabled,
		Interval:             cfg.Health.Interval,
		Timeout:              cfg.Health.Timeout,
		MaxParallel:          cfg.Health.MaxParallel,
		FailureThreshold:     cfg.Health.FailureThreshold,
		SuccessThreshold:     cfg.Health.SuccessThreshold,
		RecentModelWindow:    cfg.Health.RecentModelWindow,
		MaxModelsPerProvider: cfg.Health.MaxModelsPerProvider,
	}, log, db.Accounts(), db.Health(), connRegistry, v)

	reloadPricing := func(reloadCtx context.Context) error {
		mtr.ReplacePrices(buildPricing(), buildModelPrices(reloadCtx, db, log))
		updated, reloadErr := mtr.BackfillUnpriced(reloadCtx)
		if updated > 0 {
			log.Info("usage pricing reloaded and historical rows backfilled", "updated", updated)
		}
		if reloadErr != nil {
			return fmt.Errorf("reload usage pricing: %w", reloadErr)
		}
		return nil
	}

	gw := gateway.New(gateway.Deps{
		Config:               cfg,
		Logger:               log,
		Version:              version,
		Updates:              update.NewChecker(version, ""),
		DB:                   db,
		Identity:             idSvc,
		Auth:                 authSvc,
		Pipeline:             pipe,
		Conns:                connRegistry,
		Chains:               db.Chains(),
		Aliases:              db.Aliases(),
		Accounts:             db.Accounts(),
		Pools:                db.ProxyPools(),
		Budgets:              db.Budgets(),
		BudgetEngine:         bud,
		Usage:                db.Usage(),
		Resources:            db.Resources(),
		Settings:             db.Settings(),
		Vault:                v,
		Codecs:               codecs,
		Metrics:              metrics,
		FrontendDir:          frontendDir,
		DataDir:              dataDir,
		CfManager:            cfManager,
		TsManager:            tsManager,
		UsageHub:             uh,
		TimeoutNotifier:      timeoutNotifier,
		ProxyNotifier:        proxyNotifier,
		RateLimiter:          limiter,
		Refresher:            tokenRefresher,
		ReloadPricing:        reloadPricing,
		Guardrails:           guardrailEngine,
		GuardrailRepo:        db.Guardrails(),
		GuardrailLogs:        db.GuardrailLogs(),
		GuardrailHub:         guardrailLogHub,
		GuardrailTenantFlags: guardrailTenantPolicy,
		Health:               db.Health(),
		HealthChecker:        healthChecker,
		ProviderHealth:       healthSvc,
		ProbeRunner:          probeRunner,
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           gw.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: streaming responses are long-lived; the stall
		// timeout is enforced per-stream inside the connectors instead.
		// IdleTimeout reaps keep-alive client connections that went quiet so
		// abandoned SDK pools do not pin file descriptors forever.
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Auto-seed accounts for free, no-auth providers so they are immediately
	// usable without a manual "connect" step in the dashboard.
	seedFreeAccounts(ctx, db.Accounts(), log)

	return &App{cfg: cfg, log: log, db: db, accounts: db.Accounts(), server: srv, keepAlive: keepAlive, guardrailAudit: guardrailAudit, guardrailRetention: guardrailRetention, meter: mtr, healthChecker: healthChecker, providerHealth: healthSvc, probeRunner: probeRunner}, nil
}

// seedFreeAccounts auto-creates a default account for providers that are free
// and require no credentials, so they are immediately usable without a manual
// "connect" step in the dashboard. Seedable providers are derived from the
// catalog: AuthKind "none", not hidden, and not a local-only provider (must
// have a non-localhost base URL that actually points at a remote service).
func seedFreeAccounts(ctx context.Context, accounts *store.AccountRepo, log *slog.Logger) {
	// Local-only providers that should never be auto-seeded because they
	// depend on software running on the user's machine.
	localOnly := map[string]bool{
		"ollama-local": true,
		"vllm":         true,
		"sdwebui":      true,
		"comfyui":      true,
		"searxng":      true,
		"coqui":        true,
		"tortoise":     true,
		"google-tts":   true,
		"edge-tts":     true,
		"local-device": true,
	}
	for _, spec := range connectors.Catalog() {
		if spec.AuthKind != "none" || spec.Hidden || localOnly[spec.ID] {
			continue
		}
		existing, err := accounts.ListByProvider(ctx, store.DefaultTenantID, spec.ID)
		if err != nil {
			log.Warn("seed free accounts: list failed", "provider", spec.ID, "err", err)
			continue
		}
		if len(existing) > 0 {
			// Clear any lingering cooldowns from a previous session so the
			// account is immediately usable after restart.
			if err := accounts.ClearProviderCooldowns(ctx, store.DefaultTenantID, spec.ID); err != nil {
				log.Warn("seed free accounts: clear cooldowns failed", "provider", spec.ID, "err", err)
			}
			continue
		}
		now := time.Now()
		acc := store.Account{
			ID:        uuid.NewString(),
			TenantID:  store.DefaultTenantID,
			Provider:  spec.ID,
			Label:     spec.DisplayName + " (auto)",
			AuthKind:  store.AuthNone,
			Priority:  100,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := accounts.Create(ctx, acc); err != nil {
			log.Warn("seed free accounts: create failed", "provider", spec.ID, "err", err)
			continue
		}
		log.Info("auto-seeded free provider account", "provider", spec.ID, "label", acc.Label)
	}
}

// Run starts the HTTP server and blocks until ctx is cancelled, then shuts down
// gracefully.
func (a *App) Run(ctx context.Context) error {
	// Launch the OAuth keepalive loop so tokens stay fresh between requests.
	if a.keepAlive != nil {
		a.bg.Add(1)
		go func() {
			defer a.bg.Done()
			a.keepAlive.Run(ctx)
		}()
	}
	if a.meter != nil {
		a.meter.StartAsync(ctx)
	}
	if a.healthChecker != nil {
		a.bg.Add(1)
		go func() {
			defer a.bg.Done()
			a.healthChecker.Run(ctx, store.DefaultTenantID)
		}()
	}
	// Provider health telemetry aggregator (best-effort, non-blocking).
	// It manages its own goroutine; Close is called during shutdown to flush.
	if a.providerHealth != nil {
		a.providerHealth.Start(ctx)
	}

	// Background cooldown sweeper: periodically clears expired cooldowns so
	// accounts recover automatically without a restart.
	if a.accounts != nil {
		a.bg.Add(1)
		go func() {
			defer a.bg.Done()
			a.runCooldownSweeper(ctx)
		}()
	}

	errCh := make(chan error, 1)

	listeners, err := a.loopbackListeners()
	if err != nil {
		return err
	}

	for _, ln := range listeners {
		ln := ln
		go func() {
			a.log.Info("KeiRouter listening", "addr", ln.Addr().String(), "db", a.cfg.Database.Driver)
			if serveErr := a.server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
				errCh <- serveErr
			}
		}()
	}

	select {
	case <-ctx.Done():
		a.log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// A long-lived stream can outlast the grace period. That is not a
		// reason to skip draining usage, telemetry and audit queues: the
		// connections are closed forcibly and the drain still runs.
		var shutdownErr error
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			a.log.Warn("graceful shutdown exceeded grace period; closing active connections", "err", err)
			_ = a.server.Close()
			shutdownErr = err
		}
		// Wait for background workers that query the DB (oauth keepalive, health
		// checker, cooldown sweeper) to return before closing the store. They
		// observe ctx cancellation and exit promptly; the bounded wait prevents
		// a slow in-flight worker from hanging shutdown while still closing the
		// use-after-close window against db.Close() (a pure-Go SQLite handle can
		// crash if a query races a Close).
		a.waitForBackground(5 * time.Second)
		// Drain pending guardrail audit and usage rows before closing the DB.
		if a.guardrailRetention != nil {
			a.guardrailRetention.Stop(2 * time.Second)
		}
		if a.guardrailAudit != nil {
			a.guardrailAudit.Stop(5 * time.Second)
		}
		if a.meter != nil {
			a.meter.Close(a.cfg.Meter.ShutdownFlushTimeout)
		}
		if a.providerHealth != nil {
			a.providerHealth.Close(5 * time.Second)
		}
		if err := a.db.Close(); err != nil {
			return err
		}
		return shutdownErr
	case err := <-errCh:
		return err
	}
}

// loopbackListeners opens the TCP listeners the server serves on. When the
// configured host is a loopback name ("localhost"), it binds both 127.0.0.1
// and ::1 so callbacks resolve regardless of whether the OS prefers IPv4 or
// IPv6. This matters on Windows, where "localhost" usually resolves to ::1
// first — a single 127.0.0.1 listener would leave OAuth callbacks to
// http://localhost hanging. For any explicit address, a single listener on
// that address is used.
func (a *App) loopbackListeners() ([]net.Listener, error) {
	host := a.cfg.Server.Host
	port := a.cfg.Server.Port

	if host == "localhost" {
		var lns []net.Listener
		for _, ip := range []string{"127.0.0.1", "::1"} {
			addr := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				// ::1 may be unavailable on hosts with IPv6 disabled. Tolerate
				// that as long as at least one listener came up.
				if ip == "::1" && len(lns) > 0 {
					a.log.Warn("IPv6 loopback unavailable; serving IPv4 only", "err", err)
					continue
				}
				for _, opened := range lns {
					_ = opened.Close()
				}
				return nil, fmt.Errorf("app: listen %s: %w", addr, err)
			}
			lns = append(lns, ln)
		}
		return lns, nil
	}

	ln, err := net.Listen("tcp", a.cfg.Addr())
	if err != nil {
		return nil, fmt.Errorf("app: listen %s: %w", a.cfg.Addr(), err)
	}
	return []net.Listener{ln}, nil
}

// resolveDataDir returns the configured data directory, creating it if needed.
func resolveDataDir(cfg config.Config) (string, error) {
	dir := cfg.Data.Dir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("app: resolve home dir: %w", err)
		}
		dir = filepath.Join(home, ".keirouter")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("app: create data dir %q: %w", dir, err)
	}
	return dir, nil
}

// loadOrCreateMasterKey resolves the envelope-encryption root key. Precedence:
// explicit config/env value, then a persisted key file, then a freshly
// generated key written to disk with 0600 permissions.
func loadOrCreateMasterKey(cfg config.Config, dataDir string, log *slog.Logger) ([]byte, error) {
	if cfg.Security.MasterKey != "" {
		key, err := crypto.DecodeMasterKey(cfg.Security.MasterKey)
		if err != nil {
			return nil, fmt.Errorf("app: invalid configured master key: %w", err)
		}
		return key, nil
	}

	keyPath := filepath.Join(dataDir, "master.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		key, derr := crypto.DecodeMasterKey(string(data))
		if derr != nil {
			return nil, fmt.Errorf("app: corrupt master key file %q: %w", keyPath, derr)
		}
		return key, nil
	}

	key, err := crypto.GenerateMasterKey()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, []byte(crypto.EncodeMasterKey(key)), 0o600); err != nil {
		return nil, fmt.Errorf("app: persist master key: %w", err)
	}
	log.Warn("generated new master key", "path", keyPath,
		"note", "back this up; losing it makes stored credentials unrecoverable")
	return key, nil
}

// resolveFrontendDir locates the frontend dist directory. The installer and
// Docker image put assets under /usr/local/share/keirouter, while development
// builds keep them in frontend/dist.
func resolveFrontendDir() string {
	candidates := []string{
		os.Getenv("KEIROUTER_FRONTEND_DIR"),
		"/usr/local/share/keirouter/frontend/dist",
		"/usr/share/keirouter/frontend/dist",
		"/opt/keirouter/frontend/dist",
		"/usr/local/frontend/dist",
		"frontend/dist",
		"../frontend/dist",
		"../../frontend/dist",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".keirouter", "frontend", "dist"))
	}
	// Also try relative to the executable.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "frontend", "dist"),
			filepath.Join(dir, "..", "share", "keirouter", "frontend", "dist"),
			filepath.Join(dir, "..", "frontend", "dist"),
		)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

// loadCustomProviders registers user-defined dynamic provider instances and
// their custom models from the database into the in-memory connector catalog,
// so they participate in discovery and routing immediately at startup.
func loadCustomProviders(ctx context.Context, db *store.DB, log *slog.Logger) {
	repo := db.CustomProviders()
	providers, err := repo.ListProviders(ctx, store.DefaultTenantID)
	if err != nil {
		log.Warn("load custom providers failed", "err", err)
		return
	}
	for _, p := range providers {
		dialect := core.DialectOpenAI
		if p.Dialect == string(core.DialectAnthropic) {
			dialect = core.DialectAnthropic
		}
		connectors.RegisterDynamicProvider(connectors.DynamicProvider{
			ID: p.ID, DisplayName: p.DisplayName, Alias: p.Alias, Dialect: dialect, BaseURL: p.BaseURL,
		})
	}

	models, err := repo.ListModels(ctx, store.DefaultTenantID)
	if err != nil {
		log.Warn("load custom models failed", "err", err)
		return
	}
	byProvider := map[string][]connectors.ModelSpec{}
	for _, m := range models {
		kind := core.ServiceKind(m.Kind)
		if kind == "" {
			kind = core.ServiceLLM
		}
		byProvider[m.ProviderID] = append(byProvider[m.ProviderID], connectors.ModelSpec{
			ID: m.ModelID, Name: m.DisplayName, Kind: kind,
		})
	}
	for providerID, specs := range byProvider {
		connectors.SetDynamicModels(providerID, specs)
	}
	if len(providers) > 0 || len(models) > 0 {
		log.Info("loaded custom providers", "providers", len(providers), "models", len(models))
	}
}

// buildPricing projects the connector catalog into a meter pricing table.
func buildPricing() map[string]meter.Price {

	specs := connectors.Catalog()
	prices := make([]meter.SpecPrice, 0, len(specs))
	for _, s := range specs {
		prices = append(prices, meter.SpecPrice{ID: s.ID, InputPerM: s.InputPerM, OutputPerM: s.OutputPerM})
	}
	return meter.PricingFromCatalog(prices)
}

// cooldownSweepInterval is how often the background sweeper checks for expired
// cooldowns. Short enough to recover within seconds; long enough to be trivial.
const cooldownSweepInterval = 15 * time.Second

// waitForBackground blocks until all tracked background workers have returned
// or the timeout elapses, whichever comes first. Bounding the wait keeps
// shutdown responsive even if a worker is stuck in a slow network call.
func (a *App) waitForBackground(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		a.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		a.log.Warn("background workers did not stop within timeout; proceeding with shutdown")
	}
}

// runCooldownSweeper periodically clears expired cooldowns so accounts auto-
// recover after rate-limit / auth errors without requiring a restart.
func (a *App) runCooldownSweeper(ctx context.Context) {
	ticker := time.NewTicker(cooldownSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := a.accounts.ClearExpiredCooldowns(ctx)
			if err != nil {
				a.log.Debug("cooldown sweep failed", "err", err)
			} else if n > 0 {
				a.log.Info("sweeper cleared expired cooldowns", "count", n)
			}
		}
	}
}

// buildModelPrices builds the per-model pricing table from the connector model prices.
func buildModelPrices(ctx context.Context, db *store.DB, log *slog.Logger) map[string]meter.Price {
	out := make(map[string]meter.Price)
	for _, mp := range connectors.ModelPricingTable() {
		source := mp.Source
		if source == "" {
			source = "catalog"
		}
		estimated := mp.Estimated || mp.Provider == "kiro"
		out[mp.Provider+"/"+mp.Model] = meter.Price{
			InputPerM: mp.InputPerM, OutputPerM: mp.OutputPerM,
			CachedInputPerM: mp.CachedInputPerM, CacheWritePerM: mp.CacheWritePerM,
			ReasoningPerM:        mp.ReasoningPerM,
			LongContextThreshold: mp.LongContextThreshold,
			LongInputPerM:        mp.LongInputPerM, LongOutputPerM: mp.LongOutputPerM,
			LongCachedInputPerM: mp.LongCachedInputPerM, LongCacheWritePerM: mp.LongCacheWritePerM,
			Source: source, SourceURL: mp.SourceURL, Estimated: estimated, ExplicitFree: mp.ExplicitFree,
		}
	}
	// User-entered custom prices are the highest-priority exact match. Imported
	// zero values mean "unknown", not free, and intentionally do not override a
	// canonical vendor catalog match.
	models, err := db.CustomProviders().ListModels(ctx, store.DefaultTenantID)
	if err != nil {
		log.Warn("load custom model prices failed", "err", err)
		return out
	}
	for _, model := range models {
		if model.InputPerM <= 0 && model.OutputPerM <= 0 {
			continue
		}
		out[model.ProviderID+"/"+model.ModelID] = meter.Price{
			InputPerM: model.InputPerM, OutputPerM: model.OutputPerM,
			CachedInputPerM: model.InputPerM, CacheWritePerM: model.InputPerM,
			ReasoningPerM: model.OutputPerM, Source: "custom", Estimated: false,
		}
	}
	return out
}
