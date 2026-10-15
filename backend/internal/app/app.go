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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/mydisha/keirouter/backend/internal/audit"
	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/cache"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/consolelog"
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
	"github.com/mydisha/keirouter/backend/internal/market"
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
	reloadPricing      func(context.Context) error
	marketURL          string
	surplusURL         string
	marketCache        *market.SnapshotCache
	audit              *audit.Recorder
	auditSweeper       *audit.Sweeper
	// sharedState releases the redis limiter/budget resources; nil with the
	// memory backend.
	sharedState func()

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

	auditRec, auditSweeper := buildAudit(cfg, db, masterKey, version, log)

	// Construct services.
	v := vault.New(sealer)
	connRegistry := connectors.DefaultRegistry()
	// Load user-defined dynamic custom providers and their custom models from
	// the database so they are routable and discoverable immediately at startup.
	loadCustomProviders(ctx, db, log)
	codecs := transform.DefaultRegistry()

	idSvc := identity.New(db.APIKeys())
	idSvc.SetKeyPrefix(gateway.LoadAPIKeyPrefix(ctx, db.Settings()))

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
	if auditRec.ExportsRequests() {
		mtr.AddRecordObserver(auditRec.ObserveUsage)
	}
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
	oauth.SetGlobalProxy(proxyNotifier)

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
	// Prompt injection: pattern layers always run. The ML classifier needs a
	// sidecar URL, the semantic layer a real embeddings API (the hashing
	// fallback can't measure meaning), and the LLM judge is attached once the
	// gateway exists because it routes through KeiRouter's own providers.
	injCfg := injection.Config{Logger: log}
	if cfg.Guardrails.Injection.ClassifierURL != "" {
		injCfg.Classifier = injection.NewClassifier(injection.ClassifierConfig{
			URL:     cfg.Guardrails.Injection.ClassifierURL,
			Timeout: cfg.Guardrails.Injection.ClassifierTimeout,
			Labels:  cfg.Guardrails.Injection.ClassifierLabels,
			APIKey:  cfg.Guardrails.Injection.ClassifierAPIKey,
		})
		log.Info("guardrails: injection classifier enabled", "url", cfg.Guardrails.Injection.ClassifierURL)
	}
	if cfg.Cache.EmbeddingProvider == "api" && cfg.Cache.EmbeddingAPIKey != "" {
		injCfg.Embedder = embedder
	}
	injectionDetector := injection.NewWithConfig(injCfg)
	// Topics: the embedding engine only makes sense with a real embeddings
	// API; the LLM engine is attached with the gateway below.
	var topicsEmbedder topics.Embedder
	if cfg.Cache.EmbeddingProvider == "api" && cfg.Cache.EmbeddingAPIKey != "" {
		topicsEmbedder = embedder
	}
	topicsDetector := topics.New(topics.Config{Embedder: topicsEmbedder})
	// Per-tenant guardrails settings (currently just allow_external_engines).
	guardrailTenantPolicy := guardrails.NewSettingsTenantPolicy(db.Settings(), 30*time.Second)
	guardrailEngine := guardrails.NewEngine(guardrails.EngineConfig{
		Resolver: guardrailResolver,
		Audit:    guardrailAudit,
		Detectors: []guardrails.Detector{
			pii.NewWithConfig(piiCfg),
			injectionDetector,
			topicsDetector,
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

	// Tunable options start from config.yaml; the gateway overlays any
	// dashboard overrides when it is constructed.
	limiter, sharedState, err := buildLimiter(ctx, cfg, bud, mtr, log)
	if err != nil {
		return nil, err
	}

	// Actionable provider health dashboard: real-traffic telemetry aggregation
	// + synthetic probe runner. Best-effort, never blocks the request path.
	// Dashboard console: background components mirror their warnings into it
	// so operators see refresh failures, health and tunnel problems next to
	// request traffic.
	conLog := consolelog.New()
	sysLog := func(source string) *slog.Logger { return consolelog.Tee(log, conLog, slog.LevelWarn, source) }

	healthSvc := health.New(health.Config{
		Enabled:              cfg.ProviderHealth.Enabled,
		QueueSize:            cfg.ProviderHealth.QueueSize,
		CurrentFlushInterval: cfg.ProviderHealth.CurrentFlushInterval,
		SnapshotInterval:     cfg.ProviderHealth.SnapshotInterval,
		RollingWindow:        cfg.ProviderHealth.RollingWindow,
		LatencyThresholds:    cfg.ProviderHealth.LatencyThresholds,
	}, sysLog("health"), db.ProviderHealth())
	probeRunner := health.NewProbeRunner(sysLog("health"), db.ProviderHealth(), db.Accounts(), connRegistry, v,
		cfg.ProviderHealth.LatencyThresholds, cfg.ProviderHealth.ProbeTimeout)
	probeRunner.SetCredentialPreparer(disp)

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
	cfManager := cloudflare.NewManager(dataDir, cfg.Server.Port, sysLog("tunnel"))
	tsManager := tailscale.NewManager(dataDir, cfg.Server.Port, sysLog("tunnel"))

	// Background OAuth token keepalive. Proactively refreshes near-expiry
	// tokens every 30 minutes so requests never hit a stale token.
	keepAlive := oauth.NewKeepAlive(tokenRefresher, db.Accounts(), store.DefaultTenantID, sysLog("oauth"))
	keepAlive.SetEgress(disp.EgressContext)

	healthChecker := healthcheck.New(healthcheck.Config{
		Enabled:              cfg.Health.Enabled,
		Interval:             cfg.Health.Interval,
		Timeout:              cfg.Health.Timeout,
		MaxParallel:          cfg.Health.MaxParallel,
		FailureThreshold:     cfg.Health.FailureThreshold,
		SuccessThreshold:     cfg.Health.SuccessThreshold,
		RecentModelWindow:    cfg.Health.RecentModelWindow,
		MaxModelsPerProvider: cfg.Health.MaxModelsPerProvider,
	}, sysLog("health"), db.Accounts(), db.Health(), connRegistry, v)
	healthChecker.SetCredentialPreparer(disp.PrepareCredentials)

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

	marketCache := market.NewSnapshotCache()

	app := &App{cfg: cfg, log: log, db: db, accounts: db.Accounts(), keepAlive: keepAlive, guardrailAudit: guardrailAudit, guardrailRetention: guardrailRetention, meter: mtr, healthChecker: healthChecker, providerHealth: healthSvc, probeRunner: probeRunner, reloadPricing: reloadPricing, marketURL: "", surplusURL: "", marketCache: marketCache}

	gw := gateway.New(gateway.Deps{
		Config:                 cfg,
		Logger:                 log,
		ConsoleLog:             conLog,
		Version:                version,
		Updates:                update.NewChecker(version, ""),
		DB:                     db,
		Identity:               idSvc,
		Auth:                   authSvc,
		Audit:                  auditRec,
		Pipeline:               pipe,
		Conns:                  connRegistry,
		Chains:                 db.Chains(),
		Aliases:                db.Aliases(),
		Accounts:               db.Accounts(),
		Pools:                  db.ProxyPools(),
		Budgets:                db.Budgets(),
		BudgetEngine:           bud,
		Usage:                  db.Usage(),
		Resources:              db.Resources(),
		Settings:               db.Settings(),
		Vault:                  v,
		Codecs:                 codecs,
		Metrics:                metrics,
		FrontendDir:            frontendDir,
		DataDir:                dataDir,
		CfManager:              cfManager,
		TsManager:              tsManager,
		UsageHub:               uh,
		TimeoutNotifier:        timeoutNotifier,
		ProxyNotifier:          proxyNotifier,
		RateLimiter:            limiter,
		Refresher:              tokenRefresher,
		ReloadPricing:          reloadPricing,
		SyncMarketPrices:       app.syncChainMarketPrices,
		MarketCache:            app.MarketCache(),
		Guardrails:             guardrailEngine,
		GuardrailRepo:          db.Guardrails(),
		GuardrailLogs:          db.GuardrailLogs(),
		GuardrailHub:           guardrailLogHub,
		GuardrailTenantFlags:   guardrailTenantPolicy,
		GuardrailRetentionDays: cfg.Guardrails.AuditRetentionDays,
		Health:                 db.Health(),
		HealthChecker:          healthChecker,
		ProviderHealth:         healthSvc,
		ProbeRunner:            probeRunner,
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

	// The LLM judges (prompt injection, topics) route through the gateway's
	// own model resolution, so they can only be attached now.
	injectionDetector.SetCompleter(gw)
	topicsDetector.SetCompleter(gw)

	// Auto-seed accounts for free, no-auth providers so they are immediately
	// usable without a manual "connect" step in the dashboard.
	seedFreeAccounts(ctx, db.Accounts(), log)

	app = &App{cfg: cfg, log: log, db: db, accounts: db.Accounts(), server: srv, keepAlive: keepAlive, guardrailAudit: guardrailAudit, guardrailRetention: guardrailRetention, meter: mtr, healthChecker: healthChecker, providerHealth: healthSvc, probeRunner: probeRunner, reloadPricing: reloadPricing, marketURL: "", surplusURL: "", marketCache: marketCache, audit: auditRec, auditSweeper: auditSweeper, sharedState: sharedState}

	return app, nil
}

// keyLimiter is the per-key limiter surface the pipeline and gateway need.
type keyLimiter interface {
	limits.Limiter
	gateway.RateLimitController
}

// buildLimiter builds the per-key limiter. With limits.backend=redis it also
// moves budget spend onto shared redis counters, so every replica behind a
// load balancer enforces the same RPM/TPM windows, concurrency pool and
// budgets. The returned closer releases the redis resources (nil for memory).
func buildLimiter(ctx context.Context, cfg config.Config, bud *budget.Engine, mtr *meter.Meter, log *slog.Logger) (keyLimiter, func(), error) {
	opts := limits.Options{
		ReconcileTokens:       cfg.Limits.ReconcileTokens,
		OutputTokenFloor:      cfg.Limits.OutputTokenFloor,
		OutputReserveCap:      cfg.Limits.OutputReserveCap,
		ConcurrencyRetryAfter: cfg.Limits.ConcurrencyRetryAfter,
	}
	if cfg.Limits.Backend != "redis" {
		return limits.NewMemory(limits.MemoryConfig{
			Enabled:         cfg.Limits.Enabled,
			Window:          cfg.Limits.Window,
			CleanupInterval: cfg.Limits.CleanupInterval,
			Options:         opts,
		}), nil, nil
	}

	ropts, err := redis.ParseURL(cfg.Limits.RedisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("app: limits.redis_url: %w", err)
	}
	// Honour the per-call deadlines on the request path instead of the
	// client's multi-second socket timeouts.
	ropts.ContextTimeoutEnabled = true
	client := redis.NewClient(ropts)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		// Not fatal: requests follow limits.fail_mode until redis answers.
		log.Warn("limits: redis unreachable at startup", "addr", ropts.Addr, "fail_mode", cfg.Limits.FailMode, "err", err)
	}

	rl := limits.NewRedis(limits.RedisConfig{
		Client:   client,
		Enabled:  cfg.Limits.Enabled,
		Window:   cfg.Limits.Window,
		Options:  opts,
		LeaseTTL: cfg.Limits.LeaseTTL,
		Timeout:  cfg.Limits.RedisTimeout,
		FailMode: cfg.Limits.FailMode,
		Logger:   log,
	})
	bud.EnableSharedCounter(budget.NewRedisCounter(client, cfg.Limits.RedisTimeout, log), cfg.Limits.BudgetCounterTTL, log)
	mtr.AddRecordObserver(bud.ObserveUsage)
	log.Info("limits: shared redis backend (rate limits, concurrency, budgets)",
		"addr", ropts.Addr, "fail_mode", cfg.Limits.FailMode)
	return rl, func() {
		bud.Close()
		rl.Close()
		_ = client.Close()
	}, nil
}

// buildAudit starts the audit recorder (and its OTLP exporter and retention
// sweeper) when audit.enabled is set; both results are nil otherwise.
func buildAudit(cfg config.Config, db *store.DB, masterKey []byte, version string, log *slog.Logger) (*audit.Recorder, *audit.Sweeper) {
	if !cfg.Audit.Enabled {
		return nil, nil
	}
	instanceID := audit.NewInstanceID()
	var exp audit.Exporter
	otlpCfg, ok, err := audit.ResolveOTLPConfig(cfg.Audit.OTLP, version, instanceID, os.Getenv)
	switch {
	case err != nil:
		log.Warn("audit: OTLP export disabled", "err", err)
	case ok:
		otlpCfg.Logger = log
		exp = audit.NewOTLPExporter(otlpCfg)
		log.Info("audit: exporting events as OTLP logs", "endpoint", otlpCfg.URL)
	}
	var requests audit.UsageExporter
	if exp != nil && cfg.Audit.OTLP.IncludeRequests {
		requests = audit.NewOTLPExporter(audit.RequestExporterConfig(otlpCfg))
		log.Info("audit: exporting every gateway request as OTLP logs", "endpoint", otlpCfg.URL)
	} else if cfg.Audit.OTLP.IncludeRequests {
		log.Warn("audit: include_requests is set but no OTLP endpoint is configured; requests are not exported")
	}
	rec := audit.New(db.Audit(), exp, audit.Config{
		BufferSize:      cfg.Audit.BufferSize,
		Key:             audit.DeriveKey(masterKey),
		InstanceID:      instanceID,
		DefaultTenantID: store.DefaultTenantID,
		Requests:        requests,
		Logger:          log,
	})
	rec.Record(audit.Event{
		Action: "system.start", ActorType: audit.ActorSystem, ActorID: "system", ResourceType: "system",
		Details: map[string]string{
			"version":        version,
			"instance_id":    instanceID,
			"database":       cfg.Database.Driver,
			"limits_backend": cfg.Limits.Backend,
		},
	})
	sweeper := audit.NewSweeper(db.Audit(), log, time.Duration(cfg.Audit.RetentionDays)*24*time.Hour)
	sweeper.Start()
	return rec, sweeper
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
	if a.db != nil {
		a.bg.Add(1)
		go func() {
			defer a.bg.Done()
			a.runMarketSync(ctx)
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
		a.auditSweeper.Stop(2 * time.Second)
		a.audit.Record(audit.Event{Action: "system.stop", ActorType: audit.ActorSystem, ActorID: "system", ResourceType: "system"})
		a.audit.Stop(5 * time.Second)
		if a.sharedState != nil {
			a.sharedState()
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
		// A release may add a built-in provider whose ID or alias matches an
		// existing custom alias. The custom provider keeps the alias (see
		// connectors.SpecByAlias); say so, since the built-in is then only
		// reachable by renaming the custom alias.
		if b, ok := connectors.BuiltinSpecByAlias(p.Alias); ok && p.Alias != "" {
			log.Warn("custom provider alias shadows a built-in provider; "+p.Alias+"/<model> keeps routing to the custom provider. Rename the custom alias to use the built-in.",
				"custom_provider", p.DisplayName, "alias", p.Alias, "builtin_provider", b.ID)
		}
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

// applyProviderPriceDefaults fills price classes the catalogue entries do not
// spell out but the provider bills: Anthropic 1h cache writes at 2× input and
// web search at $10 per 1,000 searches; Gemini grounding at $35 per 1,000
// grounded prompts. Explicit catalogue values win.
func applyProviderPriceDefaults(provider, model string, p *meter.Price) {
	switch provider {
	case "anthropic", "claude", "claude-code":
		if p.CacheWrite1hPerM == 0 && p.InputPerM > 0 {
			p.CacheWrite1hPerM = p.InputPerM * 2
		}
		if p.WebSearchPerK == 0 && strings.HasPrefix(strings.ToLower(model), "claude") {
			p.WebSearchPerK = 10
		}
	case "gemini", "gemini-cli", "vertex":
		if p.WebSearchPerK == 0 && strings.HasPrefix(strings.ToLower(model), "gemini") {
			p.WebSearchPerK = 35
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
		price := meter.Price{
			InputPerM: mp.InputPerM, OutputPerM: mp.OutputPerM,
			CachedInputPerM: mp.CachedInputPerM, CacheWritePerM: mp.CacheWritePerM,
			ReasoningPerM: mp.ReasoningPerM, CacheWrite1hPerM: mp.CacheWrite1hPerM, WebSearchPerK: mp.WebSearchPerK,
			LongContextThreshold: mp.LongContextThreshold,
			LongInputPerM:        mp.LongInputPerM, LongOutputPerM: mp.LongOutputPerM,
			LongCachedInputPerM: mp.LongCachedInputPerM, LongCacheWritePerM: mp.LongCacheWritePerM,
			Source: source, SourceURL: mp.SourceURL, Estimated: estimated, ExplicitFree: mp.ExplicitFree,
		}
		applyProviderPriceDefaults(mp.Provider, mp.Model, &price)
		out[mp.Provider+"/"+mp.Model] = price
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
