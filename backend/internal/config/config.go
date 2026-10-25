// Package config loads KeiRouter configuration from defaults, an optional YAML
// file, and environment variables (in increasing order of precedence).
//
// Env vars are prefixed KEIROUTER_ and use double underscores for nesting, e.g.
// KEIROUTER_SERVER__PORT=8080 sets server.port.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// Config is the fully-resolved application configuration.
type Config struct {
	Server         ServerConfig         `koanf:"server"`
	Database       DatabaseConfig       `koanf:"database"`
	Security       SecurityConfig       `koanf:"security"`
	Cache          CacheConfig          `koanf:"cache"`
	Meter          MeterConfig          `koanf:"meter"`
	Limits         LimitsConfig         `koanf:"limits"`
	Health         HealthConfig         `koanf:"health"`
	ProviderHealth ProviderHealthConfig `koanf:"provider_health"`
	Log            LogConfig            `koanf:"log"`
	Data           DataConfig           `koanf:"data"`
	Guardrails     GuardrailsConfig     `koanf:"guardrails"`
	PortalSSO      PortalSSOConfig      `koanf:"portal_sso"`
	Payment        PaymentConfig        `koanf:"payment"`
	Turnstile      TurnstileConfig      `koanf:"turnstile"`
	Audit          AuditConfig          `koanf:"audit"`
}

// DefaultMaxRequestBodyBytes is the default maximum size accepted for inbound
// API request bodies. Large multimodal and tool-heavy conversations can exceed
// the historical 32 MiB limit, while a finite cap still protects the process.
const DefaultMaxRequestBodyBytes int64 = 128 << 20 // 128 MiB

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Host string `koanf:"host"`
	Port int    `koanf:"port"`
	// StreamStallTimeout aborts a stream that produces no bytes for this long.
	StreamStallTimeout time.Duration `koanf:"stream_stall_timeout"`
	// StreamHeartbeatInterval emits an SSE comment on client streams that have
	// been silent for this long, so reverse proxies and load balancers between
	// KeiRouter and the client do not drop the connection as idle while the
	// upstream model is thinking. Zero disables heartbeats.
	StreamHeartbeatInterval time.Duration `koanf:"stream_heartbeat_interval"`
	// RequestTimeout bounds non-streaming upstream calls.
	RequestTimeout time.Duration `koanf:"request_timeout"`
	// CORSOrigins lists allowed dashboard origins ("*" permitted for local).
	CORSOrigins []string `koanf:"cors_origins"`
	// MaxRequestBodyBytes caps inbound API request bodies. Values less than one
	// are replaced by DefaultMaxRequestBodyBytes.
	MaxRequestBodyBytes int64 `koanf:"max_request_body_bytes"`
	// MaxConcurrentRequests caps in-flight API requests to prevent resource
	// exhaustion. Zero or negative means unlimited (not recommended for
	// high-traffic deployments). Default: 100.
	MaxConcurrentRequests int `koanf:"max_concurrent_requests"`
}

// DatabaseConfig selects and configures the persistence backend.
type DatabaseConfig struct {
	// Driver is "sqlite" (default, zero-config) or "postgres".
	Driver string `koanf:"driver"`
	// DSN is the connection string. Empty with sqlite => <data_dir>/keirouter.db.
	DSN string `koanf:"dsn"`
	// MaxOpenConns/MaxIdleConns tune the pool (postgres-relevant).
	MaxOpenConns int `koanf:"max_open_conns"`
	MaxIdleConns int `koanf:"max_idle_conns"`
	// ConnMaxLifetime recycles long-lived connections before managed Postgres
	// proxies or load balancers expire them. ConnMaxIdleTime bounds idle sockets.
	ConnMaxLifetime time.Duration `koanf:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `koanf:"conn_max_idle_time"`
}

// SecurityConfig holds auth and encryption settings.
type SecurityConfig struct {
	// MasterKey is the base64 root key for envelope encryption of credentials.
	// If empty, KeiRouter generates one on first run and persists it to
	// <data_dir>/master.key (0600). Provide via env/KMS in production.
	MasterKey string `koanf:"master_key"`
	// JWTSecret signs dashboard session tokens. Auto-generated if empty.
	JWTSecret string `koanf:"jwt_secret"`
	// DashboardPasswordHash is the argon2id hash of the dashboard password.
	DashboardPasswordHash string `koanf:"dashboard_password_hash"`
	// SessionTTL is how long a dashboard session stays valid.
	SessionTTL time.Duration `koanf:"session_ttl"`
	// BindLoopbackOnly rejects non-loopback dashboard/API access when true.
	BindLoopbackOnly bool `koanf:"bind_loopback_only"`
	// AllowPrivateBaseURL relaxes the SSRF guard on provider account base URLs
	// (and other ValidateOutboundURL callers) to permit loopback and RFC1918
	// addresses. Cloud metadata, link-local, unspecified, multicast, and
	// non-http(s) schemes remain blocked. Intended for self-hosted/LAN setups
	// pointing at on-network LLM endpoints. Default false.
	AllowPrivateBaseURL bool `koanf:"allow_private_base_url"`
	// TrustForwardedHeaders lets the dashboard session-cookie Secure attribute be
	// driven by X-Forwarded-Proto / RFC 7239 Forwarded headers, which is how a
	// TLS-terminating reverse proxy (nginx/Caddy/Traefik), Tailscale Funnel, or
	// Cloudflare Tunnel signals that the client connection is HTTPS.
	//
	// Trusting these headers is safe here: they can only ever *enable* the Secure
	// attribute (asserted proto=https). A spoofed header cannot weaken security,
	// because Secure=true is the more restrictive state. Default true so HTTPS
	// deployments behind a proxy keep a Secure cookie with zero extra config.
	//
	// Set false to ignore forwarded headers entirely and base the flag only on a
	// direct TLS connection. Default true.
	TrustForwardedHeaders bool `koanf:"trust_forwarded_headers"`
}

// PortalSSOConfig configures Google sign-in for the public usage portal.
// The client secret is intended to be supplied via environment variable
// (KEIROUTER_PORTAL_SSO__GOOGLE_CLIENT_SECRET), never committed to YAML.
type PortalSSOConfig struct {
	Enabled            bool          `koanf:"enabled"`
	GoogleClientID     string        `koanf:"google_client_id"`
	GoogleClientSecret string        `koanf:"google_client_secret"`
	AllowedDomains     []string      `koanf:"allowed_domains"`
	RedirectURL        string        `koanf:"redirect_url"`
	SessionTTL         time.Duration `koanf:"session_ttl"`
}

// PaymentConfig configures the SumoPod payment gateway used for portal
// self-service credit top-ups. The API key and webhook secrets are intended to
// come from environment variables (KEIROUTER_PAYMENT__API_KEY,
// KEIROUTER_PAYMENT__WEBHOOK_SECRET, KEIROUTER_PAYMENT__WEBHOOK_TOKEN), never
// committed to YAML.
type PaymentConfig struct {
	Enabled               bool             `koanf:"enabled"`
	Provider              string           `koanf:"provider"`
	BaseURL               string           `koanf:"base_url"`
	APIKey                string           `koanf:"api_key"`
	WebhookSecret         string           `koanf:"webhook_secret"`
	WebhookToken          string           `koanf:"webhook_token"`
	MinTopupIDR           int64            `koanf:"min_topup_idr"`
	MaxTopupIDR           int64            `koanf:"max_topup_idr"`
	Packages              []PaymentPackage `koanf:"packages"`
	PaymentMethodTypeCode string           `koanf:"payment_method_type_code"`
	ExpiresInHours        int              `koanf:"expires_in_hours"`
	SuccessReturnURL      string           `koanf:"success_return_url"`
	CancelReturnURL       string           `koanf:"cancel_return_url"`
	// MinHostingIDR is the lowest price a hosting coupon may bring an
	// order to, kept apart from MinTopupIDR so top-ups are not affected.
	MinHostingIDR int64 `koanf:"min_hosting_idr"`
}

// PaymentPackage is an admin-defined preset top-up amount in whole IDR.
type PaymentPackage struct {
	ID        string `koanf:"id"`
	Label     string `koanf:"label"`
	AmountIDR int64  `koanf:"amount_idr"`
}

// TurnstileConfig configures Cloudflare Turnstile bot protection for the
// dashboard login, portal sign-in start, and portal top-up order creation. The
// secret key is intended to come from the environment
// (KEIROUTER_TURNSTILE__SECRET_KEY), never committed to YAML. The site key is
// public and is exposed to the browser via /v1/portal/branding.
type TurnstileConfig struct {
	Enabled   bool   `koanf:"enabled"`
	SiteKey   string `koanf:"site_key"`
	SecretKey string `koanf:"secret_key"`
}

// CacheConfig configures the semantic response cache.
type CacheConfig struct {
	Enabled bool `koanf:"enabled"`
	// Backend is "memory" (in-process) or "redis".
	Backend string `koanf:"backend"`
	// RedisURL is used when Backend == "redis". Format: redis://host:port[/db]
	RedisURL string `koanf:"redis_url"`
	// SimilarityThreshold in [0,1]; a candidate hit must score >= this.
	SimilarityThreshold float64 `koanf:"similarity_threshold"`
	// TTL bounds cached entry lifetime.
	TTL time.Duration `koanf:"ttl"`

	// EmbeddingProvider selects the embedding backend: "hash" (exact-match,
	// zero-dependency default) or "api" (OpenAI-compatible embeddings API for
	// true semantic near-match caching).
	EmbeddingProvider string `koanf:"embedding_provider"`
	// EmbeddingAPIURL is the base URL for the embedding API (e.g.
	// "https://api.openai.com/v1"). Only used when EmbeddingProvider == "api".
	EmbeddingAPIURL string `koanf:"embedding_api_url"`
	// EmbeddingAPIKey is the bearer token for the embedding API.
	EmbeddingAPIKey string `koanf:"embedding_api_key"`
	// EmbeddingModel is the model name for the embedding API (default:
	// "text-embedding-3-small").
	EmbeddingModel string `koanf:"embedding_model"`
	// EmbeddingDims is the output vector dimensions (default: 1536).
	EmbeddingDims int `koanf:"embedding_dims"`
}

// MeterConfig controls usage metering writes.
type MeterConfig struct {
	// Async enables buffered/batched DB writes for usage rows.
	Async bool `koanf:"async"`
	// BatchSize is the maximum records per flush.
	BatchSize int `koanf:"batch_size"`
	// FlushInterval bounds how long records wait in memory.
	FlushInterval time.Duration `koanf:"flush_interval"`
	// QueueSize is the buffered event capacity.
	QueueSize int `koanf:"queue_size"`
	// FullQueuePolicy is "sync" (fallback direct write) or "drop".
	FullQueuePolicy string `koanf:"full_queue_policy"`
	// ShutdownFlushTimeout bounds final drain on shutdown.
	ShutdownFlushTimeout time.Duration `koanf:"shutdown_flush_timeout"`
}

// LimitsConfig controls per-key request rate limiting.
type LimitsConfig struct {
	// Enabled turns request limiting on. When false, limiter is bypassed.
	Enabled bool `koanf:"enabled"`
	// Backend is "memory" (one instance) or "redis". The redis backend shares
	// RPM/TPM windows, concurrency leases and budget spend counters across
	// every replica, which is what an HA deployment behind a load balancer
	// needs; memory limits are per process.
	Backend string `koanf:"backend"`
	// DefaultRPM/TPM/Concurrency apply to keys with no assigned plan.
	// Zero means unlimited.
	DefaultRPM         int64 `koanf:"default_rpm"`
	DefaultTPM         int64 `koanf:"default_tpm"`
	DefaultConcurrency int64 `koanf:"default_concurrency"`
	// Window is the fixed counting window for RPM/TPM.
	Window time.Duration `koanf:"window"`
	// CleanupInterval controls stale bucket cleanup for memory backend.
	CleanupInterval time.Duration `koanf:"cleanup_interval"`
	// ReconcileTokens settles each TPM reservation against the usage the
	// upstream reported, refunding over-estimates and charging overruns.
	ReconcileTokens bool `koanf:"reconcile_tokens"`
	// OutputTokenFloor is the output budget reserved for a chat request that
	// sets no max_tokens. Zero reserves nothing for output up front.
	OutputTokenFloor int64 `koanf:"output_token_floor"`
	// OutputReserveCap caps the output part of a TPM reservation, so clients
	// that always send a huge max_tokens do not exhaust TPM up front. Zero
	// means no cap.
	OutputReserveCap int64 `koanf:"output_reserve_cap"`
	// ConcurrencyRetryAfter is the Retry-After hint on a concurrency denial.
	ConcurrencyRetryAfter time.Duration `koanf:"concurrency_retry_after"`
	// AuthFailuresPerMinute refuses an IP's invalid API keys with 429 after
	// this many per minute; valid keys and loopback clients are never refused.
	// Applies even when per-key limits are off. Zero disables it.
	AuthFailuresPerMinute int `koanf:"auth_failures_per_minute"`
	// LoginAttemptsPerMinute caps dashboard login attempts per IP. Zero
	// disables it.
	LoginAttemptsPerMinute int `koanf:"login_attempts_per_minute"`
	// RedisURL is the redis backend's server (redis:// or rediss://). Empty
	// falls back to cache.redis_url.
	RedisURL string `koanf:"redis_url"`
	// RedisTimeout bounds each limiter round trip. A slower or failed call
	// trips a short circuit breaker and the request is decided by FailMode.
	RedisTimeout time.Duration `koanf:"redis_timeout"`
	// FailMode decides requests while redis is unreachable: "local" enforces
	// the same limits per process, "open" admits everything, "closed" rejects
	// with a retryable rate-limit error.
	FailMode string `koanf:"fail_mode"`
	// LeaseTTL is how long a concurrency slot survives without a heartbeat,
	// so slots held by a crashed replica free themselves.
	LeaseTTL time.Duration `koanf:"lease_ttl"`
	// BudgetCounterTTL is how long a shared budget spend counter lives before
	// it is re-seeded from the usage table, which bounds drift from dropped
	// increments.
	BudgetCounterTTL time.Duration `koanf:"budget_counter_ttl"`
}

// AuditConfig controls the administrative audit trail: dashboard logins and
// every configuration change, stored in audit_log and optionally exported as
// OpenTelemetry log records.
type AuditConfig struct {
	// Enabled records audit events. Default true.
	Enabled bool `koanf:"enabled"`
	// RetentionDays drops audit rows older than N days. Zero keeps them.
	RetentionDays int `koanf:"retention_days"`
	// BufferSize bounds queued events. A full queue drops (and counts) events
	// instead of blocking the admin request.
	BufferSize int `koanf:"buffer_size"`
	// OTLP exports each event as an OTLP log record.
	OTLP AuditOTLPConfig `koanf:"otlp"`
}

// AuditOTLPConfig configures OTLP/HTTP (JSON) export of audit events. Unset
// fields fall back to the standard OTEL_EXPORTER_OTLP_* environment variables.
type AuditOTLPConfig struct {
	// Endpoint is the collector base URL, e.g. http://otel-collector:4318.
	// "/v1/logs" is appended unless the path already names it. Empty falls
	// back to OTEL_EXPORTER_OTLP_LOGS_ENDPOINT, then OTEL_EXPORTER_OTLP_ENDPOINT;
	// with neither set, export is off.
	Endpoint string `koanf:"endpoint"`
	// Headers are sent on every export, e.g. an Authorization or API key
	// header for a hosted backend. Merged over OTEL_EXPORTER_OTLP_HEADERS.
	Headers map[string]string `koanf:"headers"`
	// Compression is "gzip" (default) or "none".
	Compression string `koanf:"compression"`
	// Timeout bounds one export request.
	Timeout time.Duration `koanf:"timeout"`
	// BatchSize is the maximum records per export request.
	BatchSize int `koanf:"batch_size"`
	// FlushInterval bounds how long a record waits before export.
	FlushInterval time.Duration `koanf:"flush_interval"`
	// MaxRetries retries a failed export (network error, 429, 5xx) with
	// exponential backoff before the batch is dropped.
	MaxRetries int `koanf:"max_retries"`
	// ServiceName is the service.name resource attribute. Empty falls back to
	// OTEL_SERVICE_NAME, then "keirouter".
	ServiceName string `koanf:"service_name"`
	// ResourceAttributes are extra resource attributes (deployment.environment,
	// service.instance.id, ...). Merged over OTEL_RESOURCE_ATTRIBUTES.
	ResourceAttributes map[string]string `koanf:"resource_attributes"`
	// IncludeRequests also exports one record per gateway request (event
	// keirouter.request): key, model, provider, tokens, cost, latency, status
	// and guardrail outcome, never prompt or response content. Off by default
	// because it scales with traffic.
	IncludeRequests bool `koanf:"include_requests"`
}

// HealthConfig controls background account/model health probes.
type HealthConfig struct {
	Enabled              bool          `koanf:"enabled"`
	Interval             time.Duration `koanf:"interval"`
	Timeout              time.Duration `koanf:"timeout"`
	MaxParallel          int           `koanf:"max_parallel"`
	FailureThreshold     int           `koanf:"failure_threshold"`
	SuccessThreshold     int           `koanf:"success_threshold"`
	RecentModelWindow    time.Duration `koanf:"recent_model_window"`
	MaxModelsPerProvider int           `koanf:"max_models_per_provider"`
}

// ProviderHealthConfig controls the actionable provider health dashboard:
// real-traffic telemetry aggregation, synthetic probes, and latency thresholds.
type ProviderHealthConfig struct {
	// Enabled gates telemetry recording and the probe worker.
	Enabled bool `koanf:"enabled"`
	// ProbeInterval is the scheduled probe cadence.
	ProbeInterval time.Duration `koanf:"probe_interval"`
	// ProbeTimeout bounds one probe call.
	ProbeTimeout time.Duration `koanf:"probe_timeout"`
	// FailureThreshold is how many consecutive probe failures mark a target
	// unhealthy when no real traffic exists.
	FailureThreshold int `koanf:"failure_threshold"`
	// QueueSize is the async telemetry channel capacity.
	QueueSize int `koanf:"queue_size"`
	// CurrentFlushInterval recomputes provider_health_current this often.
	CurrentFlushInterval time.Duration `koanf:"current_flush_interval"`
	// SnapshotInterval writes 1-minute snapshot rows this often.
	SnapshotInterval time.Duration `koanf:"snapshot_interval"`
	// RollingWindow is the lookback for current-state aggregation.
	RollingWindow time.Duration `koanf:"rolling_window"`
	// LatencyThresholds maps capability to its p95 threshold in ms.
	LatencyThresholds map[string]int `koanf:"latency_thresholds"`
	// Capabilities controls which capability types are auto-probed.
	Capabilities ProviderHealthCapabilityConfig `koanf:"capabilities"`
}

// ProviderHealthCapabilityConfig toggles per-capability scheduled probes. Image,
// audio, and search are off by default to avoid expensive probes.
type ProviderHealthCapabilityConfig struct {
	ChatCompletions ProviderHealthProbeConfig `koanf:"chat_completions"`
	Embeddings      ProviderHealthProbeConfig `koanf:"embeddings"`
	ImageGeneration ProviderHealthProbeConfig `koanf:"image_generation"`
	Audio           ProviderHealthProbeConfig `koanf:"audio"`
	Search          ProviderHealthProbeConfig `koanf:"search"`
}

// ProviderHealthProbeConfig configures one capability's probe payload.
type ProviderHealthProbeConfig struct {
	Enabled   bool   `koanf:"enabled"`
	MaxTokens int    `koanf:"max_tokens"`
	Prompt    string `koanf:"prompt"`
	Input     string `koanf:"input"`
}

// LogConfig controls structured logging.
type LogConfig struct {
	// Level: debug, info, warn, error.
	Level string `koanf:"level"`
	// Format: json or text.
	Format string `koanf:"format"`
}

// DataConfig controls on-disk state location.
type DataConfig struct {
	// Dir is the root data directory. Empty => OS-specific default.
	Dir string `koanf:"dir"`
}

// GuardrailsConfig wires optional external engines for the guardrails layer.
// Detectors run with their built-in native engines by default; populate the
// sub-configs to enable an external engine on a per-detector basis.
type GuardrailsConfig struct {
	Toxicity  ToxicityEngineConfig  `koanf:"toxicity"`
	PII       PIIEngineConfig       `koanf:"pii"`
	Injection InjectionEngineConfig `koanf:"injection"`
	// AuditRetentionDays drops guardrail_logs older than N days via a
	// background sweeper. Zero / negative disables retention sweeping (the
	// table grows unbounded). 90 is a sensible operational default.
	AuditRetentionDays int `koanf:"audit_retention_days"`
}

// PIIEngineConfig configures the optional Microsoft Presidio HTTP analyzer
// engine. When AnalyzerURL is empty the engine stays disabled and policies
// that select engine="presidio" fall back to native.
type PIIEngineConfig struct {
	PresidioAnalyzerURL string        `koanf:"presidio_analyzer_url"`
	PresidioTimeout     time.Duration `koanf:"presidio_timeout"`
	PresidioLanguage    string        `koanf:"presidio_language"`
}

// InjectionEngineConfig configures the optional ML prompt-injection
// classifier (Llama Prompt Guard 2 or a DeBERTa model) served over HTTP with
// the Hugging Face text-classification contract — see compose.promptguard.yaml.
// When ClassifierURL is empty, policies that enable the classifier layer
// keep running the pattern layers only.
type InjectionEngineConfig struct {
	ClassifierURL     string        `koanf:"classifier_url"`
	ClassifierTimeout time.Duration `koanf:"classifier_timeout"`
	// ClassifierLabels lists the labels that mean "injection"; the default
	// covers Prompt Guard 2 (LABEL_1) and the common DeBERTa models.
	ClassifierLabels []string `koanf:"classifier_labels"`
	ClassifierAPIKey string   `koanf:"classifier_api_key"`
}

// ToxicityEngineConfig configures the optional OpenAI Moderation engine. When
// OpenAIAPIKey is empty the OpenAI engine stays disabled and policies that
// select engine="openai" fall back to native.
type ToxicityEngineConfig struct {
	OpenAIAPIKey  string        `koanf:"openai_api_key"`
	OpenAIBaseURL string        `koanf:"openai_base_url"`
	OpenAIModel   string        `koanf:"openai_model"`
	OpenAITimeout time.Duration `koanf:"openai_timeout"`
}

// Default returns the baseline configuration applied before file/env overrides.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 20180,
			// Reasoning models can legitimately pause for several minutes after
			// emitting an initial text/thinking chunk. Keep the stall budget in
			// step with the default request timeout to avoid aborting healthy work.
			StreamStallTimeout: 5 * time.Minute,
			// 15s keeps connections alive through common proxy idle timeouts
			// (nginx defaults to 60s) without meaningful bandwidth cost.
			StreamHeartbeatInterval: 15 * time.Second,
			RequestTimeout:          5 * time.Minute,
			CORSOrigins:             []string{"*"},
			MaxRequestBodyBytes:     DefaultMaxRequestBodyBytes,
		},
		Database: DatabaseConfig{
			Driver:          "sqlite",
			MaxOpenConns:    0,
			MaxIdleConns:    0,
			ConnMaxLifetime: 30 * time.Minute,
			ConnMaxIdleTime: 5 * time.Minute,
		},
		Security: SecurityConfig{
			SessionTTL:            24 * time.Hour,
			BindLoopbackOnly:      true,
			TrustForwardedHeaders: true, // default true: safe to trust proxies for Secure flag; only ever strengthens security
		},
		Cache: CacheConfig{
			Enabled:             false,
			Backend:             "memory",
			SimilarityThreshold: 0.95,
			TTL:                 time.Hour,
			EmbeddingProvider:   "hash",
			EmbeddingModel:      "text-embedding-3-small",
			EmbeddingDims:       1536,
		},
		Meter: MeterConfig{
			Async:                true,
			BatchSize:            100,
			FlushInterval:        time.Second,
			QueueSize:            10000,
			FullQueuePolicy:      "sync",
			ShutdownFlushTimeout: 5 * time.Second,
		},
		Limits: LimitsConfig{
			Enabled:                false,
			Backend:                "memory",
			Window:                 time.Minute,
			CleanupInterval:        time.Minute,
			ReconcileTokens:        true,
			ConcurrencyRetryAfter:  time.Second,
			AuthFailuresPerMinute:  20,
			LoginAttemptsPerMinute: 5,
			RedisTimeout:           100 * time.Millisecond,
			FailMode:               "local",
			LeaseTTL:               time.Minute,
			BudgetCounterTTL:       time.Minute,
		},
		Audit: AuditConfig{
			Enabled:       true,
			RetentionDays: 365,
			BufferSize:    4096,
			OTLP: AuditOTLPConfig{
				Compression:   "gzip",
				Timeout:       10 * time.Second,
				BatchSize:     256,
				FlushInterval: 2 * time.Second,
				MaxRetries:    3,
			},
		},
		Health: HealthConfig{
			Enabled: true,
			// Probes are real completions against every recently used model
			// of every account; a two-minute cadence keeps the signal without
			// spending a meaningful share of per-minute quotas on pings.
			Interval: 2 * time.Minute,
			// Reasoning models routinely need more than five seconds to answer
			// even a one-word prompt; a short deadline flagged them unhealthy.
			Timeout:              15 * time.Second,
			MaxParallel:          8,
			FailureThreshold:     2,
			SuccessThreshold:     1,
			RecentModelWindow:    24 * time.Hour,
			MaxModelsPerProvider: 8,
		},
		ProviderHealth: ProviderHealthConfig{
			Enabled:              true,
			ProbeInterval:        60 * time.Second,
			ProbeTimeout:         15 * time.Second,
			FailureThreshold:     3,
			QueueSize:            5000,
			CurrentFlushInterval: 30 * time.Second,
			SnapshotInterval:     60 * time.Second,
			RollingWindow:        15 * time.Minute,
			LatencyThresholds: map[string]int{
				"chat_completions": 10_000,
				"embeddings":       5_000,
				"image_generation": 60_000,
				"audio":            60_000,
				"search":           15_000,
			},
			Capabilities: ProviderHealthCapabilityConfig{
				ChatCompletions: ProviderHealthProbeConfig{Enabled: true, MaxTokens: 5, Prompt: "Reply with OK only."},
				Embeddings:      ProviderHealthProbeConfig{Enabled: true, Input: "health check"},
			},
		},
		Log: LogConfig{Level: "info", Format: "text"},
		PortalSSO: PortalSSOConfig{
			Enabled:    false,
			SessionTTL: 24 * time.Hour,
		},
		Turnstile: TurnstileConfig{
			Enabled: false,
		},
		Payment: PaymentConfig{
			Enabled:               false,
			Provider:              "sumopod",
			BaseURL:               "https://api-pay-sandbox.sumopod.com",
			MinTopupIDR:           10000,
			MaxTopupIDR:           10000000,
			MinHostingIDR:         5000,
			PaymentMethodTypeCode: "QRIS",
			ExpiresInHours:        24,
			Packages: []PaymentPackage{
				{ID: "idr10k", Label: "Rp 10.000", AmountIDR: 10000},
				{ID: "idr25k", Label: "Rp 25.000", AmountIDR: 25000},
				{ID: "idr50k", Label: "Rp 50.000", AmountIDR: 50000},
				{ID: "idr100k", Label: "Rp 100.000", AmountIDR: 100000},
			},
		},
	}
}

// Load resolves configuration. filePath may be empty to skip file loading.
func Load(filePath string) (Config, error) {
	k := koanf.New(".")

	// 1. Defaults.
	if err := k.Load(structs.Provider(Default(), "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	// 2. Optional YAML file.
	if filePath != "" {
		if err := k.Load(file.Provider(filePath), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load config file %q: %w", filePath, err)
		}
	}

	// 3. Environment overrides: KEIROUTER_SERVER__PORT -> server.port.
	envProvider := env.Provider("KEIROUTER_", ".", func(s string) string {
		s = strings.TrimPrefix(s, "KEIROUTER_")
		s = strings.ReplaceAll(s, "__", ".")
		s = strings.ToLower(s)
		if s == "security.allow_private_baseurl" {
			return "security.allow_private_base_url"
		}
		return s
	})
	if err := k.Load(envProvider, nil); err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Database.Driver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("database.driver must be sqlite or postgres, got %q", c.Database.Driver)
	}
	if c.Database.Driver == "postgres" && c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required when driver=postgres")
	}
	if c.Database.ConnMaxLifetime <= 0 {
		c.Database.ConnMaxLifetime = 30 * time.Minute
	}
	if c.Database.ConnMaxIdleTime <= 0 {
		c.Database.ConnMaxIdleTime = 5 * time.Minute
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port out of range: %d", c.Server.Port)
	}
	if c.Server.MaxRequestBodyBytes <= 0 {
		c.Server.MaxRequestBodyBytes = DefaultMaxRequestBodyBytes
	}
	if c.Meter.BatchSize <= 0 {
		c.Meter.BatchSize = 100
	}
	if c.Meter.FlushInterval <= 0 {
		c.Meter.FlushInterval = time.Second
	}
	if c.Meter.QueueSize <= 0 {
		c.Meter.QueueSize = 10000
	}
	switch c.Meter.FullQueuePolicy {
	case "", "sync", "drop":
	default:
		return fmt.Errorf("meter.full_queue_policy must be sync or drop, got %q", c.Meter.FullQueuePolicy)
	}
	if c.Meter.ShutdownFlushTimeout <= 0 {
		c.Meter.ShutdownFlushTimeout = 5 * time.Second
	}
	switch c.Limits.Backend {
	case "", "memory":
		c.Limits.Backend = "memory"
	case "redis":
		if c.Limits.RedisURL == "" {
			c.Limits.RedisURL = c.Cache.RedisURL
		}
		if c.Limits.RedisURL == "" {
			return fmt.Errorf("limits.redis_url (or cache.redis_url) is required when limits.backend=redis")
		}
	default:
		return fmt.Errorf("limits.backend must be memory or redis, got %q", c.Limits.Backend)
	}
	switch c.Limits.FailMode {
	case "":
		c.Limits.FailMode = "local"
	case "local", "open", "closed":
	default:
		return fmt.Errorf("limits.fail_mode must be local, open or closed, got %q", c.Limits.FailMode)
	}
	if c.Limits.RedisTimeout <= 0 {
		c.Limits.RedisTimeout = 100 * time.Millisecond
	}
	if c.Limits.LeaseTTL <= 0 {
		c.Limits.LeaseTTL = time.Minute
	}
	if c.Limits.BudgetCounterTTL <= 0 {
		c.Limits.BudgetCounterTTL = time.Minute
	}
	if c.Audit.RetentionDays < 0 {
		return fmt.Errorf("audit.retention_days must not be negative")
	}
	if c.Audit.BufferSize <= 0 {
		c.Audit.BufferSize = 4096
	}
	switch c.Audit.OTLP.Compression {
	case "":
		c.Audit.OTLP.Compression = "gzip"
	case "gzip", "none":
	default:
		return fmt.Errorf("audit.otlp.compression must be gzip or none, got %q", c.Audit.OTLP.Compression)
	}
	if c.Audit.OTLP.Timeout <= 0 {
		c.Audit.OTLP.Timeout = 10 * time.Second
	}
	if c.Audit.OTLP.BatchSize <= 0 {
		c.Audit.OTLP.BatchSize = 256
	}
	if c.Audit.OTLP.FlushInterval <= 0 {
		c.Audit.OTLP.FlushInterval = 2 * time.Second
	}
	if c.Audit.OTLP.MaxRetries < 0 {
		c.Audit.OTLP.MaxRetries = 0
	}
	if c.Limits.DefaultRPM < 0 {
		return fmt.Errorf("limits.default_rpm must not be negative")
	}
	if c.Limits.DefaultTPM < 0 {
		return fmt.Errorf("limits.default_tpm must not be negative")
	}
	if c.Limits.DefaultConcurrency < 0 {
		return fmt.Errorf("limits.default_concurrency must not be negative")
	}
	if c.Limits.Window <= 0 {
		c.Limits.Window = time.Minute
	}
	if c.Limits.CleanupInterval <= 0 {
		c.Limits.CleanupInterval = time.Minute
	}
	if c.Limits.OutputTokenFloor < 0 {
		return fmt.Errorf("limits.output_token_floor must not be negative")
	}
	if c.Limits.OutputReserveCap < 0 {
		return fmt.Errorf("limits.output_reserve_cap must not be negative")
	}
	if c.Limits.ConcurrencyRetryAfter <= 0 {
		c.Limits.ConcurrencyRetryAfter = time.Second
	}
	if c.Limits.AuthFailuresPerMinute < 0 {
		return fmt.Errorf("limits.auth_failures_per_minute must not be negative")
	}
	if c.Limits.LoginAttemptsPerMinute < 0 {
		return fmt.Errorf("limits.login_attempts_per_minute must not be negative")
	}
	if c.Health.Interval <= 0 {
		c.Health.Interval = 30 * time.Second
	}
	if c.Health.Timeout <= 0 {
		c.Health.Timeout = 5 * time.Second
	}
	if c.Health.MaxParallel <= 0 {
		c.Health.MaxParallel = 8
	}
	if c.Health.FailureThreshold <= 0 {
		c.Health.FailureThreshold = 2
	}
	if c.Health.SuccessThreshold <= 0 {
		c.Health.SuccessThreshold = 1
	}
	if c.Health.RecentModelWindow <= 0 {
		c.Health.RecentModelWindow = 24 * time.Hour
	}
	if c.Health.MaxModelsPerProvider <= 0 {
		c.Health.MaxModelsPerProvider = 8
	}
	if c.PortalSSO.Enabled {
		if c.PortalSSO.GoogleClientID == "" || c.PortalSSO.GoogleClientSecret == "" {
			return errors.New("portal_sso.enabled requires google_client_id and google_client_secret")
		}
		if c.PortalSSO.SessionTTL <= 0 {
			c.PortalSSO.SessionTTL = 24 * time.Hour
		}
	}
	if c.Turnstile.Enabled {
		if strings.TrimSpace(c.Turnstile.SiteKey) == "" || strings.TrimSpace(c.Turnstile.SecretKey) == "" {
			return errors.New("turnstile.enabled requires site_key and secret_key")
		}
	}
	if c.Payment.Provider == "" {
		c.Payment.Provider = "sumopod"
	}
	if c.Payment.PaymentMethodTypeCode == "" {
		c.Payment.PaymentMethodTypeCode = "QRIS"
	}
	if c.Payment.ExpiresInHours <= 0 {
		c.Payment.ExpiresInHours = 24
	}
	if c.Payment.MaxTopupIDR < c.Payment.MinTopupIDR {
		return fmt.Errorf("payment.max_topup_idr must be >= min_topup_idr")
	}
	if c.Payment.Enabled {
		u, err := url.Parse(strings.TrimSpace(c.Payment.BaseURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("payment.base_url must be a valid http(s) URL when enabled")
		}
		if strings.TrimSpace(c.Payment.APIKey) == "" {
			return errors.New("payment.enabled requires api_key")
		}
		if c.Payment.MinTopupIDR <= 0 {
			return errors.New("payment.min_topup_idr must be positive when enabled")
		}
	}
	for _, p := range c.Payment.Packages {
		if p.ID == "" || p.AmountIDR < c.Payment.MinTopupIDR || p.AmountIDR > c.Payment.MaxTopupIDR {
			return fmt.Errorf("payment package %q amount_idr not within [min,max]", p.ID)
		}
	}
	return nil
}

// Addr returns the host:port the server binds to.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}
