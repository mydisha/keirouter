package store

import "time"

// DefaultTenantID is the implicit tenant used in local single-user mode.
const DefaultTenantID = "default"

// Tenant scopes all data in multi-tenant deployments.
type Tenant struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// PortalUser binds one Google identity to exactly one API key for the public
// usage portal. GoogleSub is Google's stable subject id.
type PortalUser struct {
	GoogleSub string
	Email     string
	KeyID     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Project partitions usage and budgets within a tenant.
type Project struct {
	ID        string
	TenantID  string
	Name      string
	CreatedAt time.Time
}

// APIKey is a stored inbound credential. The plaintext is never persisted.
type APIKey struct {
	ID         string
	TenantID   string
	ProjectID  string
	PlanID     string // bound plan (empty = custom / no plan)
	Name       string
	KeyHash    string // argon2id verifier
	LookupHash string // sha-256 index
	Display    string // masked form
	Scopes     string
	Disabled   bool
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

// Plan is a reusable template for budget limits and model restrictions.
// API keys inherit plan rules unless they have per-key overrides.
type Plan struct {
	ID               string
	TenantID         string
	Name             string
	Description      string
	LimitMicros      int64
	LimitTokens      int64
	RPMLimit         int64
	TPMLimit         int64
	ConcurrencyLimit int64
	Period           string
	AlertPct         int
	HardCutoff       bool
	AllowedModels    string // comma-separated patterns, empty = all
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AuthKind classifies how an account authenticates upstream.
type AuthKind string

const (
	AuthAPIKey AuthKind = "api_key"
	AuthOAuth  AuthKind = "oauth"
	AuthNone   AuthKind = "none"
)

// Account holds an upstream provider credential. Secret material is stored as
// envelope-encrypted blobs (the *WrappedDEK / *Ciphertext pairs).
type Account struct {
	ID       string
	TenantID string
	Provider string
	Label    string
	AuthKind AuthKind

	SecretWrappedDEK string
	SecretCiphertext string

	TokenWrappedDEK string
	TokenCiphertext string

	RefreshWrappedDEK string
	RefreshCiphertext string

	TokenExpiresAt *time.Time

	Metadata       string // JSON: base_url, region, project_id, ...
	Priority       int
	BackoffLevel   int // exponential backoff level for adaptive cooldowns
	Disabled       bool
	CooldownUntil  *time.Time
	ProxyPoolID    string // bound proxy pool id (empty = no proxy)
	NeedsReconnect bool   // true when the OAuth refresh token was permanently rejected (re-auth required)
	// CreditsExhausted is true when the provider reported a depleted paid
	// balance. The dispatcher parks the account; the flag clears on a
	// successful request, a provider reconnect, or a manual reset.
	CreditsExhausted bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Chain is an ordered fallback definition (routing chain).
type Chain struct {
	ID               string
	TenantID         string
	Name             string
	Strategy         string
	FallbackProvider string
	FallbackModel    string
	// Operator price for the chain model. All zero = fall back to catalog.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64
	Steps          []ChainStep
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ChainStep is one candidate target within a chain.
type ChainStep struct {
	ID        string
	ChainID   string
	Position  int
	Provider  string
	Model     string
	CreatedAt time.Time
}

// UsageRecord is the terminal accounting fact for one inbound request.
// Token counts, pricing provenance, and cost components are snapshotted so
// historical reports never depend on whichever catalog happens to be current.
type UsageRecord struct {
	ID        string
	RequestID string
	TenantID  string
	ProjectID string
	APIKeyID  string
	ChainID   string

	Provider  string
	Model     string
	AccountID string
	Client    string // detected calling tool (claude-code, codex, ...) or "unknown"
	Status    string // success | cache_hit | blocked | failed | cancelled
	ErrorKind string

	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	CacheWriteTokens int
	ReasoningTokens  int
	UsageSource      string // provider | estimated | cache | none

	// CostMicros remains for budget/backward compatibility. CostNanos is the
	// authoritative value and avoids each small request being rounded to zero.
	CostMicros          int64
	CostNanos           int64
	InputCostNanos      int64
	CachedCostNanos     int64
	CacheWriteCostNanos int64
	OutputCostNanos     int64
	ReasoningCostNanos  int64
	AvoidedCostNanos    int64 // semantic-cache retail-equivalent cost avoided
	SavedCostNanos      int64 // input compression retail-equivalent saving

	PricingStatus      string // priced | estimated | free | missing | legacy | none
	PricingSource      string // official | custom | retail_equivalent | legacy
	PricingKey         string
	PricingMatchKind   string // exact | provider_alias | canonical_model | provider | legacy | none
	PricingSourceURL   string
	PricingAsOf        *time.Time
	PricingBackfilled  bool
	InputRatePerM      float64
	CachedRatePerM     float64
	CacheWriteRatePerM float64
	OutputRatePerM     float64
	ReasoningRatePerM  float64

	CacheHit          bool
	LatencyMS         int // retained compatibility alias for end-to-end latency
	UpstreamLatencyMS int
	EndToEndLatencyMS int
	TTFTMS            int    // time-to-first-token in ms (0 for non-streaming or cache hits)
	SlimBytesSaved    int    // bytes removed by RTK slimmer (input-side compression)
	SlimTokensSaved   int    // estimated tokens saved by RTK (bytes/4)
	SlimRules         string // comma-separated rule names that fired (e.g. "git-diff,grep")
	SlimActive        bool   // RTK slimmer was enabled for this request
	CavemanActive     bool   // caveman output compression was active
	TerseActive       bool   // terse output compression was active

	HeadroomTokensSaved int  // tokens saved by Headroom (input-side compression)
	HeadroomBytesSaved  int  // bytes removed by Headroom (input-side compression)
	HeadroomActive      bool // Headroom achieved real (non-phantom) savings
	PonytailActive      bool // ponytail output injection was active

	CreatedAt time.Time
}

// BudgetScope identifies what a budget applies to.
type BudgetScope string

const (
	ScopeTenant  BudgetScope = "tenant"
	ScopeProject BudgetScope = "project"
	ScopeAPIKey  BudgetScope = "api_key"
)

// Budget enforces a spend and/or token limit over a period.
type Budget struct {
	ID          string
	TenantID    string
	ScopeKind   BudgetScope
	ScopeID     string
	LimitMicros int64
	LimitTokens int64 // 0 = no token limit
	Period      string
	AlertPct    int
	HardCutoff  bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// KeyTopup is one append-only budget top-up for an API key. Amount and limits
// are integer micro-USD (1 USD = 1,000,000 micros).
type KeyTopup struct {
	ID                string
	TenantID          string
	KeyID             string
	BudgetID          string
	AmountMicros      int64
	Reason            string
	LimitBeforeMicros int64
	LimitAfterMicros  int64
	IdempotencyKey    string
	Actor             string
	CreatedAt         time.Time
}

// KeyLimitAdjustment is one append-only manual correction to an API key's
// budget limit. Unlike a top-up, DeltaMicros is signed: a negative value
// reduces the limit (e.g. undoing a mistaken top-up), a positive value raises
// it. All amounts are integer micro-USD (1 USD = 1,000,000 micros).
type KeyLimitAdjustment struct {
	ID                string
	TenantID          string
	KeyID             string
	BudgetID          string
	DeltaMicros       int64
	Reason            string
	LimitBeforeMicros int64
	LimitAfterMicros  int64
	IdempotencyKey    string
	Actor             string
	CreatedAt         time.Time
}

// AuditEntry is one append-only audit record.
type AuditEntry struct {
	ID        string
	TenantID  string
	Actor     string
	Action    string
	Target    string
	Detail    string
	CreatedAt time.Time
}

// APIKeyModelAccess is a single allowed-model row for per-key model gating.
// When rows exist for a key, only listed models are permitted.
type APIKeyModelAccess struct {
	APIKeyID  string
	Model     string
	CreatedAt time.Time
}

// ModelCooldown locks a specific model on an account. While active, the
// dispatch layer skips this account for that model but still allows other
// models on the same account.
type ModelCooldown struct {
	ID            string
	AccountID     string
	Model         string
	CooldownUntil time.Time
	CreatedAt     time.Time
}

// ChainRotation persists the round-robin cursor for a routing chain so
// distribution is fair across restarts.
type ChainRotation struct {
	ChainID   string
	LastIndex int
	HitCount  int
	UpdatedAt time.Time
}

// TargetRotation persists the round-robin cursor for a provider/model target.
// It is scoped by an opaque key so callers can include tenant/provider/model
// without coupling the store schema to routing string parsing.
type TargetRotation struct {
	ScopeKey  string
	LastIndex int
	HitCount  int
	UpdatedAt time.Time
}

// AccountAffinity pins a stable conversation/request key to an account. The
// dispatcher uses it for smart round-robin so a conversation keeps context on
// the same upstream account while new conversations still spread by rotation.
type AccountAffinity struct {
	ScopeKey  string
	AccountID string
	ExpiresAt time.Time
	UpdatedAt time.Time
}

// AccountHealth stores background probe status for one account/model pair.
// Model "__all__" means provider/account-level health when no specific model is
// known. The dispatcher treats unhealthy rows as a soft skip.
type AccountHealth struct {
	ID                   string
	TenantID             string
	AccountID            string
	Provider             string
	Model                string
	Status               string // healthy | degraded | unhealthy
	LatencyMS            int
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastOKAt             *time.Time
	LastCheckedAt        time.Time
	LastError            string
	UpdatedAt            time.Time
}

// ResourceSample is one resource_samples row. Nullable fields use pointers so
// platform-unsupported signals (open FDs, load average) round-trip as NULL.
type ResourceSample struct {
	TenantID  string
	CreatedAt time.Time

	Goroutines     int64
	HeapAllocBytes int64
	HeapSysBytes   int64
	GCPauseNS      int64
	NextGCBytes    int64
	NumGC          int64

	ProcCPUPercent float64
	ProcRSSBytes   int64
	ProcThreads    int64
	ProcOpenFDs    *int64

	HostCPUPercent     float64
	HostMemUsedBytes   int64
	HostMemTotalBytes  int64
	HostDiskUsedBytes  int64
	HostDiskTotalBytes int64
	HostNetSentBytes   int64
	HostNetRecvBytes   int64
	HostLoad1          *float64
	HostLoad5          *float64
	HostLoad15         *float64

	InflightRequests int64
}

// GuardrailScope identifies which dimension of a request a policy targets.
// Policies stack at request time from least to most specific.
type GuardrailScope string

const (
	GuardrailScopeGlobal   GuardrailScope = "global"
	GuardrailScopeProvider GuardrailScope = "provider"
	GuardrailScopeModel    GuardrailScope = "model"
	GuardrailScopeChain    GuardrailScope = "chain"
	GuardrailScopeAPIKey   GuardrailScope = "apikey"
)

// GuardrailPolicy is a stored safety policy. Config is an opaque JSON blob
// owned by the guardrails package; the store layer never inspects it.
type GuardrailPolicy struct {
	ID        string
	TenantID  string
	Scope     GuardrailScope
	ScopeID   string // empty for global
	Name      string
	Enabled   bool
	Config    string // JSON
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GuardrailLog is one detector decision recorded for audit.
type GuardrailLog struct {
	ID        string
	TenantID  string
	RequestID string
	APIKeyID  string
	Provider  string
	Model     string
	ChainID   string
	Detector  string // pii | injection | toxicity | topics | bias
	Direction string // inbound | outbound
	Action    string // allow | warn | mask | block | log_only
	Severity  string // low | medium | high (empty when n/a)
	Reason    string
	Findings  string // JSON array
	CreatedAt time.Time
}

// ResourceBucket is one aggregated time slice for the resources timeline.
// Each metric carries an average and a max so spikes survive bucketing.
// Network bytes are reported as a within-bucket delta (MAX-MIN of cumulative).
type ResourceBucket struct {
	Bucket int

	ProcCPUAvg float64
	ProcCPUMax float64
	HostCPUAvg float64
	HostCPUMax float64

	ProcRSSAvg     float64
	ProcRSSMax     int64
	HostMemUsedAvg float64
	HostMemUsedMax int64

	GoroutinesAvg float64
	GoroutinesMax int64
	HeapAllocAvg  float64
	HeapAllocMax  int64

	NetSentDelta int64
	NetRecvDelta int64

	GCPauseAvg float64
	GCPauseMax int64

	InflightAvg float64
	InflightMax int64
}

// ProviderHealthCurrent is the rolled-up current health state for one
// provider/account/model/capability key. It is updated by both real-traffic
// telemetry aggregation and synthetic probes for fast dashboard loading.
type ProviderHealthCurrent struct {
	ID                  string     `json:"id"`
	Provider            string     `json:"provider"`
	ProviderAccountID   string     `json:"provider_account_id"`
	Model               string     `json:"model"`
	Capability          string     `json:"capability"`
	HealthStatus        string     `json:"health_status"` // healthy | degraded | unhealthy | unknown | disabled
	HealthScore         int        `json:"health_score"`
	SuccessRate         float64    `json:"success_rate"` // 0-1
	ErrorRate           float64    `json:"error_rate"`   // 0-1
	RequestCount        int64      `json:"request_count"`
	FallbackCount       int64      `json:"fallback_count"`
	LatencyP95Ms        *int       `json:"latency_p95_ms"`
	TTFTP95Ms           *int       `json:"ttft_p95_ms"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	MainIssue           *string    `json:"main_issue"`
	Recommendation      *string    `json:"recommendation"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastFailureAt       *time.Time `json:"last_failure_at"`
	LastProbeAt         *time.Time `json:"last_probe_at"`
	LastUpdatedAt       time.Time  `json:"last_updated_at"`
}

// ProviderHealthSnapshot is one aggregated time bucket of provider health,
// stored for historical charts and trend analysis.
type ProviderHealthSnapshot struct {
	ID                  string    `json:"id"`
	BucketStart         time.Time `json:"bucket_start"`
	BucketSizeSeconds   int       `json:"bucket_size_seconds"`
	Provider            string    `json:"provider"`
	ProviderAccountID   string    `json:"provider_account_id"`
	Model               string    `json:"model"`
	Capability          string    `json:"capability"`
	RequestCount        int64     `json:"request_count"`
	SuccessCount        int64     `json:"success_count"`
	FailureCount        int64     `json:"failure_count"`
	FallbackCount       int64     `json:"fallback_count"`
	FinalFailureCount   int64     `json:"final_failure_count"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	EstimatedCostMicros int64     `json:"estimated_cost_microusd"`
	LatencyP50Ms        *int      `json:"latency_p50_ms"`
	LatencyP95Ms        *int      `json:"latency_p95_ms"`
	LatencyP99Ms        *int      `json:"latency_p99_ms"`
	TTFTP50Ms           *int      `json:"ttft_p50_ms"`
	TTFTP95Ms           *int      `json:"ttft_p95_ms"`
	TTFTP99Ms           *int      `json:"ttft_p99_ms"`
	RateLimitedCount    int64     `json:"rate_limited_count"`
	AuthErrorCount      int64     `json:"auth_error_count"`
	QuotaExceededCount  int64     `json:"quota_exceeded_count"`
	TimeoutCount        int64     `json:"timeout_count"`
	Provider5xxCount    int64     `json:"provider_5xx_count"`
	BadRequestCount     int64     `json:"bad_request_count"`
	NetworkErrorCount   int64     `json:"network_error_count"`
	UnsupportedCount    int64     `json:"unsupported_count"`
	UnknownErrorCount   int64     `json:"unknown_error_count"`
	HealthScore         int       `json:"health_score"`
	HealthStatus        string    `json:"health_status"`
	MainIssue           *string   `json:"main_issue"`
	CreatedAt           time.Time `json:"created_at"`
}

// ProviderProbeResult is one synthetic probe outcome (scheduled or manual).
type ProviderProbeResult struct {
	ID                  string    `json:"id"`
	Provider            string    `json:"provider"`
	ProviderAccountID   string    `json:"provider_account_id"`
	Model               string    `json:"model"`
	Capability          string    `json:"capability"`
	Status              string    `json:"status"` // success | failed
	HTTPStatus          *int      `json:"http_status"`
	LatencyMs           *int      `json:"latency_ms"`
	TTFTMs              *int      `json:"ttft_ms"`
	ErrorType           *string   `json:"error_type"`
	ErrorMessage        *string   `json:"error_message"`
	PromptTokens        *int      `json:"prompt_tokens"`
	CompletionTokens    *int      `json:"completion_tokens"`
	EstimatedCostMicros *int64    `json:"estimated_cost_microusd"`
	TriggeredBy         string    `json:"triggered_by"` // scheduled | manual | after_failure | startup
	CreatedAt           time.Time `json:"created_at"`
}
