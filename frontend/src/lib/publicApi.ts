// Typed client for the public landing API (/v1/public/*). Deliberately
// dependency-free and separate from lib/api.ts so the admin client never
// enters the public bundle. Responses are aggregate-only; see the Go handlers
// in backend/internal/gateway/public.go for the authoritative shape.

export interface PublicCapabilities {
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

export interface PublicUsage {
  users: number;
  requests: number;
  tokens: number;
}

export interface PublicModel {
  name: string;
  model_id: string;
  provider: string;
  provider_id: string;
  input_per_m: number;
  output_per_m: number;
  cached_per_m: number;
  cache_write_per_m: number;
  capabilities: PublicCapabilities;
  usage: PublicUsage;
}

export interface LandingNotification {
  id: string;
  tag?: string;
  title: string;
  body: string;
  href?: string;
}

export interface PublicOverview {
  total_requests: number;
  total_tokens: number;
  success: number;
  failed: number;
  model_count: number;
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`public api ${path}: ${res.status}`);
  return (await res.json()) as T;
}

export const fetchPublicOverview = () => getJSON<PublicOverview>("/v1/public/overview");

export const fetchPublicModels = () =>
  getJSON<{ models: PublicModel[] }>("/v1/public/models").then((d) => d.models);

export const fetchPublicNotifications = () =>
  getJSON<{ notifications: LandingNotification[] }>("/v1/public/notifications").then((d) => d.notifications);

export interface PublicBansos {
  exists: boolean;
  active: boolean;
  mode: "credit" | "unlimited";
  masked_display: string;
  allowed_models: string[];
  rpm: number;
  tpm: number;
  credit_remaining_usd: number | null;
  updated_at?: string;
}

export const fetchPublicBansos = () => getJSON<PublicBansos>("/v1/public/bansos");

export const fetchPublicBansosKey = () =>
  getJSON<{ key: string }>("/v1/public/bansos/key").then((d) => d.key);
