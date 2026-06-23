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
	"time"

	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/cache"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/gateway"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/guardrails/bias"
	"github.com/mydisha/keirouter/backend/internal/guardrails/injection"
	"github.com/mydisha/keirouter/backend/internal/guardrails/pii"
	"github.com/mydisha/keirouter/backend/internal/guardrails/topics"
	"github.com/mydisha/keirouter/backend/internal/guardrails/toxicity"
	"github.com/mydisha/keirouter/backend/internal/headroom"
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
}

// Build constructs the application from configuration. It opens the database,
// applies migrations, initializes the crypto root, and wires the gateway.
func Build(ctx context.Context, cfg config.Config, log *slog.Logger, version string) (*App, error) {
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
	modelPrices := buildModelPrices()
	mtr := meter.New(db.Usage(), pricing, modelPrices)
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

	// Timeout notifier: atomic cache of timeout values that can be updated
	// at runtime from the dashboard settings without restarting.
	timeoutNotifier := gateway.NewTimeoutNotifier(
		cfg.Server.StreamStallTimeout,
		30*time.Second, // ResponseHeaderTimeout fallback (from transport)
		cfg.Server.RequestTimeout,
	)

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

	pipe := pipeline.New(pipeline.Deps{
		Dispatcher:         disp,
		Meter:              mtr,
		Budget:             bud,
		Slimmer:            slim,
		Headroom:           headroom.New(),
		Metrics:            metrics,
		Cache:              semanticCache,
		Embedder:           embedder,
		Guardrails:         guardrailEngine,
		Limiter:            limiter,
		Logger:             log,
		RequestTimeout:     cfg.Server.RequestTimeout,
		StreamStallTimeout: cfg.Server.StreamStallTimeout,
		TimeoutReader:      timeoutNotifier,
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

	gw := gateway.New(gateway.Deps{
		Config:          cfg,
		Logger:          log,
		Version:         version,
		Updates:         update.NewChecker(version, ""),
		DB:              db,
		Identity:        idSvc,
		Auth:            authSvc,
		Pipeline:        pipe,
		Conns:           connRegistry,
		Chains:          db.Chains(),
		Aliases:         db.Aliases(),
		Accounts:        db.Accounts(),
		Pools:           db.ProxyPools(),
		Budgets:         db.Budgets(),
		BudgetEngine:    bud,
		Usage:           db.Usage(),
		Resources:       db.Resources(),
		Settings:        db.Settings(),
		Vault:           v,
		Codecs:          codecs,
		Metrics:         metrics,
		FrontendDir:     frontendDir,
		DataDir:         dataDir,
		CfManager:       cfManager,
		TsManager:       tsManager,
		UsageHub:        uh,
		TimeoutNotifier: timeoutNotifier,
		ProxyNotifier:   proxyNotifier,
		RateLimiter:     limiter,
		Refresher:       tokenRefresher,
		Guardrails:           guardrailEngine,
		GuardrailRepo:        db.Guardrails(),
		GuardrailLogs:        db.GuardrailLogs(),
		GuardrailHub:         guardrailLogHub,
		GuardrailTenantFlags: guardrailTenantPolicy,
		Health:          db.Health(),
		HealthChecker:   healthChecker,
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           gw.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: streaming responses are long-lived; the stall
		// timeout is enforced per-stream inside the connectors instead.
	}

	// Auto-seed accounts for free, no-auth providers so they are immediately
	// usable without a manual "connect" step in the dashboard.
	seedFreeAccounts(ctx, db.Accounts(), log)

	return &App{cfg: cfg, log: log, db: db, accounts: db.Accounts(), server: srv, keepAlive: keepAlive, guardrailAudit: guardrailAudit, guardrailRetention: guardrailRetention, meter: mtr, healthChecker: healthChecker}, nil
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
		go a.keepAlive.Run(ctx)
	}
	if a.meter != nil {
		a.meter.StartAsync(ctx)
	}
	if a.healthChecker != nil {
		go a.healthChecker.Run(ctx, store.DefaultTenantID)
	}

	// Background cooldown sweeper: periodically clears expired cooldowns so
	// accounts recover automatically without a restart.
	if a.accounts != nil {
		go a.runCooldownSweeper(ctx)
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
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return err
		}
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
		return a.db.Close()
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
func buildModelPrices() map[string]meter.Price {
	out := make(map[string]meter.Price)
	for _, mp := range connectors.ModelPricingTable() {
		out[mp.Provider+"/"+mp.Model] = meter.Price{
			InputPerM:       mp.InputPerM,
			OutputPerM:      mp.OutputPerM,
			CachedInputPerM: mp.CachedInputPerM,
			CacheWritePerM:  mp.CacheWritePerM,
			ReasoningPerM:   mp.ReasoningPerM,
		}
	}
	return out
}
