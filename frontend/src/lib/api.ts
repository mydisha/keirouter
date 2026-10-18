// Typed client for the KeiRouter admin API. All calls go through the dev-server
// proxy (or the embedded static server in production) to /api.

export interface RegionOption {
  id: string;
  label: string;
  base_url: string;
}

export interface Provider {
  id: string;
  display_name: string;
  alias: string;
  dialect: string;
  auth_kind: string;
  auth_modes: string[];
  service_kinds: string[];
  color: string;
  website: string;
  api_key_url: string;
  icon: string;
  deprecated: boolean;
  hidden: boolean;
  pinned: boolean;
  notice: string;
  drivable: boolean;
  input_per_m: number;
  output_per_m: number;
  regions?: RegionOption[];
  default_region?: string;
  // base_url is populated for user-defined custom provider instances.
  base_url?: string;
  // custom marks user-defined dynamic provider instances (editable/deletable).
  custom?: boolean;
  // Provider-specific connect-form inputs (cloud region, project id, extra
  // secrets). Values are sent as AccountInput.fields keyed by field key.
  fields?: CredentialField[];
  // api_key_mode: absent = key required, "optional" = other fields may
  // authenticate instead, "hidden" = no key input at all.
  api_key_mode?: "optional" | "hidden";
  // api_key_when shows the key input only for some values of a select field.
  api_key_when?: FieldCondition;
  api_key_label?: string;
  api_key_placeholder?: string;
  // base_url_required marks built-in providers that need a per-account URL.
  base_url_required?: boolean;
  base_url_placeholder?: string;
}

export interface FieldCondition {
  field: string;
  values: string[];
}

export interface CredentialField {
  key: string;
  label: string;
  placeholder?: string;
  hint?: string;
  required?: boolean;
  secret?: boolean;
  multiline?: boolean;
  url?: boolean;
  advanced?: boolean;
  default?: string;
  options?: { value: string; label: string }[];
  when?: FieldCondition;
}

// ProviderModel is a single model entry returned by providerModels(). Custom
// models carry a db_id so they can be edited/removed; discovered marks models
// that came from the upstream /models endpoint rather than the static catalog.
export interface ProviderModel {
  id: string;
  name: string;
  kind: string;
  custom?: boolean;
  db_id?: string;
  discovered?: boolean;
	capabilities?: ModelCapabilities;
	capability_source?: CapabilitySource;
}

export type CapabilitySource = "provider" | "exact" | "pattern" | "service_kind" | "default";

export interface ModelCapabilities {
	vision: boolean;
	pdf: boolean;
	audio_input: boolean;
	video_input: boolean;
	image_output: boolean;
	audio_output: boolean;
	search: boolean;
	tools: boolean;
	reasoning: boolean;
	structured_output: boolean;
	context_window: number;
	max_output: number;
}

// CustomProvider is a user-defined provider instance (OpenAI- or Anthropic-
// compatible) with its own unique id, base URL, accounts, and models.
export interface CustomProvider {
  id: string;
  display_name: string;
  alias: string;
  dialect: string; // "openai" | "anthropic"
  base_url: string;
  custom: true;
  created_at?: string;
  updated_at?: string;
}

// CustomModel is a user-registered model on a provider (custom or built-in).
export interface CustomModel {
  db_id: string;
  provider_id: string;
  id: string;
  name: string;
  kind: string;
  context_window: number;
  input_per_m: number;
  output_per_m: number;
}

export interface CustomModelInput {
  id: string;
  name?: string;
  kind?: string;
  context_window?: number;
  input_per_m?: number;
  output_per_m?: number;
}


export interface BrandingSettings {
  name: string;
  logo_url: string;
  favicon_url: string;
  tagline: string;
  color_palette: string;
  api_key_prefix: string;
  turnstile_enabled?: boolean;
  turnstile_site_key?: string;
}

export interface LandingNotification {
  id: string;
  tag?: string;
  title: string;
  body: string;
  href?: string;
}

export interface EndpointSettings {
  rtk_enabled: boolean;
  rtk_filter_level: string;
  caveman_enabled: boolean;
  caveman_level: string;
  terse_enabled: boolean;
  terse_level: string;
  headroom_enabled: boolean;
  headroom_url: string;
  headroom_compress_user_messages: boolean;
  headroom_timeout_ms: number;
  ponytail_enabled: boolean;
  ponytail_level: "lite" | "full" | "ultra";
  routing_strategy: string;
  sticky_limit: number;
  combo_strategy: string;
  combo_sticky_limit: number;
  outbound_proxy_enabled: boolean;
  outbound_proxy_url: string;
  outbound_no_proxy: string;
  observability_enabled?: boolean;
  rate_limits_enabled: boolean;
  // Rate-limit tuning. The server fills unset values from config.yaml.
  tpm_reconcile_enabled?: boolean;
  tpm_output_floor?: number;
  tpm_output_reserve_cap?: number;
  concurrency_retry_after_ms?: number;
  auth_failures_per_minute?: number;
  login_attempts_per_minute?: number;
  stream_stall_timeout_ms: number;
  response_header_timeout_ms: number;
  request_timeout_ms: number;
}

export interface CurrencyConfig {
  auto_refresh_enabled: boolean;
  refresh_interval_h: number;
  override_enabled: boolean;
  override_rate: number;
  source_url: string;
  rate: number;
  fetched_at: string;
  source: string;
  last_error: string;
}

export interface CurrencyStatus {
  config: CurrencyConfig;
  effective_rate: number;
  source: string;
  fetched_at: string;
  last_error: string;
}

/** One Surplus-priced chain in the admin Public pricing tab. */
export interface PublicPricingModel {
  chain_id: string;
  model: string;
  published: boolean;
  available: boolean;
  input_per_m: number | null;
  output_per_m: number | null;
  max_input_per_m: number | null;
  max_output_per_m: number | null;
  discount_pct: number | null;
}

/** Which chains GET /v1/public/pricing shows. */
export interface PublicPricingSettings {
  enabled: boolean;
  chain_ids: string[];
  models: PublicPricingModel[];
  path: string;
}

export interface MarketPricingSettings {
  auto_refresh: boolean;
  refresh_interval_seconds: number;
  markup_percent: number;
  safety_margin_enabled: boolean;
  safety_margin_percent: number;
  /** Distinct trusted price levels Surplus may fail over across for surplus-trusted slugs (0 = no ceiling). */
  surplus_fallback_depth: number;
  last_fetched_at: string;
  last_fetch_error?: string;
  last_synced_count: number;
}

export interface ProviderRoutingSettings {
  routing_strategy: "inherit" | "fill-first" | "round-robin" | "smart-round-robin" | string;
  sticky_limit: number;
  affinity_ttl_minutes: number;
}

// HeadroomTestResult is returned by POST /settings/headroom-test and reports
// whether the configured Headroom proxy is reachable and behaving correctly.
// endpoint is always masked (no credentials/query string).
export interface HeadroomTestResult {
  ok: boolean;
  reachable: boolean;
  status: number;
  latency_ms: number;
  endpoint: string;
  message: string;
}

export interface OAuthProvider {
  provider: string;
  display_name: string;
  flow: string; // authorization_code_pkce | authorization_code | device_code
  icon: string;
  color: string;
  callback_path?: string;
  fixed_port?: number;
  loopback_host?: string;
}

export interface DeviceCode {
  device_code: string;
  user_code: string;
  verification_uri: string;
  verification_uri_complete: string;
  expires_in: number;
  interval: number;
  // Client-device-code step 1 response (browser must make the upstream call).
  _client_device_code?: boolean;
  _pkce_challenge?: string;
  _pkce_nonce?: string;
  _device_code_url?: string;
  _client_id?: string;
  _scopes?: string[];
  _pkce_method?: string;
}

export interface OAuthPollResult {
  status: string; // pending | complete
  slow_down?: boolean;
  id?: string;
  provider?: string;
}

export interface OAuthCallbackStatus {
  status: "pending" | "success" | "error" | "expired" | string;
  message?: string;
}

export interface Plan {
  id: string;
  name: string;
  description: string;
  limit_micros: number;
  limit_tokens: number;
  rpm_limit: number;
  tpm_limit: number;
  concurrency_limit: number;
  period: string;
  alert_pct: number;
  hard_cutoff: boolean;
  allowed_models: string[] | null;
  key_count: number;
  created_at: string;
  updated_at: string;
}

export interface APIKey {
  id: string;
  name: string;
  display: string;
  disabled: boolean;
  plan_id: string;
  plan_name?: string;
  created_at: string;
  last_used_at?: string | null;
  allowed_models?: string[];
  models_source?: "key" | "plan" | "all";
}

export interface CreatedKey {
  id: string;
  name: string;
  key: string;
  display: string;
  plan_id: string;
  budget?: {
    id: string;
    scope_kind: string;
    limit_micros: number;
    limit_tokens: number;
    period: string;
    alert_pct: number;
    hard_cutoff: boolean;
  };
  allowed_models?: string[];
  plan?: {
    id: string;
    name: string;
  };
}

export interface Account {
  id: string;
  provider: string;
  label: string;
  auth_kind: string;
  priority: number;
  disabled: boolean;
  proxy_pool_id?: string;
  needs_reconnect?: boolean;
  created_at: string;
}

export interface AccountInput {
  provider: string;
  label: string;
  api_key?: string;
  base_url?: string;
  region?: string;
  account_id?: string;
  azure_endpoint?: string;
  azure_deployment?: string;
  azure_api_version?: string;
  azure_organization?: string;
  fields?: Record<string, string>;
  proxy_pool_id?: string;
  priority?: number;
}

// BulkAccountItem is one credential in a bulk import. Only api_key (and an
// optional per-item base_url / label) varies per row; shared provider config
// lives on BulkAccountInput.
export interface BulkAccountItem {
  label?: string;
  api_key?: string;
  base_url?: string;
}

export interface BulkAccountInput {
  provider: string;
  base_url?: string;
  region?: string;
  account_id?: string;
  azure_endpoint?: string;
  azure_deployment?: string;
  azure_api_version?: string;
  azure_organization?: string;
  fields?: Record<string, string>;
  priority?: number;
  proxy_pool_id?: string;
  validate?: boolean;
  items: BulkAccountItem[];
}

export interface BulkAccountResult {
  index: number;
  label: string;
  status: "created" | "error" | "skipped";
  id?: string;
  error?: string;
}

export interface BulkAccountResponse {
  total: number;
  created: number;
  failed: number;
  skipped: number;
  results: BulkAccountResult[];
}

export interface ChainStep {
  provider: string;
  model: string;
  position: number;
  market_slug?: string;
}

export interface Chain {
  id: string;
  name: string;
  strategy: string;
  display_provider?: string;
  capability_overrides?: string;
  fallback_provider?: string;
  fallback_model?: string;
  input_per_m: number;
  output_per_m: number;
  cache_write_per_m: number;
  cache_read_per_m: number;
  market_slugs: string[];
  reorder_by_market?: boolean;
  /** Per-chain markup override; null/undefined follows the global market markup. */
  markup_percent?: number | null;
  /** No live market quote for any bound slug: stored rates are stale and requests are refused. */
  sold_out?: boolean;
  /** Manual official price per 1M tokens; null/undefined = detected automatically. */
  official_input_per_m?: number | null;
  official_output_per_m?: number | null;
  /** Automatically detected official price; null when none is found. */
  official_auto?: { input_per_m: number; output_per_m: number; source: "models.dev"; provider: string } | null;
  steps: ChainStep[];
}

export interface Budget {
  id: string;
  scope_kind: string;
  scope_id: string;
  limit_micros: number;
  limit_tokens: number;
  period: string;
  alert_pct: number;
  hard_cutoff: boolean;
}

export interface BudgetStatus {
  id: string;
  scope_kind: string;
  scope_id: string;
  scope_name: string;
  limit_micros: number;
  limit_tokens: number;
  period: string;
  alert_pct: number;
  hard_cutoff: boolean;
  spent_micros: number;
  spent_tokens: number;
  pct_used: number;
  tokens_pct_used: number;
  period_start: string;
  /** When the budget window resets; null for "total" budgets. */
  resets_at: string | null;
}

export interface KeyTopup {
  id: string;
  amount_usd: number;
  reason: string;
  limit_before_usd: number;
  limit_after_usd: number;
  created_at: string;
}

export interface KeyLimitAdjustment {
  id: string;
  delta_usd: number;
  reason: string;
  limit_before_usd: number;
  limit_after_usd: number;
  created_at: string;
}

export interface BansosCredit {
  limit_usd: number;
  spent_usd: number;
  remaining_usd: number;
  period: string;
}

export interface Bansos {
  exists: boolean;
  key_id?: string;
  plan_id?: string;
  active: boolean;
  mode: "credit" | "unlimited";
  masked_display: string;
  allowed_models: string[];
  rpm: number;
  tpm: number;
  credit: BansosCredit | null;
  notice_enabled?: boolean;
  notice_text?: string;
  notice_rate?: number;
  updated_at?: string;
  key?: string;
}

export interface UsageSummary {
  total_requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  cost_usd: number;
  cache_hits: number;
  since: string;
}

export type UsageTerminalStatus = "success" | "cache_hit" | "blocked" | "failed" | "cancelled";
export type UsageSource = "provider" | "estimated" | "cache" | "legacy" | "none";
export type PricingStatus = "priced" | "estimated" | "free" | "missing" | "partial" | "legacy" | "none" | "mixed";

export interface ProviderUsage {
  provider: string;
  display_name: string;
  color: string;
  icon: string;
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
  /** Guardrail blocks; excluded from success_rate and failed_requests. */
  blocked_requests: number;
  success_rate: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  cost_usd: number;
  saved_cost_usd: number;
  avoided_cost_usd: number;
  avg_latency_ms: number;
  avg_ttft_ms: number;
  pricing_eligible_requests: number;
  unpriced_requests: number;
  estimated_requests: number;
  estimated_usage_requests: number;
  legacy_usage_requests: number;
  backfilled_requests: number;
	pricing_request_coverage: number | null;
  share_pct: number;
  token_share_pct: number;
}

export interface RecentActivity {
  id: string;
  request_id: string;
  api_key_id: string;
  /** Resolved key name; empty when the key was deleted or the request was unauthenticated. */
  api_key_name: string;
  /** Portal user's email for the key; empty for operator or Bansos keys. */
  user_email?: string;
  account_id: string;
  account_label: string;
  /** Calling client detected from the request (e.g. "claude-code"). */
  client: string;
  provider: string;
  provider_name: string;
  provider_color: string;
  provider_icon: string;
  model: string;
  status: UsageTerminalStatus;
  error_kind: string;
  usage_source: UsageSource;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
  tokens: number;
  cost_usd: number;
  input_cost_usd: number;
  cached_cost_usd: number;
  cache_write_cost_usd: number;
  output_cost_usd: number;
  reasoning_cost_usd: number;
  saved_cost_usd: number;
  avoided_cost_usd: number;
  pricing_status: PricingStatus;
  pricing_source: string;
  pricing_key: string;
  pricing_match_kind: string;
  pricing_source_url: string;
  pricing_as_of: string | null;
  pricing_backfilled: boolean;
  input_rate_per_m: number;
  cached_rate_per_m: number;
  cache_write_rate_per_m: number;
  output_rate_per_m: number;
  reasoning_rate_per_m: number;
  fallback_rate_per_m: number;
  cache_hit: boolean;
  latency_ms: number;
  upstream_latency_ms: number;
  end_to_end_latency_ms: number;
  ttft_ms: number;
  slim_bytes_saved: number;
  slim_tokens_saved: number;
  slim_rules: string;
  slim_active: boolean;
  caveman_active: boolean;
  terse_active: boolean;
  headroom_tokens_saved: number;
  headroom_bytes_saved: number;
  headroom_active: boolean;
  ponytail_active: boolean;
  created_at: string;
}

export interface RuleSaving {
  rule: string;
  count: number;
  bytes_saved: number;
  tokens_saved: number;
}

export interface ClientSaving {
  client: string;
  requests: number;
  optimized_requests: number;
  bytes_saved: number;
  tokens_saved: number;
  slim_tokens_saved: number;
  caveman_requests: number;
  terse_requests: number;
  headroom_tokens_saved: number;
  ponytail_requests: number;
  saved_cost_usd: number;
  avoided_cost_usd: number;
  usd_saved: number;
}

export interface TokenSavings {
  slim_bytes_saved: number;
  slim_tokens_saved: number;
  headroom_tokens_saved: number;
  total_tokens_saved: number;
  saved_tokens_per_request: number;
  saved_tokens_per_optimized_request: number;
  optimized_requests: number;
  caveman_requests: number;
  terse_requests: number;
  headroom_requests: number;
  ponytail_requests: number;
  saved_cost_usd: number;
  avoided_cost_usd: number;
  usd_saved: number;
  usd_saved_estimate: boolean;
  rules: RuleSaving[];
  by_client: ClientSaving[];
}

export interface ModelUsage {
  provider: string;
  provider_name: string;
  provider_color: string;
  provider_icon: string;
  model: string;
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
  /** Guardrail blocks; excluded from success_rate and failed_requests. */
  blocked_requests: number;
  success_rate: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  cost_usd: number;
  saved_cost_usd: number;
  avoided_cost_usd: number;
  avg_latency_ms: number;
  avg_ttft_ms: number;
  pricing_eligible_requests: number;
	unpriced_requests: number;
	missing_pricing_requests: number;
	legacy_pricing_requests: number;
  estimated_requests: number;
  estimated_usage_requests: number;
  legacy_usage_requests: number;
  backfilled_requests: number;
	pricing_request_coverage: number | null;
  pricing_status: PricingStatus;
  pricing_mixed: boolean;
  pricing_source: string;
  pricing_key: string;
  input_per_m: number;
  cached_input_per_m: number;
  cache_write_per_m: number;
  output_per_m: number;
  reasoning_per_m: number;
}

export interface SeriesPoint {
  label: string;
  start: string;
  count: number;
  requests: number;
  /** Succeeded requests; guardrail blocks and cancellations are in neither this nor failures. */
  successes?: number;
  failures: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
  /** Optimizer savings (input compression + semantic-cache avoided cost). */
  saved_usd: number;
  cache_hits: number;
  /** Mean end-to-end latency of successful requests in the bucket. */
  avg_latency_ms: number;
}

/** One chain step's share of the chain's traffic, from persisted usage rows. */
export interface ChainStepUsage {
  position: number;
  provider: string;
  model: string;
  /** True for the chain's configured last-resort fallback model. */
  is_fallback: boolean;
  requests: number;
  share: number;
  success_rate: number;
  cost_usd: number;
}

export interface ChainUsage {
  chain_id: string;
  name: string;
  strategy: string;
  requests: number;
  success_rate: number;
  /** Requests that reached their serving target only after failing over. */
  fallback_requests: number;
  fallback_rate: number;
  cost_usd: number;
  steps: ChainStepUsage[];
  /** Targets that served traffic but are no longer steps of the chain. */
  other_targets: { provider: string; model: string; requests: number; share: number }[];
}

/** Headline rollup of the equal-length window immediately before the period. */
export interface PreviousPeriodSummary {
  since: string;
  until: string;
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
  /** Guardrail blocks; excluded from success_rate and failed_requests. */
  blocked_requests: number;
  success_rate: number;
  total_tokens: number;
  cost_usd: number;
  usd_saved: number;
  cache_hits: number;
  avg_latency_ms: number;
  p50_latency_ms: number;
}

export interface UsageInsightsSummary {
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
  /** Guardrail blocks; excluded from success_rate and failed_requests. */
  blocked_requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  cost_usd: number;
  cost_per_request_usd: number;
  tokens_per_request: number;
  cache_hits: number;
  success_rate: number;
  avg_latency_ms: number;
  p50_latency_ms: number;
  p95_latency_ms: number;
  avg_ttft_ms: number;
  pricing_eligible_requests: number;
  priced_requests: number;
  unpriced_requests: number;
  unpriced_tokens: number;
  estimated_requests: number;
  estimated_usage_requests: number;
  estimated_usage_tokens: number;
  legacy_usage_requests: number;
  legacy_usage_tokens: number;
  backfilled_requests: number;
	pricing_request_coverage: number | null;
	pricing_token_coverage: number | null;
  since: string;
}

export interface UsageInsights {
  period: string;
  since: string;
  generated_at: string;
  summary: UsageInsightsSummary;
  previous: PreviousPeriodSummary;
  savings: TokenSavings;
  providers: ProviderUsage[];
  recent: RecentActivity[];
  series: SeriesPoint[];
  busiest: string;
}

export interface KeyModelUsage {
  provider: string;
  provider_name: string;
  provider_icon: string;
  model: string;
  requests: number;
  success_requests: number;
  failed_requests: number;
  blocked_requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  cost_usd: number;
  avg_latency_ms: number;
  last_used_at: string;
}

export interface LeaderboardEntry {
  key_id: string;
  key_name: string;
  /** Portal user's email; empty for operator or Bansos keys. */
  email: string;
  requests: number;
  tokens: number;
  spend_usd: number;
  topup_count: number;
  topup_idr: number;
  topup_usd: number;
}

export interface UsageLeaderboard {
  period: string;
  since: string;
  generated_at: string;
  topups: LeaderboardEntry[];
  spend: LeaderboardEntry[];
  tokens: LeaderboardEntry[];
  requests: LeaderboardEntry[];
}

export interface ModelUsageResponse {
  period: string;
  since: string;
  generated_at: string;
  models: ModelUsage[];
}

export interface UpstreamQuota {
  resource_type: string;
  used: number;
  limit: number;
  remaining: number;
  reset_at?: string;
}

export interface CodexCreditInfo {
  id?: string;
  redeem_request_id?: string;
  status: string;
  granted_at?: string;
  expires_at?: string;
  title?: string;
  description?: string;
}

export interface CodexResetCredits {
  available_count: number;
  credits: CodexCreditInfo[];
}

export interface CodexConsumeResult {
  ok: boolean;
  no_credit: boolean;
  status: number;
  code?: string;
  outcome?: string;
  windows_reset?: number;
  message?: string;
}

export interface CodexUsageData {
  plan_type: string;
  allowed: boolean;
  limit_reached: boolean;
  primary_used_percent: number;
  primary_reset_at: number;
  primary_window_seconds: number;
  secondary_used_percent: number;
  secondary_reset_at: number;
  secondary_window_seconds: number;
  credits_balance: string;
  has_credits: boolean;
  unlimited: boolean;
  reset_credits_available: number;
}

export interface CodexUsageDetails {
  usage_data?: CodexUsageData;
  reset_credits?: CodexResetCredits;
  error?: string;
}

export interface QuotaAccount {
  id: string;
  provider: string;
  provider_name: string;
  label: string;
  auth_kind: string;
  priority: number;
  status: string; // active | paused | needs_attention
  usage_type: string; // compatibility field; not a paid/free classification
  quota_supported?: boolean;
  quota_state?: "reported" | "pending" | "paused" | "unavailable" | "error" | "usage_only";
  total_requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  cost_usd: number;
  input_per_m: number;
  output_per_m: number;
  notice?: string;
  plan_name?: string;
  message?: string;
  upstream_quotas?: UpstreamQuota[];
  updated_at: string;
}

// Console log uses structured entries streamed via SSE (/api/console/stream)
// and fetched as history from /api/console, which returns { logs: ConsoleLogEntry[] }.
// Request lines share `req`; exactly one of them is `final` and summarizes the
// outcome. Other kinds (oauth, health, tunnel, admin) are background events.
export interface ConsoleLogEntry {
  seq: number;
  time: string; // HH:MM:SS.mmm
  ts?: number; // unix ms
  level: string; // DEBUG | INFO | WARN | ERROR | LOG
  msg: string; // human-readable summary
  detail?: string; // optional technical detail, revealed on expand
  hint?: string; // plain-language explanation for operators
  kind?: string; // request | admin | oauth | health | tunnel | ...
  req?: string; // request ID
  endpoint?: string; // "POST /v1/chat/completions"
  client?: string;
  key?: string; // API key name
  model?: string; // model the client asked for
  provider?: string;
  target?: string; // provider/model that served (or failed) the request
  status?: number; // HTTP status returned to the client (499 = client disconnected)
  ms?: number; // elapsed since the request arrived
  tokens?: number;
  cost_micros?: number;
  fallbacks?: number;
  stream?: boolean;
  cache?: boolean;
  final?: boolean;
}

export interface ProxyPool {
  id: string;
  name: string;
  type: string; // http | vercel | cloudflare | deno
  proxy_url: string;
  no_proxy: string;
  strict: boolean;
  is_active: boolean;
  test_status: string; // unknown | testing | active | error
  last_tested?: string;
  last_error?: string;
  /** Accounts (any tenant) routed through this pool. */
  bound_connection_count?: number;
}

/** Limits which requests a skill applies to; empty lists mean "any". */
export interface SkillScope {
  plans?: string[];
  keys?: string[];
  models?: string[];
}

export interface Skill {
  id: string;
  name: string;
  description: string;
  prompt: string;
  enabled: boolean;
  /** Clients may drop it with the X-KeiRouter-Skip-Skills header. */
  optional: boolean;
  scope?: SkillScope;
  /** Values for {{name}} placeholders in prompt, filled in by the gateway. */
  variables?: Record<string, string>;
  created_at: string;
  updated_at?: string;
}

export type SkillInput = { name?: string; description?: string; prompt?: string; enabled?: boolean; optional?: boolean; scope?: SkillScope; variables?: Record<string, string> };

export interface AccessSettings {
  local_enabled: boolean;
  tunnel_enabled: boolean;
  tailscale_enabled: boolean;
  tunnel_url?: string;
  tailscale_url?: string;
  endpoint_url: string;
}

export interface TunnelStatus {
  enabled: boolean;
  settingsEnabled: boolean;
  tunnelUrl: string;
  shortId: string;
  publicUrl: string;
  running: boolean;
}

export interface TailscaleStatus {
  enabled: boolean;
  settingsEnabled: boolean;
  tunnelUrl: string;
  running: boolean;
  loggedIn: boolean;
  installed: boolean;
  platform: string;
}

export interface TunnelCombinedStatus {
  tunnel: TunnelStatus;
  tailscale: TailscaleStatus;
  download: { downloading: boolean; progress: number };
}

export interface TunnelEnableResult {
  success: boolean;
  tunnelUrl: string;
  shortId: string;
  publicUrl: string;
  alreadyRunning?: boolean;
}

export interface TailscaleCheckResult {
  installed: boolean;
  loggedIn: boolean;
  platform: string;
  daemonRunning: boolean;
  hasCachedPassword: boolean;
}

export interface TailscaleEnableResult {
  success: boolean;
  tunnelUrl?: string;
  needsLogin?: boolean;
  authUrl?: string;
  funnelNotEnabled?: boolean;
  enableUrl?: string;
  error?: string;
}

/** Connection currently stored in a CLI tool's config. The plaintext key never
 *  leaves the server — only a masked hint and whether it authenticates. */
export interface CLIToolSettings {
  base_url: string;
  model?: string;
  api_key_hint?: string;
  key_status: "valid" | "invalid" | "disabled" | "missing";
  key_name?: string;
  /** The stored endpoint addresses this gateway. */
  endpoint_match: boolean;
}

export interface CLITool {
  id: string;
  name: string;
  dialect: string;
  instructions: string;
  snippet: string;
  installed: boolean;
  /** Install signals that matched, strongest first. */
  detected_via?: ("binary" | "extension" | "config")[];
  /** The config points at a gateway (see settings.endpoint_match for which). */
  configured: boolean;
  config_path: string;
  config_files?: string[];
  /** Copy of the primary config taken before KeiRouter first edited it. */
  backup_path?: string;
  binary_path?: string;
  /** Installed IDE extension, e.g. "github.copilot-chat@0.31.2 in Cursor". */
  extension?: string;
  version?: string;
  model_required: boolean;
  settings?: CLIToolSettings;
  /** Detection problem other than "not installed" (unreadable config, timeout). */
  error?: string;
}

export interface CLIToolsResponse {
  base_url: string;
  model: string;
  tools: CLITool[];
}

export interface AuthStatus {
  authenticated: boolean;
  using_default: boolean;
  onboarding_complete: boolean;
}

export interface SystemSnapshot {
  /** Epoch seconds of the collector tick these numbers come from. */
  sampled_at: number;
  /** Seconds the CPU percentages are averaged over. */
  window_s: number;
  cpu_pct: number;
  cpu_per_core: number[];
  cpu_count: number;
  cpu_model?: string;
  /** 1, 5 and 15 minute load average; absent on Windows. */
  load_avg?: number[];
  mem_total_mb: number;
  mem_used_mb: number;
  mem_available_mb: number;
  mem_pct: number;
  swap_total_mb: number;
  swap_used_mb: number;
  disk_path: string;
  disk_total_gb: number;
  disk_used_gb: number;
  disk_free_gb: number;
  disk_pct: number;
  goroutines: number;
  heap_alloc_mb: number;
  heap_sys_mb: number;
  heap_inuse_mb: number;
  heap_idle_mb: number;
  next_gc_mb: number;
  gc_pause_total_ms: number;
  gc_pause_last_ms: number;
  gc_cycles: number;
  uptime_s: number;
  host_uptime_s: number;
  pid: number;
  host: string;
  os: string;
  arch: string;
  go_version: string;
  // Process-level metrics
  /** Share of the whole machine's CPU capacity, 0–100. */
  proc_cpu_pct: number;
  /** Cores' worth of CPU in use. */
  proc_cpu_cores: number;
  proc_rss_mb: number;
  proc_threads: number;
  proc_open_fds: number;
  proc_fd_limit?: number;
  proc_conns: number;
  proc_conns_established: number;
}

export interface SystemSample {
  ts: number;
  cpu_pct: number;
  mem_pct: number;
  goroutines: number;
  heap_mb: number;
  cpu_spike?: boolean;
  mem_spike?: boolean;
  // Process-level metrics
  proc_cpu_pct: number;
  proc_rss_mb: number;
  proc_threads?: number;
  proc_open_fds?: number;
}

export interface SystemHistory {
  interval_sec: number;
  max_size: number;
  thresholds: { cpu_pct: number; mem_pct: number; disk_pct: number };
  spikes: SystemSample[];
  samples: SystemSample[];
}

/** One aggregated slice of the persisted resource timeline (7-day retention). */
export interface SystemResourceBucket {
  ts: number;
  proc_cpu_avg: number;
  proc_cpu_max: number;
  host_cpu_avg: number;
  host_cpu_max: number;
  proc_rss_avg_mb: number;
  proc_rss_max_bytes: number;
  host_mem_pct_avg: number;
  host_mem_pct_max: number;
  goroutines_avg: number;
  goroutines_max: number;
  heap_avg_mb: number;
  heap_max_bytes: number;
  samples: number;
}

// ============================================================================
// Guardrails
// ============================================================================

export type GuardrailScope = "global" | "provider" | "model" | "chain" | "apikey";
export type GuardrailAction = "allow" | "log_only" | "warn" | "mask" | "block";
export type GuardrailSeverity = "low" | "medium" | "high";
export type PIIStrategy = "redact" | "replace" | "mask" | "hash" | "block" | "anonymize";

export interface PIIConfig {
  enabled: boolean;
  types?: string[];
  strategy?: PIIStrategy;
  min_score?: number;
  scan_output?: boolean;
  engine?: string;
  /** Entities that refuse the request whatever the strategy. */
  block_types?: string[];
  /** Exact values never treated as PII (case-insensitive). */
  allow_list?: string[];
  /** Which parts of the conversation are scanned. Defaults to "all". */
  scope?: "all" | "user" | "last_user";
}

export interface InjectionConfig {
  enabled: boolean;
  severity_threshold?: GuardrailSeverity;
  action?: GuardrailAction;
  /** Also inspect tool results for indirect prompt injection. */
  scan_tool_results?: boolean;
  /** ML classifier layer (Prompt Guard 2 sidecar). */
  classifier?: boolean;
  /** Injection probability treated as an attack (0–1, default 0.9). */
  classifier_threshold?: number;
  /** Embedding match against known attacks. */
  semantic?: boolean;
  /** Cosine similarity treated as a match (0–1, default 0.85). */
  semantic_threshold?: number;
  /** LLM judge mode. */
  judge?: "off" | "suspicious" | "always";
  /** Model, alias or chain the judge runs on. */
  judge_model?: string;
  /** Wrap tool results in secret delimiters marked as data. */
  spotlight?: boolean;
  /** Plant a canary in the system prompt and block responses that leak it. */
  canary?: boolean;
  /** Refuse tool calls after tool results that look injected. */
  guard_tool_calls?: boolean;
}

export interface TopicsConfig {
  enabled: boolean;
  mode?: "allow" | "block";
  topics?: string[];
  action?: GuardrailAction;
  engine?: "keyword" | "embedding" | "llm";
  similarity_threshold?: number;
  /** Model, alias or chain the "llm" engine runs on. */
  model?: string;
}

export interface ToxicityConfig {
  enabled: boolean;
  categories?: string[];
  threshold?: number;
  action?: GuardrailAction;
  engine?: "native" | "openai";
}

export interface BiasConfig {
  enabled: boolean;
  categories?: string[];
  threshold?: number;
  action?: GuardrailAction;
}

export interface GuardrailPolicyConfig {
  enabled?: boolean;
  pii?: PIIConfig;
  injection?: InjectionConfig;
  topics?: TopicsConfig;
  toxicity?: ToxicityConfig;
  bias?: BiasConfig;
}

export interface GuardrailPolicy {
  id: string;
  name: string;
  scope: GuardrailScope;
  scope_id: string;
  enabled: boolean;
  config: GuardrailPolicyConfig;
  created_at: string;
  updated_at: string;
}

export interface GuardrailFinding {
  entity: string;
  score: number;
  start: number;
  end: number;
  original?: string;
  redacted?: string;
}

export interface GuardrailDecision {
  detector: string;
  action: GuardrailAction;
  severity?: GuardrailSeverity;
  reason?: string;
  findings?: GuardrailFinding[];
  direction?: "inbound" | "outbound";
}

export interface GuardrailTestResult {
  action: GuardrailAction;
  reason: string;
  decisions: GuardrailDecision[];
  /** The sample as it would reach the provider after masking, when changed. */
  masked_text?: string | null;
}

export interface GuardrailLogEntry {
  id: string;
  request_id: string;
  api_key_id: string;
  provider: string;
  model: string;
  chain_id: string;
  detector: string;
  direction: "inbound" | "outbound";
  action: GuardrailAction;
  severity: GuardrailSeverity | "";
  reason: string;
  findings: GuardrailFinding[] | null;
  created_at: string;
}

// GuardrailLogFilter narrows the audit log; the stats view drills down with it.
export interface GuardrailLogFilter {
  api_key_id?: string;
  detector?: string;
  action?: string;
  direction?: string;
  model?: string;
  chain_id?: string;
  request_id?: string;
  /** Report period token (24h, 7d, …); bounds created_at. */
  period?: string;
}

export interface GuardrailTargetStat {
  key: string;
  requests: number;
  blocked: number;
  masked: number;
  warned: number;
  log_only: number;
}

// GuardrailStats is the monitoring payload. Request counts are per request
// (usage_records) and exclude LLM judge calls, which are reported in `judge`.
export interface GuardrailStats {
  since: string;
  buckets: number;
  totals: {
    requests: number;
    /** Requests a guardrail policy applied to. */
    evaluated: number;
    blocked: number;
    blocked_inbound: number;
    blocked_outbound: number;
    /** Streams cut after part of the answer reached the client. */
    blocked_mid_stream: number;
    masked: number;
    warned: number;
    log_only: number;
    block_rate: number;
    mask_rate: number;
    warn_rate: number;
    overhead_avg_ms: number;
    overhead_p50_ms: number;
    overhead_p95_ms: number;
  };
  judge: { calls: number; tokens: number; cost_nanos: number; avg_latency_ms: number };
  timeline: { bucket: number; requests: number; blocked: number; masked: number; warned: number; log_only: number }[];
  by_detector: { detector: string; direction: "inbound" | "outbound"; action: GuardrailAction; decisions: number; requests: number }[];
  top_reasons: { detector: string; action: GuardrailAction; reason: string; requests: number }[];
  top_entities: { entity: string; count: number }[];
  top_api_keys: GuardrailTargetStat[];
  top_models: GuardrailTargetStat[];
  data_quality: {
    audit_dropped: number;
    log_retention_days: number;
    logs_truncated: boolean;
    entities_sampled: boolean;
  };
}

// ── Audit log ───────────────────────────────────────────────────────────────

export type AuditOutcome = "success" | "failure" | "denied";

export interface AuditEntry {
  id: string;
  time: string;
  /** e.g. keys.create, auth.login, settings.endpoint */
  action: string;
  outcome: AuditOutcome;
  severity: "INFO" | "WARN" | "ERROR";
  actor_type: "dashboard" | "api_key" | "system" | "";
  actor_id: string;
  client_ip: string;
  user_agent: string;
  method: string;
  route: string;
  status_code: number;
  resource_type: string;
  resource_id: string;
  request_id: string;
  trace_id: string;
  span_id: string;
  details: Record<string, string>;
  chain_id: string;
  chain_seq: number;
  hash: string;
}

export interface AuditFilter {
  action?: string;
  outcome?: string;
  q?: string;
  cursor?: string;
  limit?: number;
}

export interface AuditStatus {
  stats: {
    enabled: boolean;
    chain_id?: string;
    queued: number;
    written: number;
    failed: number;
    dropped: number;
    otlp?: {
      endpoint: string;
      queued: number;
      exported: number;
      failed: number;
      dropped: number;
      last_error?: string;
      last_ok?: string;
    };
  };
  retention_days: number;
}

export interface AuditVerifyReport {
  ok: boolean;
  chains: number;
  entries: number;
  breaks: { chain_id: string; seq: number; entry_id: string; reason: string }[];
  verified_at: string;
  truncated?: boolean;
}

function auditQuery(f: AuditFilter = {}): string {
  const qs = new URLSearchParams();
  if (f.action) qs.set("action", f.action);
  if (f.outcome) qs.set("outcome", f.outcome);
  if (f.q) qs.set("q", f.q);
  if (f.cursor) qs.set("cursor", f.cursor);
  if (f.limit) qs.set("limit", String(f.limit));
  const s = qs.toString();
  return s ? `?${s}` : "";
}

export interface EffectiveGuardrail {
  scope: {
    tenant_id?: string;
    provider?: string;
    model?: string;
    chain_id?: string;
    apikey_id?: string;
  };
  policy: GuardrailPolicyConfig;
}

export interface UpdateInfo {
  current: string;
  latest: string;
  update_available: boolean;
  changelog: string;
  published_at: string;
  html_url: string;
  checked: boolean;
}

export interface SQLiteStatus {
  available: boolean;
  dialect: string;
  path?: string;
}

export interface SQLiteRestoreResult {
  ok: boolean;
  restart_required: boolean;
  safety_backup: string;
}

export interface ForeignImportResult {
  source: string;
  imported: number;
  skipped: number;
  accounts: number;
  custom_providers: number;
  api_keys: number;
  chains: number;
  aliases: number;
  proxy_pools: number;
  usage_records?: number;
  errors?: string[];
}
export interface N9routerImportOptions {
  usage: boolean;
  providers: boolean;
  api_keys: boolean;
  proxy_pools: boolean;
  chains: boolean;
  settings: boolean;
  password: boolean;
  mode: "merge" | "overwrite" | "wipe";
}
export type N9routerAnalyzeResult = Partial<
  Record<"providerNodes" | "providerConnections" | "apiKeys" | "combos" | "proxyPools" | "usageHistory", number>
>;

class APIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

// Default per-request timeout for admin API calls. Without an upper bound a
// stalled backend leaves the fetch promise pending forever, so React Query
// never transitions out of its loading state and the page spins indefinitely
// until a hard refresh. A bounded request rejects, surfacing an error the UI
// can render (and the user can retry).
const DEFAULT_TIMEOUT_MS = 20_000;

// fetchWithTimeout wraps fetch with an AbortController-based deadline. On
// timeout the request is aborted and a clear APIError(408) is thrown so callers
// can distinguish a stall from a network/HTTP failure.
async function fetchWithTimeout(
  input: string,
  init: RequestInit = {},
  timeoutMs = DEFAULT_TIMEOUT_MS,
): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(input, { ...init, signal: controller.signal });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      throw new APIError(408, "Request timed out. Is the backend reachable?");
    }
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

/** Returns the browser's IANA timezone (e.g. "Asia/Jakarta"), falling back to UTC. */
function browserTZ(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetchWithTimeout(`/api${path}`, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let message = res.statusText;
    try {
      const data = await res.json();
      message = data?.error?.message ?? message;
    } catch {
      // keep statusText
    }
    throw new APIError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export interface FullRestoreResult {
  ok: boolean;
  tables: number;
  rows: number;
  backup_created_at: string;
  source_dialect: string;
  restart_required: boolean;
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const data = await res.json();
    return data?.error?.message ?? res.statusText;
  } catch {
    return res.statusText;
  }
}

async function requestBlob(method: string, path: string): Promise<Blob> {
  const res = await fetchWithTimeout(`/api${path}`, { method });
  if (!res.ok) {
    let message = res.statusText;
    try {
      const data = await res.json();
      message = data?.error?.message ?? message;
    } catch {
      // keep statusText
    }
    throw new APIError(res.status, message);
  }
  return res.blob();
}

async function requestForm<T>(method: string, path: string, body: FormData): Promise<T> {
  // Uploads can legitimately take longer than a JSON call: a 9router SQLite
  // import uploads tens of MB and converts 60k+ usage rows. Use the largest
  // supported timeout and let callers with smaller payloads finish early.
  const res = await fetchWithTimeout(`/api${path}`, { method, body }, 300_000);
  if (!res.ok) {
    let message = res.statusText;
    try {
      const data = await res.json();
      message = data?.error?.message ?? message;
    } catch {
      // keep statusText
    }
    throw new APIError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export interface KeyUsageData {
  key_id: string;
  key_name: string;
  budgets: {
    period: string;
    limit_tokens: number;
    tokens_used: number;
    tokens_remaining: number;
    tokens_pct_used: number;
    limit_usd: number;
    spent_usd: number;
    usd_remaining: number;
    usd_pct_used: number;
    alert: boolean;
  }[];
  allowed_models: string[];
  models_source?: "key" | "plan" | "all";
  current_period: {
    prompt_tokens: number;
    completion_tokens: number;
    total_requests: number;
    cost_usd: number;
  };
  daily?: {
    date: string;
    requests: number;
    prompt_tokens: number;
    completion_tokens: number;
    cost_usd: number;
  }[];
  models?: {
    provider: string;
    model: string;
    total_requests: number;
    prompt_tokens: number;
    completion_tokens: number;
    cost_usd: number;
  }[];
  recent?: PortalRecentRequest[];
  days?: number;
}

export interface PortalRecentRequest {
  id: string;
  provider: string;
  model: string;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
  cache_hit: boolean;
  latency_ms: number;
  created_at: string;
  optimizations: string[];
  ttft_ms?: number;
  slim_bytes_saved?: number;
  slim_tokens_saved?: number;
  slim_rules?: string;
  headroom_tokens_saved?: number;
}

/**
 * Fetch branding settings for the public portal (no auth required)
 */
export async function fetchPortalBranding(): Promise<BrandingSettings> {
  const resp = await fetch("/v1/portal/branding");
  if (!resp.ok) {
    return { name: "KeiRouter", logo_url: "", favicon_url: "", tagline: "", color_palette: "kei", api_key_prefix: "kr_" };
  }
  return resp.json();
}

export interface PortalStatus {
  authenticated: boolean;
  email?: string;
  claimed?: boolean;
  key_id?: string;
  has_key?: boolean;
  provisioning_enabled?: boolean;
}

export interface PortalPlan {
  id: string;
  name: string;
  description?: string;
  limit_usd: number;
  limit_tokens: number;
  period: string;
  allowed_models: string[] | null;
}

export interface PortalUserRecord {
  google_sub: string;
  email: string;
  key_id: string;
  plan_id?: string;
  key_name?: string;
  display?: string;
  disabled?: boolean;
  last_used_at?: string | null;
  created_at: string;
  allowed_models?: string[];
  models_source?: "key" | "plan" | "all";
  budget?: {
    limit_micros: number;
    limit_tokens: number;
    period: string;
    hard_cutoff: boolean;
  } | null;
}

/**
 * Portal session status (cookie-authenticated).
 */
export async function fetchPortalStatus(): Promise<PortalStatus> {
  const resp = await fetch("/portal/auth/status");
  if (!resp.ok) return { authenticated: false };
  return resp.json();
}

/**
 * Claim the signed-in user's API key. Requires the full key; the server
 * verifies it and binds it to the Google identity.
 */
export async function claimPortalKey(apiKey: string): Promise<{ ok: boolean; key_id: string }> {
  const resp = await fetch("/portal/auth/claim", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ api_key: apiKey }),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to claim key");
  return data;
}

/**
 * Usage for the signed-in portal user's claimed key (cookie-authenticated).
 */
/**
 * Fetch a key's owner-portal usage payload from the admin API (session auth).
 * The public by-id portal lookup is not exposed; portal usage needs SSO.
 */
export async function fetchKeyUsageById(id: string, days?: number): Promise<KeyUsageData> {
  const qs = days ? `?days=${days}` : "";
  return request<KeyUsageData>("GET", `/keys/${encodeURIComponent(id)}/usage${qs}`);
}

export async function fetchPortalUsage(days?: number): Promise<KeyUsageData> {
  const qs = days ? `?days=${days}` : "";
  const resp = await fetch(`/portal/api/usage${qs}`);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load usage");
  return data;
}

export async function portalLogout(): Promise<void> {
  await fetch("/portal/auth/logout", { method: "POST" });
}

/**
 * Plans the portal may self-provision from (public).
 */
export async function fetchPortalPlans(): Promise<PortalPlan[]> {
  const resp = await fetch("/portal/plans");
  if (!resp.ok) return [];
  const data = await resp.json();
  return data.plans ?? [];
}

/**
 * Provision an API key for the signed-in portal user from the default plan.
 * The plaintext key is returned once.
 */
export async function createPortalKey(): Promise<{ key: string; key_id: string; plan_id: string; plan_name: string }> {
  const resp = await fetch("/portal/api/key", { method: "POST" });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to create key");
  return data;
}

export interface PortalKeyInfo {
  key_id: string;
  name: string;
  display: string;
  disabled: boolean;
  created_at: string;
  last_used_at?: string | null;
  plan_id?: string;
  plan_name?: string;
  /** True when the server stored a sealed plaintext and reveal is possible. */
  revealable?: boolean;
  /** Requests must be at least this % below the official price; 0 = off. */
  min_discount_pct?: number;
}

export interface PortalTopup {
  id: string;
  amount_usd: number;
  reason: string;
  created_at: string;
}

export interface PortalTopupData {
  topups: PortalTopup[];
  balance?: { limit_usd: number; spent_usd: number; usd_remaining: number };
}

export interface PaymentPackage {
  id: string;
  label: string;
  amount_idr: number;
}

export interface PaymentConfig {
  enabled: boolean;
  min_topup_idr?: number;
  max_topup_idr?: number;
  payment_method_type_code?: string;
  currency_source?: string;
  fx_rate?: number;
  packages?: PaymentPackage[];
}

export interface PaymentOrder {
  order_id: string;
  status: "pending" | "completed" | "manual" | "failed" | "expired";
  amount_idr: number;
  credit_usd: number;
  fx_rate: number;
  payment_link_url?: string;
  expires_at?: string;
  created_at: string;
  paid_at?: string;
}

export interface PaymentOrderAdmin extends PaymentOrder {
  /** "hosting" for Hosting 9router orders; "" or "credit" for credit top-ups. */
  product?: string;
  hosting_instance_id?: string;
  terms_version?: string;
  terms_accepted_at?: string;
  google_sub: string;
  user_email?: string;
  key_id: string;
  key_display?: string;
  provider: string;
  provider_payment_id: string;
  actor: string;
  budget_limit_usd?: number;
}

export interface PaymentSummary {
  total_idr: number;
  total_credit_usd: number;
  count_by_status: Record<string, number>;
}

export type FinanceRange = "today" | "7d" | "30d" | "all";

export interface FinanceOverview {
  /** What users were charged: upstream cost plus markup. */
  billed_usd: number;
  /** Markup earned over upstream cost (billed - cost). */
  revenue_usd: number;
  cost_exact_usd: number;
  cost_estimated_usd: number;
  profit_usd: number;
  margin_pct: number;
  requests: number;
  credit_in_idr: number;
  credit_in_usd: number;
  paid_orders: number;
  outstanding_credit_usd: number;
  in_flight: number;
}

export type UpstreamAccountStatus = "active" | "planned" | "archived";

export interface UpstreamAccount {
  id: string;
  name: string;
  url: string;
  status: UpstreamAccountStatus;
  opening_balance_usd: number;
  /** RFC3339, or "" when unset. */
  opening_at: string;
  low_balance_usd: number;
  /** RFC3339, or "" when no alert has fired since the threshold was last set. */
  alert_fired_at: string;
  providers: string[];
  created_at: string;
  updated_at: string;
}

export interface UpstreamAccountBalance extends UpstreamAccount {
  balance_usd: number;
  topups_usd: number;
  spent_usd: number;
  spent_estimated_usd: number;
  spent_range_usd: number;
  avg_daily_usd: number;
  /** null when there is no recent spend to project from. */
  days_left: number | null;
  in_flight: number;
  low: boolean;
}

export interface FinanceReport {
  range: FinanceRange;
  overview: FinanceOverview;
  accounts: UpstreamAccountBalance[];
  unmapped: { providers: string[]; spent_range_usd: number; requests: number };
}

export interface UpstreamAccountInput {
  name?: string;
  url?: string;
  status?: UpstreamAccountStatus;
  opening_balance_usd?: number;
  /** RFC3339 or YYYY-MM-DD. */
  opening_at?: string;
  low_balance_usd?: number;
  providers?: string[];
}

export interface UpstreamTopup {
  id: string;
  account_id: string;
  amount_usd: number;
  topup_at: string;
  note: string;
  actor: string;
  created_at: string;
}

export interface FinanceAlertSettings {
  enabled: boolean;
  discord_webhook_url_masked: string;
  configured: boolean;
}

export interface FinanceProviderOption {
  id: string;
  label: string;
}

export interface UsageEconomics {
  billed_usd: number;
  upstream_usd: number;
  profit_usd: number;
  margin_pct: number;
  requests: number;
  priced_requests: number;
  unpriced_usd: number;
}

/** Masked metadata for the signed-in portal user's key. */
export async function fetchPortalKey(): Promise<PortalKeyInfo> {
  const resp = await fetch("/portal/api/key");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load key");
  return data;
}

/**
 * Reveal the full plaintext of a portal-provisioned key. Only works for keys
 * the portal created; manually claimed keys return an error.
 */
export async function revealPortalKey(): Promise<string> {
  const resp = await fetch("/portal/api/key/reveal");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to reveal key");
  return data.key;
}

/** Set the signed-in portal user's discount floor (0 turns it off). */
export async function setPortalDiscountFloor(minDiscountPct: number): Promise<{ min_discount_pct: number }> {
  const resp = await fetch("/portal/api/key/discount-floor", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ min_discount_pct: minDiscountPct }),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to save minimum discount");
  return data;
}

/** One model row on the portal's Discount Settings page. */
export interface PortalDiscountModel {
  model: string;
  /** False when no step of the chain can guarantee a discount. */
  supported: boolean;
  /** The model's own minimum; null follows the key default. */
  override_pct: number | null;
  /** What requests for this model must meet. */
  effective_pct: number;
}

export interface PortalDiscounts {
  default_pct: number;
  max_pct: number;
  models: PortalDiscountModel[];
}

/** The key's default minimum discount and its per-model overrides. */
export async function fetchPortalDiscounts(): Promise<PortalDiscounts> {
  const resp = await fetch("/portal/api/discounts");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load discount settings");
  return data;
}

/** Set one model's minimum discount, overriding the key default. */
export async function setPortalModelDiscount(model: string, minDiscountPct: number): Promise<void> {
  const resp = await fetch(`/portal/api/discounts/models/${encodeURIComponent(model)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ min_discount_pct: minDiscountPct }),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to save minimum discount");
}

/** Remove a model's override so it follows the key default again. */
export async function resetPortalModelDiscount(model: string): Promise<void> {
  const resp = await fetch(`/portal/api/discounts/models/${encodeURIComponent(model)}`, { method: "DELETE" });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to reset minimum discount");
}

/** Read-only topup ledger + balance for the signed-in portal user. */
export async function fetchPortalTopups(): Promise<PortalTopupData> {
  const resp = await fetch("/portal/api/topups");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load topups");
  return data;
}

/** Non-secret payment options the portal top-up page renders. */
export async function fetchPortalPaymentConfig(): Promise<PaymentConfig> {
  const resp = await fetch("/portal/payment/config");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load payment config");
  return data;
}

/** Create a SumoPod payment for the signed-in user's key. */
export async function createTopupOrder(input: {
  amount_idr?: number;
  package_id?: string;
  idempotency_key?: string;
  turnstile_token?: string;
}): Promise<PaymentOrder> {
  const resp = await fetch("/portal/api/topup/orders", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to create payment");
  return data;
}

/** List the signed-in user's payment orders. */
export async function fetchPortalTopupOrders(): Promise<{ orders: PaymentOrder[] }> {
  const resp = await fetch("/portal/api/topup/orders");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load orders");
  return data;
}

/** Load a single payment order owned by the signed-in user. */
export async function fetchPortalTopupOrder(id: string): Promise<PaymentOrder> {
  const resp = await fetch(`/portal/api/topup/orders/${id}`);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error?.message || data.error || "Failed to load order");
  return data;
}

// ─── Hosting 9router ────────────────────────────────────────────────────────

export type HostingStatus =
  | "awaiting_payment"
  | "paid"
  | "provisioning"
  | "active"
  | "failed"
  | "expired"
  | "stopped"
  | "deleted";

export interface PortalHostingInstance {
  id: string;
  status: HostingStatus;
  /** "" until the instance is reachable. */
  url: string;
  /** RFC3339, or "" before the first activation. */
  paid_until: string;
  /** Days paid for but not yet started (counted from activation). */
  prepaid_days: number;
  created_at: string;
  pending_payment_link?: string;
  pending_order_id?: string;
  /** True once the instance has its own initial dashboard password. */
  has_password: boolean;
}

export interface PortalHostingConfig {
  enabled: boolean;
  price_idr: number;
  period_days: number;
  terms_version: string;
  terms_url: string;
  /** Where paid customers waiting for activation contact the admin. */
  support_url: string;
  instances: PortalHostingInstance[];
}

export interface HostingOrder {
  order_id: string;
  instance_id: string;
  status: PaymentOrder["status"];
  amount_idr: number;
  payment_link_url: string;
  expires_at?: string;
  terms_version: string;
  created_at: string;
  coupon_code?: string;
  /** Price before the coupon; 0 when no coupon was used. */
  base_amount_idr?: number;
  bonus_days?: number;
}

export type HostingCouponKind = "discount" | "bonus_days";
export type HostingCouponDiscountType = "percent" | "fixed";

/** Result of checking a coupon before checkout. */
export type HostingCouponCheck =
  | {
      valid: true;
      coupon_code: string;
      kind: HostingCouponKind;
      discount_type: HostingCouponDiscountType | "";
      discount_value: number;
      base_idr: number;
      discount_idr: number;
      final_idr: number;
      bonus_days: number;
      period_days: number;
      expires_at: string;
    }
  | { valid: false; code: string; message: string };

/** Error from a portal call that keeps the server's machine-readable code. */
export class PortalAPIError extends Error {
  status: number;
  code?: string;
  constructor(status: number, message: string, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

/** Hosting offer plus the signed-in user's instances. */
export async function fetchPortalHosting(): Promise<PortalHostingConfig> {
  const resp = await fetch("/portal/api/hosting");
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new PortalAPIError(resp.status, data.error?.message || data.error || "Failed to load hosting", data.error?.code);
  return data;
}

/** Reveal the initial 9router dashboard password of the caller's instance. */
export async function fetchPortalHostingPassword(id: string): Promise<string> {
  const resp = await fetch(`/portal/api/hosting/instances/${encodeURIComponent(id)}/password`, { cache: "no-store" });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new PortalAPIError(resp.status, data.error?.message || data.error || "Failed to load password", data.error?.code);
  return data.password as string;
}

/** Check a hosting coupon for the signed-in user. It does not use the coupon. */
export async function checkPortalHostingCoupon(code: string): Promise<HostingCouponCheck> {
  const resp = await fetch("/portal/api/hosting/coupons/check", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new PortalAPIError(resp.status, data.error?.message || data.error || "Failed to check coupon", data.error?.code);
  return data;
}

/** Create a SumoPod payment for a new hosting instance, or a renewal when instance_id is set. */
export async function createPortalHostingOrder(input: {
  coupon_code?: string;
  instance_id?: string;
  accept_terms: boolean;
  terms_version: string;
  idempotency_key: string;
  turnstile_token?: string;
}): Promise<HostingOrder> {
  const resp = await fetch("/portal/api/hosting/orders", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new PortalAPIError(resp.status, data.error?.message || data.error || "Failed to create payment", data.error?.code);
  return data;
}

/** A Dokploy server that runs customer containers, with its own wildcard domain. */
export interface HostingNode {
  /** ^[a-z0-9][a-z0-9-]{0,39}$, unique, never changed once saved. */
  id: string;
  name: string;
  /** Dokploy serverId; "" = the Dokploy host itself. */
  server_id: string;
  /** Wildcard *.root_domain must point at this node's IP. */
  root_domain: string;
  /** Monitoring target id; "" falls back to the target flagged instances. */
  monitor_target_id: string;
  /** 0 = unlimited. */
  capacity: number;
  enabled: boolean;
}

export interface HostingNodeLoad extends HostingNode {
  used: number;
  /** Open slots, or -1 when unlimited. */
  free: number;
  /** Enabled and not full. */
  available: boolean;
}

export interface HostingSettings {
  enabled: boolean;
  monthly_price_idr: number;
  dokploy_url: string;
  api_key_masked: string;
  has_api_key: boolean;
  environment_id: string;
  images: string[];
  container_port: number;
  env_template: string;
  nodes: HostingNode[];
  password_env: string;
  memory_limit: string;
  cpu_limit: string;
  data_path: string;
  support_url: string;
  grace_days: number;
  terms_version: string;
  period_days: number;
}

export interface HostingSettingsInput {
  enabled: boolean;
  monthly_price_idr: number;
  dokploy_url: string;
  /** Omit or leave empty to keep the stored key. */
  api_key?: string;
  clear_api_key?: boolean;
  environment_id: string;
  images: string[];
  container_port: number;
  env_template: string;
  nodes: HostingNode[];
  password_env: string;
  memory_limit: string;
  cpu_limit: string;
  data_path: string;
  support_url: string;
}

export interface HostingInstanceOrder {
  order_id: string;
  status: PaymentOrder["status"];
  amount_idr: number;
  created_at: string;
  paid_at?: string;
  terms_version: string;
  terms_accepted_at: string;
  coupon_code?: string;
  base_amount_idr?: number;
  bonus_days?: number;
  reason?: string;
}

/** Admin view of a hosting coupon. */
export interface HostingCoupon {
  id: string;
  code: string;
  kind: HostingCouponKind;
  discount_type: HostingCouponDiscountType | "";
  discount_value: number;
  bonus_days: number;
  /** 0 = unlimited. */
  max_uses: number;
  /** Paid uses. */
  used_count: number;
  /** Uses held by payments still pending. */
  held_count: number;
  /** -1 = unlimited. */
  remaining: number;
  starts_at: string;
  expires_at: string;
  active: boolean;
  state: "active" | "inactive" | "scheduled" | "expired" | "exhausted";
  note: string;
  created_at: string;
  updated_at: string;
}

export interface HostingCouponInput {
  code: string;
  kind: HostingCouponKind;
  discount_type?: HostingCouponDiscountType;
  discount_value?: number;
  bonus_days?: number;
  max_uses: number;
  /** RFC3339, or "" for none. */
  starts_at: string;
  expires_at: string;
  active: boolean;
  note: string;
}

export interface HostingInstanceAdmin {
  id: string;
  key_id: string;
  status: HostingStatus;
  image: string;
  subdomain: string;
  fqdn: string;
  url: string;
  dokploy_app_id: string;
  dokploy_app_name: string;
  dokploy_domain_id: string;
  data_volume: string;
  last_error: string;
  paid_until: string;
  prepaid_days: number;
  created_at: string;
  updated_at: string;
  provisioned_at: string;
  has_password: boolean;
  /** Node the instance runs on; "" before it is provisioned. */
  node_id: string;
  user_email?: string;
  orders?: HostingInstanceOrder[];
}

// Hosting finance (admin): revenue vs operating cost, bucketed per WIB month.
export interface HostingFinanceMonth {
  /** "2026-10" */
  month: string;
  revenue_idr: number;
  cost_idr: number;
  profit_idr: number;
  orders: number;
  refund_idr: number;
}

export type HostingCostCategory = "vps" | "domain" | "software" | "other";
export type HostingCostPeriod = "monthly" | "one_time";

export interface HostingCost {
  id: string;
  name: string;
  category: HostingCostCategory;
  amount_idr: number;
  period: HostingCostPeriod;
  /** YYYY-MM-DD */
  start_date: string;
  /** "" or YYYY-MM-DD */
  end_date: string;
  note: string;
  created_at: string;
  updated_at: string;
  this_month_idr: number;
}

export interface HostingCostInput {
  name: string;
  category: HostingCostCategory;
  amount_idr: number;
  period: HostingCostPeriod;
  start_date: string;
  end_date?: string;
  note?: string;
}

export interface HostingFinance {
  months: HostingFinanceMonth[];
  current: {
    month: string;
    revenue_idr: number;
    cost_idr: number;
    profit_idr: number;
    running_instances: number;
    mrr_idr: number;
    price_idr: number;
    cost_per_instance_idr: number;
    break_even_instances: number;
  };
  totals: { revenue_idr: number; cost_idr: number; profit_idr: number };
  status_counts: Record<string, number>;
  refunds_needed: { order_id: string; amount_idr: number; paid_at: string; instance_id: string }[] | null;
  costs: HostingCost[];
  note: string;
}

// Hosting monitoring (admin): Dokploy monitoring targets and their metrics.
export interface HostingMonitorTarget {
  id: string;
  name: string;
  url: string;
  instances: boolean;
  has_token: boolean;
  token_masked: string;
}

export interface HostingMonitorTargetInput {
  /** Omit for a new target. */
  id?: string;
  name: string;
  url: string;
  /** Omit or "" keeps the stored token; required again when the URL changes. */
  token?: string;
  instances: boolean;
}

export interface HostingServerMetric {
  timestamp: string;
  cpu: number;
  cpuModel: string;
  cpuCores: number;
  os: string;
  distro: string;
  /** Percent. */
  memUsed: number;
  memUsedGB: number;
  /** GB. */
  memTotal: number;
  /** Seconds. */
  uptime: number;
  /** Percent. */
  diskUsed: number;
  /** GB. */
  totalDisk: number;
  /** MB, cumulative. */
  networkIn: number;
  networkOut: number;
}

export interface HostingServerMetrics {
  id: string;
  name: string;
  instances: boolean;
  samples: HostingServerMetric[];
  error?: string;
}

export interface HostingContainerMetric {
  timestamp: string;
  CPU: number;
  Memory: { percentage: number; used: number; total: number; usedUnit: string; totalUnit: string };
  Network: { input: number; output: number; inputUnit: string; outputUnit: string };
  BlockIO: { read: number; write: number; readUnit: string; writeUnit: string };
  Name: string;
}

export interface HostingContainerMetrics {
  instance_id: string;
  app_name: string;
  fqdn: string;
  status: HostingStatus;
  node_id: string;
  /** Monitoring target name the metrics came from. */
  server?: string;
  samples: HostingContainerMetric[];
  error?: string;
}

export const api = {
  // Auth (no session required for status/login/logout).
  authStatus: () => request<AuthStatus>("GET", "/auth/status"),
  login: (password: string, turnstileToken?: string) =>
    request<{ ok: boolean; using_default: boolean; onboarding_complete: boolean }>(
      "POST",
      "/auth/login",
      { password, ...(turnstileToken ? { turnstile_token: turnstileToken } : {}) },
    ),
  logout: () => request<{ ok: boolean }>("POST", "/auth/logout"),
  changePassword: (newPassword: string) =>
    request<{ ok: boolean }>("POST", "/auth/password", { new_password: newPassword }),
  completeOnboarding: () => request<{ ok: boolean }>("POST", "/auth/onboarding/complete"),

  providers: () => request<{ providers: Provider[] }>("GET", "/providers"),
  // Every listed provider's models in one request. `pending` names providers
  // whose live discovery was still running; fetch those with providerModels().
  allProviderModels: () =>
    request<{ providers: Record<string, ProviderModel[]>; pending: string[] }>("GET", "/providers/models"),
  providerModels: (id: string, kind?: string) =>
    request<{ models: ProviderModel[] }>(
      "GET",
      `/providers/${id}/models${kind ? `?kind=${encodeURIComponent(kind)}` : ""}`,
    ),
  providerRouting: (id: string) =>
    request<ProviderRoutingSettings>("GET", `/providers/${id}/routing`),
  updateProviderRouting: (id: string, patch: Partial<ProviderRoutingSettings>) =>
    request<ProviderRoutingSettings>("POST", `/providers/${id}/routing`, patch),

  // Custom provider instances (dynamic OpenAI-/Anthropic-compatible providers).
  listCustomProviders: () =>
    request<{ providers: CustomProvider[] }>("GET", "/custom-providers"),
  createCustomProvider: (input: { display_name: string; dialect: string; base_url: string; alias?: string }) =>
    request<CustomProvider>("POST", "/custom-providers", input),
  updateCustomProvider: (id: string, patch: { display_name?: string; alias?: string; base_url?: string }) =>
    request<CustomProvider>("PATCH", `/custom-providers/${id}`, patch),
  deleteCustomProvider: (id: string) =>
    request<{ id: string; deleted: boolean; accounts_disabled?: number }>("DELETE", `/custom-providers/${id}`),

  // Display provider categories (public model catalog labels).
  listProviderCategories: () =>
    request<{ categories: { id: string; label: string }[] }>("GET", "/provider-categories"),
  createProviderCategory: (input: { id?: string; label: string }) =>
    request<{ id: string; label: string }>("POST", "/provider-categories", input),
  deleteProviderCategory: (id: string) =>
    request<{ id: string; deleted: boolean }>("DELETE", `/provider-categories/${id}`),

  importModels: (id: string) =>
    request<{ provider_id: string; imported: number; skipped: number; total: number }>(
      "POST",
      `/providers/${id}/import-models`,
    ),

  // Custom models, attachable to any provider id (custom or built-in).
  listCustomModels: (providerId: string) =>
    request<{ models: CustomModel[] }>("GET", `/providers/${providerId}/custom-models`),
  createCustomModel: (providerId: string, input: CustomModelInput) =>
    request<CustomModel>("POST", `/providers/${providerId}/custom-models`, input),
  updateCustomModel: (providerId: string, dbId: string, patch: Partial<CustomModelInput>) =>
    request<CustomModel>("PATCH", `/providers/${providerId}/custom-models/${dbId}`, patch),
  deleteCustomModel: (providerId: string, dbId: string) =>
    request<{ db_id: string; deleted: boolean }>("DELETE", `/providers/${providerId}/custom-models/${dbId}`),


  listPlans: () => request<{ plans: Plan[] }>("GET", "/plans"),
  createPlan: (input: {
    name: string;
    description?: string;
    limit_usd?: number;
    limit_tokens?: number;
    rpm_limit?: number;
    tpm_limit?: number;
    concurrency_limit?: number;
    period?: string;
    alert_pct?: number;
    hard_cutoff?: boolean;
    allowed_models?: string[];
  }) => request<Plan>("POST", "/plans", input),
  updatePlan: (id: string, patch: {
    name?: string;
    description?: string;
    limit_usd?: number;
    limit_tokens?: number;
    rpm_limit?: number;
    tpm_limit?: number;
    concurrency_limit?: number;
    period?: string;
    alert_pct?: number;
    hard_cutoff?: boolean;
    allowed_models?: string[];
  }) => request<Plan>("PATCH", `/plans/${id}`, patch),
  deletePlan: (id: string) => request<void>("DELETE", `/plans/${id}`),
  listPlanKeys: (id: string) => request<{ keys: APIKey[] }>("GET", `/plans/${id}/keys`),

  // Portal users (admin): self-provisioned Google SSO accounts.
  listPortalUsers: () =>
    request<{ users: PortalUserRecord[]; default_plan_id: string }>("GET", "/portal-users"),
  deletePortalUser: (sub: string) => request<void>("DELETE", `/portal-users/${encodeURIComponent(sub)}`),
  setPortalUserPlan: (sub: string, planId: string) =>
    request<{ google_sub: string; plan_id: string }>("PATCH", `/portal-users/${encodeURIComponent(sub)}/plan`, { plan_id: planId }),
  togglePortalUserKey: (sub: string, disabled: boolean) =>
    request<{ disabled: boolean }>("POST", `/portal-users/${encodeURIComponent(sub)}/key/toggle`, { disabled }),
  rotatePortalUserKey: (sub: string) =>
    request<{ key: string; key_id: string; display: string }>("POST", `/portal-users/${encodeURIComponent(sub)}/key/rotate`),
  getPortalSettings: () => request<{ default_plan_id: string }>("GET", "/portal-settings"),
  updatePortalSettings: (defaultPlanId: string) =>
    request<{ default_plan_id: string }>("POST", "/portal-settings", { default_plan_id: defaultPlanId }),

  // Payment orders (admin): revenue summary and manual credit approval.
  paymentOrders: () => request<{ orders: PaymentOrderAdmin[] }>("GET", "/payments/orders"),
  paymentSummary: () => request<PaymentSummary>("GET", "/payments/summary"),
  paymentEconomics: () => request<UsageEconomics>("GET", "/payments/economics"),
  approvePaymentOrder: (id: string, reason: string) =>
    request<PaymentOrderAdmin>("POST", `/payments/orders/${id}/approve`, { reason }),

  // Hosting 9router (admin): Dokploy settings and instance lifecycle.
  getHostingSettings: () => request<HostingSettings>("GET", "/hosting/settings"),
  putHostingSettings: (input: HostingSettingsInput) => request<HostingSettings>("PUT", "/hosting/settings", input),
  testHostingSettings: () => request<{ ok: boolean; error?: string }>("POST", "/hosting/settings/test"),
  listHostingInstances: (status?: string) =>
    request<{ instances: HostingInstanceAdmin[] }>("GET", `/hosting/instances${status ? `?status=${encodeURIComponent(status)}` : ""}`),
  listHostingNodes: () => request<{ nodes: HostingNodeLoad[]; recommended: string }>("GET", "/hosting/nodes"),
  /** Omit nodeId to let the server pick the node with the most room. */
  provisionHosting: (id: string, image: string, nodeId?: string) =>
    request<HostingInstanceAdmin>("POST", `/hosting/instances/${encodeURIComponent(id)}/provision`, {
      image,
      ...(nodeId ? { node_id: nodeId } : {}),
    }),
  stopHosting: (id: string) => request<HostingInstanceAdmin>("POST", `/hosting/instances/${encodeURIComponent(id)}/stop`),
  startHosting: (id: string) => request<HostingInstanceAdmin>("POST", `/hosting/instances/${encodeURIComponent(id)}/start`),
  deleteHosting: (id: string) => request<HostingInstanceAdmin>("POST", `/hosting/instances/${encodeURIComponent(id)}/delete`),
  listHostingCoupons: () => request<{ coupons: HostingCoupon[] }>("GET", "/hosting/coupons"),
  createHostingCoupon: (body: HostingCouponInput) => request<HostingCoupon>("POST", "/hosting/coupons", body),
  updateHostingCoupon: (id: string, body: HostingCouponInput) =>
    request<HostingCoupon>("PUT", `/hosting/coupons/${encodeURIComponent(id)}`, body),
  deleteHostingCoupon: (id: string) => request<{ deleted: boolean }>("DELETE", `/hosting/coupons/${encodeURIComponent(id)}`),
  hostingFinance: (months: number) => request<HostingFinance>("GET", `/hosting/finance?months=${months}`),
  createHostingCost: (body: HostingCostInput) => request<HostingCost>("POST", "/hosting/costs", body),
  updateHostingCost: (id: string, body: HostingCostInput) => request<HostingCost>("PUT", `/hosting/costs/${encodeURIComponent(id)}`, body),
  deleteHostingCost: (id: string) => request<void>("DELETE", `/hosting/costs/${encodeURIComponent(id)}`),
  getHostingMonitoring: () => request<{ targets: HostingMonitorTarget[] }>("GET", "/hosting/monitoring/settings"),
  putHostingMonitoring: (targets: HostingMonitorTargetInput[]) =>
    request<{ targets: HostingMonitorTarget[] }>("PUT", "/hosting/monitoring/settings", { targets }),
  hostingServerMetrics: (limit = 60) => request<{ servers: HostingServerMetrics[] }>("GET", `/hosting/monitoring/servers?limit=${limit}`),
  hostingContainerMetrics: (limit = 30) =>
    request<{ containers: HostingContainerMetrics[]; error?: string }>("GET", `/hosting/monitoring/containers?limit=${limit}`),

  // Finance monitor (admin): profit, upstream balances, top-ups, alerts.
  financeReport: (range: FinanceRange) =>
    request<FinanceReport>("GET", `/payments/finance?range=${encodeURIComponent(range)}`),
  financeProviders: () => request<{ providers: FinanceProviderOption[] }>("GET", "/payments/providers"),
  upstreamAccounts: () => request<{ accounts: UpstreamAccount[] }>("GET", "/payments/upstream-accounts"),
  createUpstreamAccount: (body: UpstreamAccountInput) =>
    request<UpstreamAccount>("POST", "/payments/upstream-accounts", body),
  updateUpstreamAccount: (id: string, body: UpstreamAccountInput) =>
    request<UpstreamAccount>("PATCH", `/payments/upstream-accounts/${encodeURIComponent(id)}`, body),
  upstreamTopups: (accountId: string) =>
    request<{ topups: UpstreamTopup[] }>("GET", `/payments/upstream-accounts/${encodeURIComponent(accountId)}/topups`),
  createUpstreamTopup: (accountId: string, body: { amount_usd: number; topup_at?: string; note?: string }) =>
    request<UpstreamTopup>("POST", `/payments/upstream-accounts/${encodeURIComponent(accountId)}/topups`, body),
  deleteUpstreamTopup: (id: string) =>
    request<{ ok: boolean }>("DELETE", `/payments/upstream-topups/${encodeURIComponent(id)}`),
  financeAlerts: () => request<FinanceAlertSettings>("GET", "/payments/alerts"),
  /** `enabled` is always sent: the backend reads a missing field as false. */
  updateFinanceAlerts: (body: { enabled: boolean; discord_webhook_url?: string }) =>
    request<FinanceAlertSettings>("PUT", "/payments/alerts", body),
  testFinanceAlerts: () => request<{ ok: boolean }>("POST", "/payments/alerts/test"),

  listKeys: () => request<{ keys: APIKey[] }>("GET", "/keys"),
  createKey: (name: string, opts?: {
    plan_id?: string;
    budget_limit_usd?: number;
    budget_limit_tokens?: number;
    budget_period?: string;
    budget_alert_pct?: number;
    budget_hard_cutoff?: boolean;
    allowed_models?: string[];
  }) =>
    request<CreatedKey>("POST", "/keys", { name, ...(opts ? opts : {}) }),
  updateKey: (id: string, patch: { disabled?: boolean; allowed_models?: string[] }) =>
    request<{ id: string; disabled?: boolean; allowed_models?: string[] }>("PATCH", `/keys/${id}`, patch),
  deleteKey: (id: string) => request<void>("DELETE", `/keys/${id}`),
  deleteKeys: (ids: string[]) => Promise.all(ids.map((id) => request<void>("DELETE", `/keys/${id}`))),

  getBansos: () => request<Bansos>("GET", "/bansos"),
  createBansos: (input: {
    mode: "credit" | "unlimited";
    allowed_models: string[];
    rpm?: number;
    tpm?: number;
    credit_limit_usd?: number;
  }) => request<Bansos>("POST", "/bansos", input),
  updateBansos: (patch: {
    active?: boolean;
    mode?: "credit" | "unlimited";
    allowed_models?: string[];
    rpm?: number;
    tpm?: number;
    credit_limit_usd?: number;
    notice_enabled?: boolean;
    notice_text?: string;
    notice_rate?: number;
  }) => request<Bansos>("PATCH", "/bansos", patch),
  topupBansos: (input: { amount_usd: number; reason?: string; idempotency_key: string }) =>
    request<{ topup: KeyTopup }>("POST", "/bansos/topup", input),
  rotateBansos: () =>
    request<{ key_id: string; key: string; masked_display: string }>("POST", "/bansos/rotate"),

  listAccounts: () => request<{ accounts: Account[] }>("GET", "/accounts"),
  createAccount: (input: AccountInput) =>
    request<{ id: string }>("POST", "/accounts", input),
  bulkCreateAccounts: (input: BulkAccountInput) =>
    request<BulkAccountResponse>("POST", "/accounts/bulk", input),
  updateAccount: (id: string, patch: { label?: string; priority?: number; disabled?: boolean; proxy_pool_id?: string }) =>
    request<{ id: string }>("PATCH", `/accounts/${id}`, patch),
  deleteAccount: (id: string) => request<void>("DELETE", `/accounts/${id}`),
  testAccount: (id: string) =>
    request<{ id: string; status: string; message: string }>("POST", `/accounts/${id}/test`),
  validateKey: (input: AccountInput) =>
    request<{ status: string; message?: string }>("POST", "/validate-key", input),
  accountQuota: (id: string) =>
    request<{ provider: string; supported: boolean; plan_name?: string; message?: string; quotas?: UpstreamQuota[] }>(
      "GET", `/accounts/${id}/quota`,
    ),
  codexResetCredits: (id: string) =>
    request<CodexResetCredits>("GET", `/accounts/${id}/codex-reset-credits`),
  codexConsumeCredit: (id: string, creditId?: string) =>
    request<CodexConsumeResult>("POST", `/accounts/${id}/codex-consume-credit`, creditId ? { credit_id: creditId } : {}),
  codexUsageDetails: (id: string) =>
    request<CodexUsageDetails>("GET", `/accounts/${id}/codex-usage-details`),

  listChains: () => request<{ chains: Chain[] }>("GET", "/chains"),
  createChain: (input: { name: string; strategy?: string; display_provider?: string; fallback_provider?: string; fallback_model?: string; input_per_m?: number; output_per_m?: number; cache_write_per_m?: number; cache_read_per_m?: number; market_slugs?: string[]; reorder_by_market?: boolean; markup_percent?: number | null; official_input_per_m?: number | null; official_output_per_m?: number | null; steps: { provider: string; model: string; market_slug?: string }[] }) =>
    request<{ id: string }>("POST", "/chains", input),
  updateChain: (id: string, patch: { name?: string; strategy?: string; display_provider?: string; fallback_provider?: string; fallback_model?: string; input_per_m?: number; output_per_m?: number; cache_write_per_m?: number; cache_read_per_m?: number; market_slugs?: string[]; reorder_by_market?: boolean; markup_percent?: number | null; official_input_per_m?: number | null; official_output_per_m?: number | null; steps?: { provider: string; model: string; market_slug?: string }[] }) =>
    request<{ id: string }>("PATCH", `/chains/${id}`, patch),
  deleteChain: (id: string) => request<void>("DELETE", `/chains/${id}`),

  listBudgets: () => request<{ budgets: Budget[] }>("GET", "/budgets"),
  budgetStatus: () => request<{ budgets: BudgetStatus[] }>("GET", "/budgets/status"),
  createBudget: (input: { scope_kind?: string; scope_id?: string; limit_usd?: number; limit_tokens?: number; period?: string; alert_pct?: number; hard_cutoff?: boolean }) =>
    request<{ id: string }>("POST", "/budgets", input),
  updateBudget: (id: string, patch: { limit_usd?: number; limit_tokens?: number; period?: string; alert_pct?: number; hard_cutoff?: boolean }) =>
    request<void>("PATCH", `/budgets/${id}`, patch),
  deleteBudget: (id: string) => request<void>("DELETE", `/budgets/${id}`),

  listKeyTopups: (keyId: string) =>
    request<{ topups: KeyTopup[] }>("GET", `/keys/${keyId}/topups`),
  topupKey: (keyId: string, input: { amount_usd: number; reason?: string; idempotency_key: string }) =>
    request<{ topup: KeyTopup }>("POST", `/keys/${keyId}/topup`, input),
  listKeyLimitAdjustments: (keyId: string) =>
    request<{ adjustments: KeyLimitAdjustment[] }>("GET", `/keys/${keyId}/limit-adjustments`),
  adjustKeyLimit: (keyId: string, input: { limit_usd: number; reason: string; idempotency_key: string }) =>
    request<{ adjustment: KeyLimitAdjustment }>("POST", `/keys/${keyId}/limit`, input),

  usage: (period: string) => request<UsageSummary>("GET", `/usage?period=${period}&tz=${browserTZ()}`),
  usageInsights: (period: string, opts: { limit?: number; buckets?: number } = {}) => {
    const qs = new URLSearchParams({ period, tz: browserTZ() });
    if (opts.limit) qs.set("limit", String(opts.limit));
    if (opts.buckets) qs.set("buckets", String(opts.buckets));
    return request<UsageInsights>("GET", `/usage/insights?${qs.toString()}`);
  },
  chainUsage: (period: string) =>
    request<{ period: string; since: string; chains: ChainUsage[] }>("GET", `/usage/chains?period=${period}&tz=${browserTZ()}`),
  modelUsage: (period: string) =>
    request<ModelUsageResponse>("GET", `/usage/models?period=${period}&tz=${browserTZ()}`),
  keyModelUsage: (keyId: string, period: string) =>
    request<{ period: string; since: string; models: KeyModelUsage[] }>("GET", `/keys/${keyId}/models?period=${period}&tz=${browserTZ()}`),
  usageLeaderboard: (period: string) =>
    request<UsageLeaderboard>("GET", `/usage/leaderboard?period=${period}&tz=${browserTZ()}`),

  quota: (period: string) =>
    request<{ accounts: QuotaAccount[]; since: string }>("GET", `/quota?period=${period}&tz=${browserTZ()}`),
  quotaByProvider: (provider: string) =>
    request<{ accounts: QuotaAccount[]; since: string }>("GET", `/quota?period=today&tz=${browserTZ()}&provider=${provider}`),

  consoleLog: () => request<{ epoch: string; logs: ConsoleLogEntry[] }>("GET", "/console"),

  cliTools: (model?: string) =>
    request<CLIToolsResponse>("GET", model ? `/cli-tools?model=${encodeURIComponent(model)}` : "/cli-tools"),
  cliTool: (toolId: string) => request<CLITool>("GET", `/cli-tools/${encodeURIComponent(toolId)}`),
  /** Omit api_key to keep the key already stored in the tool's config. */
  cliToolConfigure: (toolId: string, body: { base_url: string; api_key?: string; model?: string }) =>
    request<{ ok: boolean; tool: CLITool }>("POST", `/cli-tools/${encodeURIComponent(toolId)}/configure`, body),
  cliToolRemove: (toolId: string) =>
    request<{ ok: boolean; tool: CLITool }>("POST", `/cli-tools/${encodeURIComponent(toolId)}/remove`),

  listProxyPools: () => request<{ pools: ProxyPool[] }>("GET", "/proxy-pools"),
  createProxyPool: (input: { name: string; type?: string; proxy_url: string; no_proxy?: string; strict?: boolean; is_active?: boolean }) =>
    request<{ id: string }>("POST", "/proxy-pools", input),
  deployCloudflareRelay: (input: { account_id: string; api_token: string; project_name?: string }) =>
    request<{ id: string; name: string; deploy_url: string; test_status: string }>("POST", "/proxy-pools/cloudflare-deploy", input),
  updateProxyPool: (id: string, patch: { name?: string; proxy_url?: string; no_proxy?: string; strict?: boolean; is_active?: boolean }) =>
    request<void>("PATCH", `/proxy-pools/${id}`, patch),
  deleteProxyPool: (id: string) => request<void>("DELETE", `/proxy-pools/${id}`),

  listSkills: () => request<{ skills: Skill[] }>("GET", "/skills"),
  createSkill: (input: SkillInput & { name: string }) =>
    request<Skill>("POST", "/skills", input),
  updateSkill: (id: string, patch: SkillInput) =>
    request<Skill>("POST", `/skills/${id}`, patch),
  deleteSkill: (id: string) => request<void>("DELETE", `/skills/${id}`),

  endpointSettings: () => request<EndpointSettings>("GET", "/settings/endpoint"),
  updateEndpointSettings: (patch: Partial<EndpointSettings>) =>
    request<EndpointSettings>("POST", "/settings/endpoint", patch),
  currencySettings: () => request<CurrencyStatus>("GET", "/settings/currency"),
  updateCurrencySettings: (patch: Partial<Pick<CurrencyConfig, "auto_refresh_enabled" | "refresh_interval_h" | "override_enabled" | "override_rate" | "source_url">>) =>
    request<CurrencyStatus>("POST", "/settings/currency", patch),
  refreshCurrency: () => request<CurrencyStatus>("POST", "/settings/currency/refresh", {}),

  marketPricingSettings: () => request<MarketPricingSettings>("GET", "/market-pricing/settings"),
  updateMarketPricingSettings: (patch: Partial<Pick<MarketPricingSettings, "auto_refresh" | "refresh_interval_seconds" | "markup_percent" | "safety_margin_enabled" | "safety_margin_percent" | "surplus_fallback_depth">>) =>
    request<MarketPricingSettings>("PATCH", "/market-pricing/settings", patch),
  refreshMarketPrices: () => request<{ synced: number; last_fetched_at: string }>("POST", "/market-pricing/refresh", {}),
  publicPricing: () => request<PublicPricingSettings>("GET", "/public-pricing"),
  updatePublicPricing: (body: { enabled: boolean; chain_ids: string[] }) =>
    request<PublicPricingSettings>("PUT", "/public-pricing", body),

  testHeadroom: (body?: { url?: string; timeout_ms?: number }) =>
    request<HeadroomTestResult>("POST", "/settings/headroom-test", body ?? {}),

  accessSettings: () => request<AccessSettings>("GET", "/settings/access"),
  updateAccessSettings: (patch: Partial<Omit<AccessSettings, "endpoint_url">>) =>
    request<AccessSettings>("POST", "/settings/access", patch),

  // Branding / white-label settings.
  branding: () => request<BrandingSettings>("GET", "/settings/branding"),
  updateBranding: (patch: Partial<BrandingSettings>) =>
    request<BrandingSettings>("POST", "/settings/branding", patch),

  // Landing page notifications.
  notifications: () => request<{ notifications: LandingNotification[] }>("GET", "/settings/notifications"),
  updateNotifications: (notifications: LandingNotification[]) =>
    request<{ notifications: LandingNotification[] }>("POST", "/settings/notifications", { notifications }),

  // Tunnel management.
  tunnelStatus: () => request<TunnelCombinedStatus>("GET", "/tunnel/status"),
  tunnelEnable: () => request<TunnelEnableResult>("POST", "/tunnel/enable"),
  tunnelDisable: () => request<{ success: boolean }>("POST", "/tunnel/disable"),
  tailscaleCheck: () => request<TailscaleCheckResult>("GET", "/tunnel/tailscale-check"),
  tailscaleEnable: (sudoPassword?: string) =>
    request<TailscaleEnableResult>("POST", "/tunnel/tailscale-enable", sudoPassword ? { sudoPassword } : {}),
  tailscaleDisable: () => request<{ success: boolean }>("POST", "/tunnel/tailscale-disable"),

  // Model management.
  listDisabledModels: (providerAlias: string) =>
    request<{ ids: string[] }>("GET", `/models/disabled?provider=${encodeURIComponent(providerAlias)}`),
  disableModels: (providerAlias: string, ids: string[]) =>
    request<{ ids: string[] }>("POST", "/models/disabled", { providerAlias, ids }),
  enableModels: (providerAlias: string, ids: string[]) =>
    request<{ ids: string[] }>("DELETE", "/models/disabled", { providerAlias, ids }),

  // Update check (queries GitHub for the latest release + changelog).
  // Pass force=true to bypass the backend's 6-hour cache (the "Check now" button).
  updateCheck: (force?: boolean) =>
    request<UpdateInfo>("GET", `/update/check${force ? "?refresh=1" : ""}`),

  // Database export/import. An optional passphrase produces a portable backup
  // whose credentials are re-keyed to the passphrase (movable across machines
  // with different master keys).
  exportDatabase: (passphrase?: string) =>
    request<Record<string, unknown>>(
      "GET",
      passphrase ? `/settings/database?passphrase=${encodeURIComponent(passphrase)}` : "/settings/database",
    ),
  importDatabase: (payload: Record<string, unknown>, passphrase?: string) =>
    request<{ imported: number }>("POST", "/settings/database", passphrase ? { ...payload, passphrase } : payload),

  // Foreign config import: convert a 9router or OmniRoute backup JSON into
  // KeiRouter records (accounts re-sealed, api keys re-hashed, combos → chains).
  importForeignConfig: (source: "9router" | "omniroute", config: Record<string, unknown>) =>
    request<ForeignImportResult>("POST", "/settings/database/import-foreign", { source, config }),

  // Full router backup: every table (chains, providers, accounts, API keys,
  // portal users, balances, usage...) with secrets re-keyed to a passphrase.
  fullBackup: async (passphrase: string): Promise<Blob> => {
    const res = await fetchWithTimeout(
      "/api/settings/backup",
      { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ passphrase }) },
      300_000,
    );
    if (!res.ok) throw new APIError(res.status, await errorMessage(res));
    return res.blob();
  },
  fullRestore: async (file: File, passphrase: string): Promise<FullRestoreResult> => {
    const res = await fetchWithTimeout(
      "/api/settings/restore",
      { method: "POST", headers: { "Content-Type": "application/json", "X-Backup-Passphrase": passphrase }, body: file },
      600_000,
    );
    if (!res.ok) throw new APIError(res.status, await errorMessage(res));
    return res.json() as Promise<FullRestoreResult>;
  },

  sqliteStatus: () => request<SQLiteStatus>("GET", "/settings/sqlite"),
  backupSQLite: () => requestBlob("GET", "/settings/sqlite/backup"),
  restoreSQLite: (file: File) => {
    const body = new FormData();
    body.append("file", file);
    return requestForm<SQLiteRestoreResult>("POST", "/settings/sqlite/restore", body);
  },
  // Import 9router SQLite database directly (usageHistory, apiKeys, providers, proxyPools, settings).
  import9routerSQLite: (file: File, options?: Partial<N9routerImportOptions>) => {
    const body = new FormData();
    body.append("file", file);
    if (options) body.append("options", JSON.stringify(options));
    return requestForm<ForeignImportResult>("POST", "/settings/database/import-9router-sqlite", body);
  },
  // Analyze a 9router SQLite upload: per-table row counts, nothing written.
  analyze9routerSQLite: (file: File) => {
    const body = new FormData();
    body.append("file", file);
    return requestForm<N9routerAnalyzeResult>("POST", "/settings/database/analyze-9router-sqlite", body);
  },

  // Proxy test.
  testProxy: (proxyUrl: string) =>
    request<{ ok: boolean; status?: number; elapsedMs?: number; error?: string; exitIP?: string }>("POST", "/settings/proxy-test", { proxyUrl }),

  // Proxy pool test.
  testProxyPool: (id: string) =>
    request<{ status: string; last_tested?: string; elapsed_ms?: number; error?: string; egress_ip?: string }>("POST", `/proxy-pools/${id}/test`),

  // OAuth provider connections.
  oauthProviders: () => request<{ providers: OAuthProvider[] }>("GET", "/oauth/providers"),
  oauthAuthorize: (provider: string, redirectUri: string) =>
    request<{ authorize_url: string; state: string; redirect_uri?: string }>("POST", `/oauth/${provider}/authorize`, {
      redirect_uri: redirectUri,
    }),
  oauthExchange: (provider: string, input: { code: string; state: string; label?: string }) =>
    request<{ id: string; provider: string; email: string }>("POST", `/oauth/${provider}/exchange`, input),
  // Polls whether a redirect-based flow completed server-side. Needed for
  // providers whose auth pages sever window.opener (COOP), where the popup's
  // postMessage never reaches the dashboard.
  oauthCallbackStatus: (provider: string, state: string) =>
    request<OAuthCallbackStatus>("GET", `/oauth/${provider}/callback-status?state=${encodeURIComponent(state)}`),
  oauthDeviceCode: (provider: string) =>
    request<DeviceCode>("POST", `/oauth/${provider}/device-code`, {}),
  oauthDeviceCodeSubmit: (
    provider: string,
    input: {
      nonce: string;
      device_code: string;
      user_code: string;
      verification_uri: string;
      verification_uri_complete: string;
      expires_in: number;
      interval: number;
    },
  ) => request<DeviceCode>("POST", `/oauth/${provider}/device-code-submit`, input),
  oauthPoll: (provider: string, deviceCode: string, label?: string) =>
    request<OAuthPollResult>("POST", `/oauth/${provider}/poll`, { device_code: deviceCode, label }),

  // Kiro connect flow (AWS SSO OIDC device flows + import token). Mounted under
  // /kiro (not /oauth/kiro) to avoid the chi /oauth/{provider} route collision.
  kiroDeviceStart: (input: { method: "builder-id" | "idc"; start_url?: string; region?: string }) =>
    request<DeviceCode>("POST", "/kiro/device-start", input),
  kiroDevicePoll: (deviceCode: string, label?: string) =>
    request<OAuthPollResult>("POST", "/kiro/device-poll", { device_code: deviceCode, label }),
  kiroAPIKey: (apiKey: string, region?: string, label?: string) =>
    request<{ id: string; provider: string }>("POST", "/kiro/api-key", {
      api_key: apiKey,
      region,
      label,
    }),
  kiroImport: (refreshToken: string, label?: string) =>
    request<{ id: string; provider: string }>("POST", "/kiro/import", {
      refresh_token: refreshToken,
      label,
    }),
  kiroImportCLIProxy: (json: string) =>
    request<{ id: string; provider: string; email?: string }>("POST", "/kiro/import-cli-proxy", { json }),


  // Qoder connect flow (PKCE device-token poll). Mounted under /qoder (not
  // /oauth/qoder) to avoid the chi /oauth/{provider} route collision. The flow
  // generates a PKCE pair + nonce locally, opens the Qoder account picker in
  // the browser, then polls until the user authorizes.
  qoderDeviceStart: () =>
    request<DeviceCode>("POST", "/qoder/device-start", {}),
  qoderDevicePoll: (deviceCode: string, label?: string) =>
    request<OAuthPollResult>("POST", "/qoder/device-poll", { device_code: deviceCode, label }),

  // KiloCode connect flow (custom device-auth). Mounted under /kilocode (not
  // /oauth/kilocode) to avoid the chi /oauth/{provider} route collision.
  kilocodeDeviceStart: () =>
    request<DeviceCode>("POST", "/kilocode/device-start", {}),
  kilocodeDevicePoll: (deviceCode: string, label?: string) =>
    request<OAuthPollResult>("POST", "/kilocode/device-poll", { device_code: deviceCode, label }),

  // CodeBuddy connect flow (browser-poll auth). Mounted under /codebuddy.
  codebuddyAuthStart: () =>
    request<DeviceCode>("POST", "/codebuddy/auth-start", {}),
  codebuddyAuthPoll: (deviceCode: string, label?: string) =>
    request<OAuthPollResult>("POST", "/codebuddy/auth-poll", { device_code: deviceCode, label }),

  // Kimchi connect flow (browser-callback auth). Mounted under /kimchi.
  kimchiAuthStart: () =>
    request<DeviceCode>("POST", "/kimchi/auth-start", {}),
  kimchiAuthPoll: (deviceCode: string, label?: string) =>
    request<OAuthPollResult>("POST", "/kimchi/auth-poll", { device_code: deviceCode, label }),
  kimchiCallbackSubmit: (state: string, token: string) =>
    request<{ status: string }>("POST", "/kimchi/callback-submit", { state, token }),

  // Cursor connect flow (import token from Cursor IDE). Mounted under /cursor.
  cursorImport: (token: string, label?: string) =>
    request<{ id: string; provider: string }>("POST", "/cursor/import", { token, label }),

  // Command Code connect flow (import token from CLI or studio). Mounted under /commandcode.
  commandcodeImport: (token: string, label?: string) =>
    request<{ id: string; provider: string }>("POST", "/commandcode/import", { token, label }),

  // System monitoring.
  systemMonitor: () => request<SystemSnapshot>("GET", "/system"),
  systemHistory: () => request<SystemHistory>("GET", "/system/history"),
  systemResources: (hours: number, interval: string) =>
    request<SystemResourceBucket[]>("GET", `/system/resources?hours=${hours}&interval=${encodeURIComponent(interval)}`),

  // Guardrails (content-safety policies).
  listGuardrails: (scope?: GuardrailScope) =>
    request<{ guardrails: GuardrailPolicy[] }>(
      "GET",
      scope ? `/guardrails?scope=${encodeURIComponent(scope)}` : "/guardrails",
    ),
  getGuardrail: (id: string) =>
    request<GuardrailPolicy>("GET", `/guardrails/${id}`),
  createGuardrail: (input: {
    name?: string;
    scope: GuardrailScope;
    scope_id?: string;
    enabled?: boolean;
    config?: GuardrailPolicyConfig;
  }) => request<GuardrailPolicy>("POST", "/guardrails", input),
  updateGuardrail: (
    id: string,
    patch: { name?: string; enabled?: boolean; config?: GuardrailPolicyConfig },
  ) => request<GuardrailPolicy>("PATCH", `/guardrails/${id}`, patch),
  deleteGuardrail: (id: string) =>
    request<void>("DELETE", `/guardrails/${id}`),
  effectiveGuardrail: (params: {
    provider?: string;
    model?: string;
    chain?: string;
    apikey?: string;
  }) => {
    const qs = new URLSearchParams();
    if (params.provider) qs.set("provider", params.provider);
    if (params.model) qs.set("model", params.model);
    if (params.chain) qs.set("chain", params.chain);
    if (params.apikey) qs.set("apikey", params.apikey);
    const suffix = qs.toString();
    return request<EffectiveGuardrail>(
      "GET",
      `/guardrails/effective${suffix ? `?${suffix}` : ""}`,
    );
  },
  listGuardrailEntities: () =>
    request<{ entities: string[] }>("GET", "/guardrails/entities"),
  /** Optional engines this server can run, per detector (e.g. injection.classifier). */
  getGuardrailCapabilities: () =>
    request<{ capabilities: Record<string, Record<string, boolean>> }>("GET", "/guardrails/capabilities"),
  listAudit: (filter?: AuditFilter) =>
    request<{ entries: AuditEntry[]; next_cursor?: string }>("GET", `/audit${auditQuery(filter)}`),
  auditStatus: () => request<AuditStatus>("GET", "/audit/status"),
  verifyAudit: () => request<AuditVerifyReport>("GET", "/audit/verify"),
  /** JSON Lines of every matching entry (cursor/limit are ignored). */
  exportAudit: (filter?: AuditFilter) =>
    requestBlob("GET", `/audit/export${auditQuery({ ...filter, cursor: undefined, limit: undefined })}`),
  guardrailStats: (period: string, opts: { buckets?: number; api_key_id?: string; model?: string; chain_id?: string } = {}) => {
    const qs = new URLSearchParams({ period, tz: browserTZ() });
    for (const [k, v] of Object.entries(opts)) {
      if (v) qs.set(k, String(v));
    }
    return request<GuardrailStats>("GET", `/guardrails/stats?${qs.toString()}`);
  },
  listGuardrailLogs: (filter?: GuardrailLogFilter & { limit?: number }) => {
    const qs = new URLSearchParams();
    for (const [k, v] of Object.entries(filter ?? {})) {
      if (v) qs.set(k, String(v));
    }
    if (filter?.period) qs.set("tz", browserTZ());
    const suffix = qs.toString();
    return request<{ logs: GuardrailLogEntry[] }>(
      "GET",
      `/guardrails/logs${suffix ? `?${suffix}` : ""}`,
    );
  },
  testGuardrail: (input: { text: string; config?: GuardrailPolicyConfig }) =>
    request<GuardrailTestResult>("POST", "/guardrails/test", input),

  listGuardrailTemplates: () =>
    request<{ templates: GuardrailTemplate[] }>("GET", "/guardrails/templates"),

  exportGuardrails: (scope?: string) =>
    request<GuardrailBundle>(
      "GET",
      `/guardrails/export${scope ? `?scope=${encodeURIComponent(scope)}` : ""}`,
    ),

  importGuardrails: (bundle: GuardrailBundle) =>
    request<{
      imported: Array<{ name: string; scope: string; scope_id?: string }>;
      skipped: Array<{ name: string; reason: string }>;
    }>("POST", "/guardrails/import", bundle),

  getGuardrailTenantFlags: () =>
    request<{ allow_external_engines: boolean }>(
      "GET",
      "/guardrails/tenant-flags",
    ),

  putGuardrailTenantFlags: (flags: { allow_external_engines?: boolean }) =>
    request<{ allow_external_engines: boolean }>(
      "PUT",
      "/guardrails/tenant-flags",
      flags,
    ),

  // ---- Actionable provider health dashboard ----

  healthOverview: (range = "1h", status?: string) =>
    request<HealthOverview>(
      "GET",
      `/health/overview?range=${encodeURIComponent(range)}${status ? `&status=${encodeURIComponent(status)}` : ""}`,
    ),

  healthTimeline: (range = "24h", buckets = 24) =>
    request<HealthTimeline>("GET", `/health/timeline?range=${encodeURIComponent(range)}&buckets=${buckets}`),

  gatewayInfo: () => request<GatewayInfo>("GET", "/gateway/info"),

  healthProviderDetail: (provider: string, range = "24h") =>
    request<HealthProviderDetail>(
      "GET",
      `/health/providers/${encodeURIComponent(provider)}?range=${encodeURIComponent(range)}`,
    ),

  healthModels: (range = "1h", status?: string) =>
    request<{ models: HealthModelRow[] }>(
      "GET",
      `/health/models?range=${encodeURIComponent(range)}${status ? `&status=${encodeURIComponent(status)}` : ""}`,
    ),

  healthChains: (range = "1h") =>
    request<{ chains: HealthChainRow[] }>(
      "GET",
      `/health/chains?range=${encodeURIComponent(range)}`,
    ),

  healthChainDetail: (id: string) =>
    request<HealthChainDetail>("GET", `/health/chains/${encodeURIComponent(id)}`),

  healthProbeHistory: (params: { provider?: string; range?: string; page?: number; limit?: number } = {}) => {
    const qs = new URLSearchParams();
    if (params.provider) qs.set("provider", params.provider);
    qs.set("range", params.range ?? "24h");
    qs.set("page", String(params.page ?? 1));
    qs.set("limit", String(params.limit ?? 50));
    return request<{ items: HealthProbeRow[]; pagination: { page: number; limit: number; total: number } }>(
      "GET",
      `/health/probes?${qs.toString()}`,
    );
  },

  runHealthProbe: (input: { provider: string; provider_account_id?: string; model: string; capability?: string }) =>
    request<HealthProbeResult>("POST", "/health/probes/run", input),
};

// ---- Provider health types ----

export type HealthTimelineStatus = "ok" | "degraded" | "down" | "idle";

export interface HealthTimelineBucket {
  start: string;
  requests: number;
  failures: number;
  rate_limited: number;
  p95_ms: number;
  status: HealthTimelineStatus;
}

export interface HealthTimelineProvider {
  provider: string;
  display_name: string;
  color: string;
  icon: string;
  requests: number;
  failures: number;
  fallbacks: number;
  rate_limited: number;
  success_rate: number;
  worst_p95_ms: number;
  buckets: HealthTimelineBucket[];
}

export interface HealthTimeline {
  since: string;
  until: string;
  bucket_seconds: number;
  providers: HealthTimelineProvider[];
}

export interface GatewayInfo {
  name: string;
  version: string;
  uptime_s: number;
  started_at: string;
  listen_addr: string;
  dialect: "sqlite" | "postgres" | string;
  go_version: string;
  os: string;
  arch: string;
}

export type HealthStatus = "healthy" | "degraded" | "unhealthy" | "unknown" | "disabled";

export interface HealthSummary {
  healthy: number;
  degraded: number;
  unhealthy: number;
  unknown: number;
  disabled: number;
  fallbacks: number;
  avg_p95_latency_ms: number;
  telemetry_dropped: number;
  telemetry_dropped_scope: "process_lifetime";
}

export interface HealthProviderRow {
  provider: string;
  status: HealthStatus;
  score: number;
  accounts: number;
  models_monitored: number;
  success_rate: number;
  error_rate: number;
  latency_p95_ms: number;
  ttft_p95_ms: number;
  fallback_count: number;
  last_probe_at?: string;
  main_issue?: string;
  recommendation?: string;
  /** Historical ranges only: request volume, status over the whole range, and the live status. */
  requests?: number;
  range_status?: HealthStatus;
  live_status?: HealthStatus;
  rate_limited_count?: number;
  final_failure_count?: number;
  last_seen_at?: string;
}

export interface HealthOverviewWindow {
  /** rolling_current: the live telemetry window; historical: persisted snapshots over the requested range. */
  kind: "rolling_current" | "historical";
  duration_seconds: number;
  requested_range: string;
  generated_at: string;
  since?: string;
}

export interface HealthOverview {
  window: HealthOverviewWindow;
  summary: HealthSummary;
  providers: HealthProviderRow[];
}

export interface HealthSnapshot {
  bucket_start: string;
  request_count: number;
  success_count: number;
  failure_count: number;
  fallback_count: number;
  latency_p50_ms?: number;
  latency_p95_ms?: number;
  latency_p99_ms?: number;
  ttft_p50_ms?: number;
  ttft_p95_ms?: number;
  ttft_p99_ms?: number;
  rate_limited_count: number;
  auth_error_count: number;
  quota_exceeded_count: number;
  timeout_count: number;
  provider_5xx_count: number;
  bad_request_count: number;
  network_error_count: number;
  health_score: number;
  health_status: HealthStatus;
}

export interface HealthProviderDetail {
  provider: string;
  status: HealthStatus;
  score: number;
  main_issue: string;
  recommendation: string;
  metrics: {
    requests: number;
    success_rate: number;
    error_rate: number;
    latency_p95_ms: number;
    ttft_p95_ms: number;
    fallback_count: number;
  };
  error_breakdown: Record<string, number>;
  models: HealthModelRow[];
  snapshots: HealthSnapshot[];
}

export interface HealthModelRow {
  provider?: string;
  provider_account_id?: string;
  model: string;
  capability?: string;
  status: HealthStatus;
  score: number;
  success_rate: number;
  error_rate: number;
  latency_p95_ms?: number;
  ttft_p95_ms?: number;
  fallback_count: number;
  main_issue?: string;
  last_updated_at?: string;
  requests?: number;
  recommendation?: string;
}

export interface HealthChainRow {
  chain_id: string;
  name: string;
  status: HealthStatus;
  affected_provider: string;
  affected_model: string;
  main_issue?: string;
  step_count?: number;
  requests: number;
  fallback_rate: number;
  fallback_count: number;
  final_failure_count: number;
  recommendation: string;
}

export interface HealthChainStep {
  position: number;
  provider: string;
  model: string;
  status: HealthStatus;
  score: number;
  main_issue: string;
}

export interface HealthChainDetail {
  chain_id: string;
  name: string;
  strategy: string;
  steps: HealthChainStep[];
  requests?: number;
  fallback_rate?: number;
  fallback_count?: number;
  final_failure_count?: number;
  fallback_provider?: string;
  fallback_model?: string;
}

export interface HealthProbeRow {
  time: string;
  provider: string;
  provider_account_id: string;
  model: string;
  capability: string;
  status: string;
  http_status?: number;
  latency_ms?: number;
  ttft_ms?: number;
  error_type?: string;
  error_message?: string;
  triggered_by: string;
}

export interface HealthProbeResult {
  status: string;
  provider: string;
  model: string;
  http_status?: number;
  latency_ms?: number;
  ttft_ms?: number;
  error_type?: string;
  message: string;
}

export interface GuardrailTemplate {
  id: string;
  name: string;
  description: string;
  config: GuardrailPolicyConfig;
}

export interface GuardrailBundle {
  version: number;
  exported_at?: string;
  policies: Array<{
    name: string;
    scope: string;
    scope_id?: string;
    enabled: boolean;
    config: GuardrailPolicyConfig;
  }>;
}

// ---- SSE usage stream --------------------------------------------------------

export interface UsageEvent {
  provider: string;
  model: string;
  account_id: string;
  tokens: number;
}

/**
 * Creates an EventSource connected to the usage SSE stream. The caller
 * provides a callback that fires on each usage event. Returns a cleanup
 * function that closes the connection.
 */
export function connectUsageStream(onEvent: (ev: UsageEvent) => void): () => void {
  let es: EventSource | null = null;
  let retryCount = 0;
  const maxRetries = 10;
  let closed = false;

  function connect() {
    if (closed) return;
    es = new EventSource("/api/usage/stream");
    
    es.onopen = () => {
      retryCount = 0; // reset on successful connection
    };
    
    es.onmessage = (msg) => {
      try {
        const ev = JSON.parse(msg.data) as UsageEvent;
        onEvent(ev);
      } catch { /* ignore malformed events */ }
    };
    
    es.onerror = () => {
      es?.close();
      if (closed) return;
      
      if (retryCount < maxRetries) {
        const delay = Math.min(1000 * 2 ** retryCount, 30000);
        setTimeout(connect, delay);
        retryCount++;
      }
    };
  }

  connect();

  return () => {
    closed = true;
    es?.close();
  };
}

/**
 * Subscribe to the guardrails audit-log SSE stream. New rows arrive as they
 * land in the database (the AuditWriter publishes after each successful batch
 * insert). Returns a cleanup function that closes the connection.
 */
export function connectGuardrailLogStream(
  onEvent: (row: GuardrailLogEntry) => void,
): () => void {
  let es: EventSource | null = null;
  let retryCount = 0;
  const maxRetries = 10;
  let closed = false;

  function connect() {
    if (closed) return;
    es = new EventSource("/api/guardrails/logs/stream");
    es.onopen = () => {
      retryCount = 0;
    };
    es.onmessage = (msg) => {
      try {
        const row = JSON.parse(msg.data) as GuardrailLogEntry;
        onEvent(row);
      } catch {
        /* ignore malformed events */
      }
    };
    es.onerror = () => {
      es?.close();
      if (closed) return;
      if (retryCount < maxRetries) {
        const delay = Math.min(1000 * 2 ** retryCount, 30000);
        setTimeout(connect, delay);
        retryCount++;
      }
    };
  }

  connect();
  return () => {
    closed = true;
    es?.close();
  };
}

export { APIError };
