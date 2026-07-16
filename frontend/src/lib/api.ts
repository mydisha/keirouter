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
  stream_stall_timeout_ms: number;
  response_header_timeout_ms: number;
  request_timeout_ms: number;
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
  allowed_models?: string[];
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
}

export interface Chain {
  id: string;
  name: string;
  strategy: string;
  fallback_provider?: string;
  fallback_model?: string;
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
  failures: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
}

export interface UsageInsightsSummary {
  total_requests: number;
  successful_requests: number;
  failed_requests: number;
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
  savings: TokenSavings;
  providers: ProviderUsage[];
  recent: RecentActivity[];
  series: SeriesPoint[];
  busiest: string;
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
export interface ConsoleLogEntry {
  seq: number;
  time: string; // HH:MM:SS.mmm
  level: string; // DEBUG | INFO | WARN | ERROR | LOG
  msg: string; // human-readable summary
  detail?: string; // optional technical detail, revealed on expand
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
}

export interface Skill {
  id: string;
  name: string;
  description: string;
  prompt: string;
  enabled: boolean;
  created_at: string;
}

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

export interface CLITool {
  id: string;
  name: string;
  dialect: string;
  instructions: string;
  snippet: string;
  installed: boolean;
  configured: boolean;
  config_path: string;
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
  cpu_pct: number;
  cpu_per_core: number[];
  mem_total_mb: number;
  mem_used_mb: number;
  mem_available_mb: number;
  mem_pct: number;
  disk_total_gb: number;
  disk_used_gb: number;
  disk_free_gb: number;
  disk_pct: number;
  goroutines: number;
  heap_alloc_mb: number;
  heap_sys_mb: number;
  heap_inuse_mb: number;
  heap_idle_mb: number;
  gc_pause_total_ms: number;
  gc_pause_last_ms: number;
  gc_cycles: number;
  open_fds: number;
  net_conns: number;
  uptime_s: number;
  pid: number;
  host: string;
  os: string;
  arch: string;
  // Process-level metrics
  proc_cpu_pct: number;
  proc_rss_mb: number;
  proc_threads: number;
  proc_open_fds: number;
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
  proc_cpu_pct?: number;
  proc_rss_mb?: number;
  proc_threads?: number;
  proc_open_fds?: number;
}

export interface SystemHistory {
  interval_sec: number;
  max_size: number;
  spikes: SystemSample[];
  samples: SystemSample[];
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
}

export interface InjectionConfig {
  enabled: boolean;
  severity_threshold?: GuardrailSeverity;
  action?: GuardrailAction;
}

export interface TopicsConfig {
  enabled: boolean;
  mode?: "allow" | "block";
  topics?: string[];
  action?: GuardrailAction;
  engine?: "keyword" | "embedding";
  similarity_threshold?: number;
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
  errors?: string[];
}

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
  // Uploads (e.g. SQLite restore) can legitimately take longer than a JSON
  // call, so allow a more generous deadline than the default.
  const res = await fetchWithTimeout(`/api${path}`, { method, body }, 60_000);
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
    return { name: "KeiRouter", logo_url: "", favicon_url: "", tagline: "", color_palette: "sage-terra" };
  }
  return resp.json();
}

/**
 * Fetch usage stats for an API Key, authenticated via the key itself (public portal)
 */
export async function fetchKeyUsage(key: string, days?: number): Promise<KeyUsageData> {
  const qs = days ? `?days=${days}` : "";
  const resp = await fetch(`/v1/keys/me/usage${qs}`, {
    headers: { Authorization: `Bearer ${key}` },
  });
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({}));
    throw new Error(data.error || "Invalid key or server error");
  }
  return resp.json();
}

/**
 * Fetch usage stats for an API Key using its database ID (public portal link sharing)
 */
export async function fetchKeyUsageById(id: string, days?: number): Promise<KeyUsageData> {
  const qs = days ? `?days=${days}` : "";
  const resp = await fetch(`/v1/portal/keys/${id}/usage${qs}`);
  if (!resp.ok) {
    const data = await resp.json().catch(() => ({}));
    throw new Error(data.error || "Invalid key ID or server error");
  }
  return resp.json();
}

export const api = {
  // Auth (no session required for status/login/logout).
  authStatus: () => request<AuthStatus>("GET", "/auth/status"),
  login: (password: string) =>
    request<{ ok: boolean; using_default: boolean; onboarding_complete: boolean }>(
      "POST",
      "/auth/login",
      { password },
    ),
  logout: () => request<{ ok: boolean }>("POST", "/auth/logout"),
  changePassword: (newPassword: string) =>
    request<{ ok: boolean }>("POST", "/auth/password", { new_password: newPassword }),
  completeOnboarding: () => request<{ ok: boolean }>("POST", "/auth/onboarding/complete"),

  providers: () => request<{ providers: Provider[] }>("GET", "/providers"),
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
  createChain: (input: { name: string; strategy?: string; fallback_provider?: string; fallback_model?: string; steps: { provider: string; model: string }[] }) =>
    request<{ id: string }>("POST", "/chains", input),
  updateChain: (id: string, patch: { name?: string; strategy?: string; fallback_provider?: string; fallback_model?: string; steps?: { provider: string; model: string }[] }) =>
    request<{ id: string }>("PATCH", `/chains/${id}`, patch),
  deleteChain: (id: string) => request<void>("DELETE", `/chains/${id}`),

  listBudgets: () => request<{ budgets: Budget[] }>("GET", "/budgets"),
  budgetStatus: () => request<{ budgets: BudgetStatus[] }>("GET", "/budgets/status"),
  createBudget: (input: { scope_kind?: string; scope_id?: string; limit_usd?: number; limit_tokens?: number; period?: string; alert_pct?: number; hard_cutoff?: boolean }) =>
    request<{ id: string }>("POST", "/budgets", input),
  updateBudget: (id: string, patch: { limit_usd?: number; limit_tokens?: number; period?: string; alert_pct?: number; hard_cutoff?: boolean }) =>
    request<void>("PATCH", `/budgets/${id}`, patch),
  deleteBudget: (id: string) => request<void>("DELETE", `/budgets/${id}`),

  usage: (period: string) => request<UsageSummary>("GET", `/usage?period=${period}&tz=${browserTZ()}`),
  usageInsights: (period: string) =>
    request<UsageInsights>("GET", `/usage/insights?period=${period}&tz=${browserTZ()}`),
  modelUsage: (period: string) =>
    request<ModelUsageResponse>("GET", `/usage/models?period=${period}&tz=${browserTZ()}`),

  quota: (period: string) =>
    request<{ accounts: QuotaAccount[]; since: string }>("GET", `/quota?period=${period}&tz=${browserTZ()}`),
  quotaByProvider: (provider: string) =>
    request<{ accounts: QuotaAccount[]; since: string }>("GET", `/quota?period=today&tz=${browserTZ()}&provider=${provider}`),

  consoleLog: () => request<{ logs: ConsoleLogEntry[] }>("GET", "/console"),

  cliTools: (model?: string) =>
    request<CLIToolsResponse>("GET", model ? `/cli-tools?model=${encodeURIComponent(model)}` : "/cli-tools"),
  cliToolConfigure: (toolId: string, body: { base_url: string; api_key: string; models?: string[] }) =>
    request<{ ok: boolean }>("POST", `/cli-tools/${toolId}/configure`, body),
  cliToolRemove: (toolId: string) =>
    request<{ ok: boolean }>("POST", `/cli-tools/${toolId}/remove`),

  listProxyPools: () => request<{ pools: ProxyPool[] }>("GET", "/proxy-pools"),
  createProxyPool: (input: { name: string; type?: string; proxy_url: string; no_proxy?: string; strict?: boolean; is_active?: boolean }) =>
    request<{ id: string }>("POST", "/proxy-pools", input),
  deployCloudflareRelay: (input: { account_id: string; api_token: string; project_name?: string }) =>
    request<{ id: string; name: string; deploy_url: string; test_status: string }>("POST", "/proxy-pools/cloudflare-deploy", input),
  updateProxyPool: (id: string, patch: { name?: string; proxy_url?: string; no_proxy?: string; strict?: boolean; is_active?: boolean }) =>
    request<void>("PATCH", `/proxy-pools/${id}`, patch),
  deleteProxyPool: (id: string) => request<void>("DELETE", `/proxy-pools/${id}`),

  listSkills: () => request<{ skills: Skill[] }>("GET", "/skills"),
  createSkill: (input: { name: string; description?: string; prompt?: string; enabled?: boolean }) =>
    request<Skill>("POST", "/skills", input),
  updateSkill: (id: string, patch: { enabled?: boolean }) =>
    request<void>("POST", `/skills/${id}`, patch),
  deleteSkill: (id: string) => request<void>("DELETE", `/skills/${id}`),

  endpointSettings: () => request<EndpointSettings>("GET", "/settings/endpoint"),
  updateEndpointSettings: (patch: Partial<EndpointSettings>) =>
    request<EndpointSettings>("POST", "/settings/endpoint", patch),
  testHeadroom: (body?: { url?: string; timeout_ms?: number }) =>
    request<HeadroomTestResult>("POST", "/settings/headroom-test", body ?? {}),

  accessSettings: () => request<AccessSettings>("GET", "/settings/access"),
  updateAccessSettings: (patch: Partial<Omit<AccessSettings, "endpoint_url">>) =>
    request<AccessSettings>("POST", "/settings/access", patch),

  // Branding / white-label settings.
  branding: () => request<BrandingSettings>("GET", "/settings/branding"),
  updateBranding: (patch: Partial<BrandingSettings>) =>
    request<BrandingSettings>("POST", "/settings/branding", patch),

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

  sqliteStatus: () => request<SQLiteStatus>("GET", "/settings/sqlite"),
  backupSQLite: () => requestBlob("GET", "/settings/sqlite/backup"),
  restoreSQLite: (file: File) => {
    const body = new FormData();
    body.append("file", file);
    return requestForm<SQLiteRestoreResult>("POST", "/settings/sqlite/restore", body);
  },

  // Proxy test.
  testProxy: (proxyUrl: string) =>
    request<{ ok: boolean; status?: number; elapsedMs?: number; error?: string; exitIP?: string }>("POST", "/settings/proxy-test", { proxyUrl }),

  // Proxy pool test.
  testProxyPool: (id: string) =>
    request<{ status: string; last_tested?: string; elapsed_ms?: number; error?: string }>("POST", `/proxy-pools/${id}/test`),

  // OAuth provider connections.
  oauthProviders: () => request<{ providers: OAuthProvider[] }>("GET", "/oauth/providers"),
  oauthAuthorize: (provider: string, redirectUri: string) =>
    request<{ authorize_url: string; state: string; redirect_uri?: string }>("POST", `/oauth/${provider}/authorize`, {
      redirect_uri: redirectUri,
    }),
  oauthExchange: (provider: string, input: { code: string; state: string; label?: string }) =>
    request<{ id: string; provider: string; email: string }>("POST", `/oauth/${provider}/exchange`, input),
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
  listGuardrailLogs: (filter?: {
    api_key_id?: string;
    detector?: string;
    action?: string;
    limit?: number;
  }) => {
    const qs = new URLSearchParams();
    if (filter?.api_key_id) qs.set("api_key_id", filter.api_key_id);
    if (filter?.detector) qs.set("detector", filter.detector);
    if (filter?.action) qs.set("action", filter.action);
    if (filter?.limit) qs.set("limit", String(filter.limit));
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
}

export interface HealthOverviewWindow {
  kind: "rolling_current";
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
