// Package gateway is the HTTP edge of KeiRouter. It authenticates inbound
// requests, parses them with the client's dialect codec, resolves a routing
// chain, runs the pipeline, and renders the response (unary or streaming) back
// in the client's dialect.
package gateway

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mydisha/keirouter/backend/internal/auth"
	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/clitools"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/consolelog"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/fastjson"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
	"github.com/mydisha/keirouter/backend/internal/health"
	"github.com/mydisha/keirouter/backend/internal/healthcheck"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/oauth"
	"github.com/mydisha/keirouter/backend/internal/observ"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/transform"
	"github.com/mydisha/keirouter/backend/internal/tunnel/cloudflare"
	"github.com/mydisha/keirouter/backend/internal/tunnel/tailscale"
	"github.com/mydisha/keirouter/backend/internal/update"
	"github.com/mydisha/keirouter/backend/internal/usagehub"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// Server holds the gateway's dependencies and HTTP routes.
type Server struct {
	cfg             config.Config
	log             *slog.Logger
	db              *store.DB
	identity        *identity.Service
	auth            *auth.Service
	pipeline        *pipeline.Pipeline
	conns           *connectors.Registry
	chains          *store.ChainRepo
	aliases         *store.AliasRepo
	accounts        *store.AccountRepo
	pools           *store.ProxyPoolRepo
	budgets         *store.BudgetRepo
	budgetEngine    *budget.Engine
	usage           *store.UsageRepo
	resources       *store.ResourceRepo
	settings        *store.SettingsRepo
	vault           *vault.Vault
	codecs          *transform.Registry
	metrics         *observ.Metrics
	consoleLog      *consolelog.Buffer
	cliTools        *clitools.Registry
	cliToolHome     string
	frontendDir     string
	dataDir         string
	oauthSessions   *oauth.SessionStore
	cfManager       *cloudflare.Manager
	tsManager       *tailscale.Manager
	usageHub        *usagehub.Hub
	timeoutNotifier *TimeoutNotifier
	proxyNotifier   *ProxyNotifier
	rateLimiter     interface{ SetEnabled(bool) }
	refresher       dispatch.TokenRefresher
	version         string
	updates         *update.Checker
	insightsCache   *ttlCache
	guardrails          *guardrails.Engine
	guardrailRepo       *store.GuardrailRepo
	guardrailLogs       *store.GuardrailLogRepo
	guardrailHub        *guardrails.LogHub
	guardrailTenantFlag *guardrails.SettingsTenantPolicy
	guardrailTestRL     *guardrailTestRateLimiter
	health          *store.HealthRepo
	healthChecker   *healthcheck.Checker
	providerHealth  *health.Service
	probeRunner     *health.ProbeRunner
	router          chi.Router
}

// Deps bundles the gateway's collaborators.
type Deps struct {
	Config          config.Config
	Logger          *slog.Logger
	Version         string
	Updates         *update.Checker
	DB              *store.DB
	Identity        *identity.Service
	Auth            *auth.Service
	Pipeline        *pipeline.Pipeline
	Conns           *connectors.Registry
	Chains          *store.ChainRepo
	Aliases         *store.AliasRepo
	Accounts        *store.AccountRepo
	Pools           *store.ProxyPoolRepo
	Budgets         *store.BudgetRepo
	BudgetEngine    *budget.Engine
	Usage           *store.UsageRepo
	Resources       *store.ResourceRepo
	Settings        *store.SettingsRepo
	Vault           *vault.Vault
	Codecs          *transform.Registry
	Metrics         *observ.Metrics
	ConsoleLog      *consolelog.Buffer
	CLITools        *clitools.Registry
	CLITHome        string
	FrontendDir     string
	DataDir         string
	CfManager       *cloudflare.Manager
	TsManager       *tailscale.Manager
	UsageHub        *usagehub.Hub
	TimeoutNotifier *TimeoutNotifier
	ProxyNotifier   *ProxyNotifier
	RateLimiter     interface{ SetEnabled(bool) }
	Refresher       dispatch.TokenRefresher
	Guardrails           *guardrails.Engine
	GuardrailRepo        *store.GuardrailRepo
	GuardrailLogs        *store.GuardrailLogRepo
	GuardrailHub         *guardrails.LogHub
	GuardrailTenantFlags *guardrails.SettingsTenantPolicy
	Health          *store.HealthRepo
	HealthChecker   *healthcheck.Checker
	ProviderHealth  *health.Service
	ProbeRunner     *health.ProbeRunner
}

// New builds a gateway Server and wires its routes.
func New(d Deps) *Server {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	cliTools := d.CLITools
	if cliTools == nil {
		cliTools = clitools.NewRegistry()
	}
	cliToolHome := d.CLITHome
	if cliToolHome == "" {
		cliToolHome, _ = os.UserHomeDir()
	}
	conLog := d.ConsoleLog
	if conLog == nil {
		conLog = consolelog.New()
	}
	s := &Server{
		cfg:             d.Config,
		log:             log,
		db:              d.DB,
		identity:        d.Identity,
		auth:            d.Auth,
		pipeline:        d.Pipeline,
		conns:           d.Conns,
		chains:          d.Chains,
		aliases:         d.Aliases,
		accounts:        d.Accounts,
		pools:           d.Pools,
		budgets:         d.Budgets,
		budgetEngine:    d.BudgetEngine,
		usage:           d.Usage,
		resources:       d.Resources,
		settings:        d.Settings,
		vault:           d.Vault,
		codecs:          d.Codecs,
		metrics:         d.Metrics,
		consoleLog:      conLog,
		cliTools:        cliTools,
		cliToolHome:     cliToolHome,
		frontendDir:     d.FrontendDir,
		dataDir:         d.DataDir,
		oauthSessions:   oauth.NewSessionStore(),
		cfManager:       d.CfManager,
		tsManager:       d.TsManager,
		usageHub:        d.UsageHub,
		timeoutNotifier: d.TimeoutNotifier,
		proxyNotifier:   d.ProxyNotifier,
		rateLimiter:     d.RateLimiter,
		refresher:       d.Refresher,
		version:         d.Version,
		updates:         d.Updates,
		insightsCache:   newTTLCache(insightsCacheTTL),
		guardrails:          d.Guardrails,
		guardrailRepo:       d.GuardrailRepo,
		guardrailLogs:       d.GuardrailLogs,
		guardrailHub:        d.GuardrailHub,
		guardrailTenantFlag: d.GuardrailTenantFlags,
		guardrailTestRL:     newGuardrailTestRateLimiter(10, time.Minute),
		health:          d.Health,
		healthChecker:   d.HealthChecker,
		providerHealth:  d.ProviderHealth,
		probeRunner:     d.ProbeRunner,
	}
	s.router = s.routes()
	startSystemCollector(d.Resources)
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	// Collapse a duplicated /v1/v1 prefix before routing. Must run first so the
	// rewritten path reaches the real /v1/* routes below.
	r.Use(collapseDoubleV1)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: s.cfg.Server.CORSOrigins,
		AllowedMethods: []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type", "x-api-key", "X-KeiRouter-Affinity", "X-Conversation-ID", "X-Thread-ID", "X-Session-ID", "OpenAI-Conversation-ID"},
	}))

	// Health check (unauthenticated).
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Version / info endpoint (unauthenticated).
	r.Get("/v1", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"name":    "KeiRouter",
			"version": s.versionString(),
			"status":  "ok",
			"endpoints": []string{
				"/v1/chat/completions",
				"/v1/messages",
				"/v1/responses",
				"/v1/models",
				"/v1/embeddings",
				"/v1/images/generations",
				"/v1/audio/speech",
				"/v1/audio/transcriptions",
				"/v1/search",
				"/v1/web/fetch",
			},
		})
	})

	// Prometheus metrics endpoint. Loopback-guarded by default to avoid leaking
	// operational telemetry; expose deliberately behind a scraper's network.
	if s.metrics != nil {
		r.Group(func(r chi.Router) {
			r.Use(s.loopbackOnly)
			r.Handle("/metrics", promhttp.HandlerFor(s.metrics.Registry(), promhttp.HandlerOpts{}))
		})
	}

	// OpenAI-compatible API surface (authenticated).
	r.Group(func(r chi.Router) {
		// Cap concurrent in-flight requests to prevent resource exhaustion
		// under high traffic. Each request holds argon2 goroutines, SQLite
		// connection time, upstream connections, and buffer pool slots.
		maxConc := s.cfg.Server.MaxConcurrentRequests
		if maxConc <= 0 {
			maxConc = 100 // sensible default for AI gateway workloads
		}
		r.Use(concurrencyLimiter(maxConc))
		r.Use(s.authMiddleware)
		r.Post("/v1/chat/completions", s.handleOpenAIChat)
		r.Post("/v1/messages", s.handleAnthropicMessages)
		// Anthropic clients (Claude Code) call count_tokens before each turn to
		// size context. Served locally — not all upstreams expose it.
		r.Post("/v1/messages/count_tokens", s.handleAnthropicCountTokens)

		// OpenAI Responses API surface (Codex and Responses-native clients).
		r.Post("/v1/responses", s.handleOpenAIResponses)

		// Gemini-native generateContent endpoint. The model + action are in the
		// URL path ({model}:generateContent), matching Google's SDK clients.
		r.Post("/v1beta/models/{modelAction}", s.handleGeminiGenerate)

		// Multi-capability endpoints (Phase 2).
		r.Post("/v1/embeddings", s.handleEmbeddings)
		r.Post("/v1/images/generations", s.handleImageGeneration)
		r.Post("/v1/audio/speech", s.handleAudioSpeech)
		r.Post("/v1/audio/transcriptions", s.handleAudioTranscription)
		r.Post("/v1/search", s.handleWebSearch)
		r.Post("/v1/web/fetch", s.handleWebFetch)

		// Model discovery.
		r.Get("/v1/models", s.handleListModels)
		r.Get("/v1/models/info", s.handleModelInfo)
		r.Get("/v1/models/{kind}", s.handleListModelsByKind)

		// Self-service key usage (token budget + cost trace).
		r.Get("/v1/keys/me/usage", s.handleKeyUsage)
	})

	// Public portal endpoints (no auth required)
	r.Get("/v1/portal/keys/{id}/usage", s.handlePortalKeyUsage)
	r.Get("/v1/portal/branding", s.portalBranding)

	// Dashboard auth endpoints (login/logout/status) are loopback-guarded but
	// do not require a session — they are how a session is obtained.
	// Login has rate limiting to prevent brute force attacks.
	r.Route("/api/auth", func(r chi.Router) {
		r.Use(s.loopbackOnly)
		// Apply rate limiting to login endpoint
		r.Group(func(r chi.Router) {
			r.Use(s.loginRateLimiter)
			r.Post("/login", s.handleLogin)
		})
		r.Post("/logout", s.handleLogout)
		r.Get("/status", s.handleAuthStatus)
		r.Group(func(pr chi.Router) {
			pr.Use(s.sessionMiddleware)
			s.mountAuthenticatedAuth(pr)
		})
	})

	// Dashboard admin API. Guarded by loopback access AND a valid dashboard
	// session, so provider credentials and routing config are never exposed to
	// an unauthenticated caller, even on localhost.
	r.Route("/api", func(r chi.Router) {
		r.Use(s.loopbackOnly)
		r.Use(s.sessionMiddleware)
		s.mountAdmin(r)
	})

	// OAuth provider redirects (GET) land here. They MUST work without a
	// dashboard session — state is the CSRF guard — and without depending on
	// the frontend asset directory. The handler writes a self-contained HTML
	// page that postMessages the opener and closes the popup, so the same
	// route works whether or not the dashboard SPA is bundled with the binary.
	r.Get("/oauth/callback", s.oauthCallback)
	r.Get("/auth/callback", s.oauthCallback)

	// Kimchi browser-callback endpoint. Receives a redirect from
	// app.kimchi.dev/cli-auth with the token on the query string.
	// Must be outside session middleware (external browser redirect,
	// no dashboard cookie). The path matches the callback URL built
	// in oauth.KimchiStartAuth (callbackBase + "/api/kimchi/callback").
	r.Get("/api/kimchi/callback", s.kimchiCallback)

	// Serve frontend static files. The dashboard is a Vite SPA; unmatched
	// paths fall through to index.html so client-side routing works.
	if s.frontendDir != "" {
		fs := http.FileServer(http.Dir(s.frontendDir))
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/" {
				path = "/index.html"
			}
			fullPath := filepath.Join(s.frontendDir, path)
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				r.URL.Path = "/"
			}
			fs.ServeHTTP(w, r)
		})
	}

	return r
}

// collapseDoubleV1 rewrites a duplicated /v1/v1 path prefix down to a single
// /v1. The Anthropic SDK (Claude Code) always appends /v1/messages to
// ANTHROPIC_BASE_URL, so a base URL that already ends in /v1 yields
// /v1/v1/messages. base-URL styles (with or without a trailing /v1).
func collapseDoubleV1(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/v1":
			r.URL.Path = "/v1"
		case strings.HasPrefix(r.URL.Path, "/v1/v1/"):
			r.URL.Path = r.URL.Path[len("/v1"):]
		}
		next.ServeHTTP(w, r)
	})
}

// ---- HTTP helpers -----------------------------------------------------------

// writeJSON writes a JSON response with the given status. Uses Sonic-backed
// fastjson.Marshal instead of encoding/json.NewEncoder to avoid per-response
// encoder allocation and benefit from JIT-compiled serialization.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := fastjson.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"json marshal failed"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// writeError writes an OpenAI-style error envelope.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errorType(status),
		},
	})
}

func errorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 400 && status < 500:
		return "invalid_request_error"
	default:
		return "api_error"
	}
}

// tenantOf returns the tenant id for an authenticated key, defaulting to the
// implicit local tenant when unset.
func tenantOf(key store.APIKey) string {
	if key.TenantID != "" {
		return key.TenantID
	}
	return store.DefaultTenantID
}
