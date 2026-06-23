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
	Steps            []ChainStep
	CreatedAt        time.Time
	UpdatedAt        time.Time
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

// UsageRecord meters one completed request.
type UsageRecord struct {
	ID               string
	TenantID         string
	ProjectID        string
	APIKeyID         string
	Provider         string
	Model            string
	AccountID        string
	Client           string // detected calling tool (claude-code, codex, ...) or "unknown"
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	CacheWriteTokens int
	CostMicros       int64
	CacheHit         bool
	LatencyMS        int
	TTFTMS           int    // time-to-first-token in ms (0 for non-streaming or cache hits)
	SlimBytesSaved   int    // bytes removed by RTK slimmer (input-side compression)
	SlimTokensSaved  int    // estimated tokens saved by RTK (bytes/4)
	SlimRules        string // comma-separated rule names that fired (e.g. "git-diff,grep")
	HeadroomActive   bool   // Headroom compression was active
	HeadroomTokensBefore int // exact input tokens before Headroom compression
	HeadroomTokensAfter  int // exact input tokens after Headroom compression
	HeadroomTokensSaved  int // exact tokens saved by Headroom
	HeadroomCompressionRatio float64 // Headroom tokens_after/tokens_before
	HeadroomTransforms string // comma-separated Headroom transforms
	HeadroomCCRHashes  string // comma-separated CCR hashes
	CavemanActive    bool   // caveman output compression was active
	TerseActive      bool   // terse output compression was active
	CreatedAt        time.Time
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
