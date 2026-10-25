package store

import (
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
)

// DefaultTenantID is the implicit tenant used in local single-user mode.
const DefaultTenantID = "default"

// Tenant scopes all data in multi-tenant deployments.
type Tenant struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// PortalUser binds one Google identity to exactly one API key for the public
// usage portal. GoogleSub is Google's stable subject id. PlanID records the
// plan used at self-provisioning time (empty for manual claims).
//
// SealedKey optionally holds the envelope-encrypted plaintext for keys the
// portal itself provisioned, so the owner can reveal them again later. It is
// empty for manually claimed keys, whose plaintext is unrecoverable.
type PortalUser struct {
	GoogleSub string
	Email     string
	KeyID     string
	PlanID    string
	SealedKey crypto.Sealed
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
	// MinDiscountPct is the customer's discount floor: requests are refused
	// unless the price they pay is at least this many percent below the
	// official list price. Zero means no floor.
	MinDiscountPct int
	// ModelDiscounts holds per-model overrides of MinDiscountPct, keyed by
	// lower-cased model (chain) name. Only filled on authentication; a value
	// of 0 turns the minimum off for that model.
	ModelDiscounts map[string]int
}

// EffectiveMinDiscount returns the minimum discount that applies to a request
// for model: the model's override when one exists, else the key default.
func (k APIKey) EffectiveMinDiscount(model string) int {
	if pct, ok := k.ModelDiscounts[NormalizeDiscountModel(model)]; ok {
		return pct
	}
	return k.MinDiscountPct
}

// NormalizeDiscountModel maps a requested model to the key used for
// per-model discounts: "chain:Name" and "name" address the same chain.
func NormalizeDiscountModel(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "chain:")
	return strings.ToLower(model)
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
	// DisplayProvider is the operator-chosen category shown in the public model
	// catalog. Empty means the legacy "combo" sentinel.
	DisplayProvider string
	// CapabilityOverrides is operator JSON (`{"vision":true,"reasoning":false}`)
	// forcing the public capability badges for this chain. Empty = heuristic.
	CapabilityOverrides string
	// Operator price for the chain model. All zero = fall back to catalog.
	InputPerM      float64
	OutputPerM     float64
	CacheWritePerM float64
	CacheReadPerM  float64
	MarketSlugs    []string
	// ReorderByMarket orders steps cheapest-first by live market slug rate at
	// request time. Off by default; requires per-step market slugs to be useful.
	ReorderByMarket bool
	// MarkupPercent overrides the global market markup for this chain. Nil
	// follows the global setting; 0 is an explicit no-markup override.
	MarkupPercent *float64
	// OfficialInputPerM/OfficialOutputPerM are the operator-set official list
	// price (USD per 1M tokens) the public discount is measured against. Nil
	// means auto: taken from market and catalog references.
	OfficialInputPerM  *float64
	OfficialOutputPerM *float64
	// Disabled takes the chain off sale: the catalog shows it as sold out and
	// requests are refused. Off by default.
	Disabled  bool
	Steps     []ChainStep
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChainStep is one candidate target within a chain.
type ChainStep struct {
	ID         string
	ChainID    string
	Position   int
	Provider   string
	Model      string
	MarketSlug string
	CreatedAt  time.Time
}

// ProviderCategory is an operator-managed display provider label used by the
// public model catalog. ID is the slug sent as provider_id.
type ProviderCategory struct {
	ID        string
	TenantID  string
	Label     string
	CreatedAt time.Time
	UpdatedAt time.Time
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

	Provider  string
	Model     string
	AccountID string
	Client    string // detected calling tool (claude-code, codex, ...) or "unknown"
	// ChainID is the routing chain that resolved the request; empty for direct
	// provider/model requests. FallbackCount > 0 means the first-choice target
	// failed and a later chain step served (or terminally failed) the request.
	ChainID       string
	FallbackCount int
	Status        string // success | cache_hit | blocked | failed | cancelled
	ErrorKind     string

	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	CacheWriteTokens int
	ReasoningTokens  int
	// CacheWrite1hTokens is the 1h-TTL subset of CacheWriteTokens.
	CacheWrite1hTokens int
	// WebSearchRequests counts provider-side web searches billed per request.
	WebSearchRequests int
	UsageSource       string // provider | estimated | cache | none

	// CostMicros remains for budget/backward compatibility. CostNanos is the
	// authoritative value and avoids each small request being rounded to zero.
	CostMicros          int64
	CostNanos           int64
	InputCostNanos      int64
	CachedCostNanos     int64
	CacheWriteCostNanos int64
	OutputCostNanos     int64
	ReasoningCostNanos  int64
	ToolCostNanos       int64 // provider-side tool use (web search) charges
	AvoidedCostNanos    int64 // semantic-cache retail-equivalent cost avoided
	SavedCostNanos      int64 // input compression retail-equivalent saving

	// Upstream cost is the pre-markup market rate cost: what the operator pays
	// the upstream provider. Zero when the request had no market slug (the
	// upstream cost is unknown, not free).
	UpstreamCostNanos  int64
	UpstreamCostMicros int64

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
	// FallbackRatePerM is the cheapest available input rate when the request was
	// served by a costlier fallback slug. Zero means not a fallback.
	FallbackRatePerM float64

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

	// Guardrail outcome: the strictest action the guardrails took ("allow"
	// when they ran and nothing fired, "" when no policy applied), the
	// detector and direction that decided it, whether a block cut a stream
	// the client was already receiving, and the evaluation overhead.
	GuardrailAction    string
	GuardrailDetector  string
	GuardrailDirection string
	GuardrailMidStream bool
	GuardrailMS        int
	PonytailActive     bool // ponytail output injection was active

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

// PaymentOrderStatus is the lifecycle state of a payment-gateway order.
type PaymentOrderStatus string

const (
	PaymentPending   PaymentOrderStatus = "pending"
	PaymentCompleted PaymentOrderStatus = "completed"
	PaymentManual    PaymentOrderStatus = "manual"
	PaymentFailed    PaymentOrderStatus = "failed"
	PaymentExpired   PaymentOrderStatus = "expired"
)

// PaymentSettleable lists the statuses a confirmed payment may still settle.
// The gateway can report success after it already reported expiry or failure
// (a customer paying as the link expires), and that money must still count.
var PaymentSettleable = []PaymentOrderStatus{PaymentPending, PaymentExpired, PaymentFailed}

// Settleable reports whether a confirmed payment can still settle the order.
func (s PaymentOrderStatus) Settleable() bool {
	for _, v := range PaymentSettleable {
		if s == v {
			return true
		}
	}
	return false
}

// PaymentOrder is one payment-gateway top-up. AmountIDR is whole rupiah,
// CreditMicros and FxRateMicros are integer micros (credit in micro-USD, rate
// in IDR-per-USD x 1e6). The rate and credit are locked at creation.
type PaymentOrder struct {
	ID                string
	TenantID          string
	KeyID             string
	GoogleSub         string
	AmountIDR         int64
	CreditMicros      int64
	FxRateMicros      int64
	Status            PaymentOrderStatus
	Provider          string
	ProviderPaymentID string
	PaymentLinkURL    string
	IdempotencyKey    string
	Actor             string
	Reason            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	PaidAt            string
	ExpiresAt         string
	// Product is ProductCredit (a top-up) or ProductHosting (a 9router
	// instance month). HostingInstanceID is set for hosting orders.
	Product           string
	HostingInstanceID string
	// TermsVersion and TermsAcceptedAt record the hosting terms the user
	// accepted at checkout. Empty for credit orders.
	TermsVersion    string
	TermsAcceptedAt string
	// Coupon fields are set when a hosting order used a coupon: the coupon,
	// the price before the discount and the extra days it grants. They are
	// frozen at checkout so later coupon edits do not change a paid order.
	CouponID      string
	CouponCode    string
	BaseAmountIDR int64
	BonusDays     int
}

// Payment order products.
const (
	ProductCredit  = "credit"
	ProductHosting = "hosting"
)

// AuditEntry is one append-only audit record. Field names follow the
// audit_log columns; the comments give the OpenTelemetry attribute each one
// exports as.
type AuditEntry struct {
	ID           string // log.record.uid
	TenantID     string
	Actor        string // user.id: dashboard session, api key id, or "system"
	ActorType    string // dashboard | api_key | system
	Action       string // event.name suffix, e.g. keys.create, auth.login
	Target       string // id of the resource acted on
	ResourceType string // keys, accounts, chains, ...
	Outcome      string // success | failure | denied
	Severity     string // INFO | WARN | ERROR
	ClientIP     string // client.address
	UserAgent    string // user_agent.original
	HTTPMethod   string // http.request.method
	HTTPRoute    string // http.route
	StatusCode   int    // http.response.status_code
	RequestID    string
	TraceID      string // W3C trace id (hex) from the caller's traceparent
	SpanID       string
	Detail       string // JSON object of extra attributes (never secrets)
	ChainID      string // writer instance whose hash chain holds the row
	ChainSeq     int64
	PrevHash     string
	Hash         string
	CreatedAt    time.Time
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
	Bucket int `json:"ts"` // bucket start, epoch seconds

	// CPU percentages are shares of the whole host (0–100).
	ProcCPUAvg float64 `json:"proc_cpu_avg"`
	ProcCPUMax float64 `json:"proc_cpu_max"`
	HostCPUAvg float64 `json:"host_cpu_avg"`
	HostCPUMax float64 `json:"host_cpu_max"`

	ProcRSSAvg     float64 `json:"proc_rss_avg_mb"`
	ProcRSSMax     int64   `json:"proc_rss_max_bytes"`
	HostMemUsedAvg float64 `json:"host_mem_used_avg_mb"`
	HostMemUsedMax int64   `json:"host_mem_used_max_bytes"`
	HostMemPctAvg  float64 `json:"host_mem_pct_avg"`
	HostMemPctMax  float64 `json:"host_mem_pct_max"`

	GoroutinesAvg float64 `json:"goroutines_avg"`
	GoroutinesMax int64   `json:"goroutines_max"`
	HeapAllocAvg  float64 `json:"heap_avg_mb"`
	HeapAllocMax  int64   `json:"heap_max_bytes"`

	NetSentDelta int64 `json:"net_sent_delta"`
	NetRecvDelta int64 `json:"net_recv_delta"`

	GCPauseAvg float64 `json:"gc_pause_avg_ms"`
	GCPauseMax int64   `json:"gc_pause_max_ns"`

	InflightAvg float64 `json:"inflight_avg"`
	InflightMax int64   `json:"inflight_max"`

	Samples int `json:"samples"`
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

// UpstreamAccount is a prepaid account with an upstream model provider.
// Amounts are USD micros.
type UpstreamAccount struct {
	ID, Name, URL string
	// Status is "active", "planned" or "archived".
	Status               string
	OpeningBalanceMicros int64
	OpeningAt            time.Time
	LowBalanceMicros     int64
	// AlertFiredAt is zero while the low-balance alert is armed.
	AlertFiredAt time.Time
	// Providers lists the provider ids mapped to this account; filled by List/Get.
	Providers            []string
	CreatedAt, UpdatedAt time.Time
}

// UpstreamTopup is a recorded top-up of an upstream account.
type UpstreamTopup struct {
	ID, AccountID string
	AmountMicros  int64
	TopupAt       time.Time
	Note, Actor   string
	CreatedAt     time.Time
}
