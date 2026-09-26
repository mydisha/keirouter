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

export interface PublicModel {
  name: string;
  model_id: string;
  provider: string;
  input_per_m: number;
  output_per_m: number;
  capabilities: PublicCapabilities;
  usage_24h: { users: number; requests: number; tokens: number };
}

export interface PublicOverview {
  total_requests: number;
  total_tokens: number;
  rps_10s: number;
  success_24h: number;
  failed_24h: number;
  top_models: { name: string; requests: number; tokens: number }[];
  recent: { provider: string; model: string; status: string; latency_ms: number; ttft_ms: number }[];
}

export interface PublicPerformance {
  model: string;
  avg_latency_ms: number;
  avg_ttft_ms: number;
  success_rate: number;
  series: { bucket: number; requests: number; tokens: number }[];
}

export interface PublicArchived {
  podium: { model: string; tokens: number; requests: number }[];
  history: { model: string; tokens: number; requests: number; status: string }[];
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`public api ${path}: ${res.status}`);
  return (await res.json()) as T;
}

export const fetchPublicOverview = () => getJSON<PublicOverview>("/v1/public/overview");

export const fetchPublicModels = () =>
  getJSON<{ models: PublicModel[] }>("/v1/public/models").then((d) => d.models);

export const fetchPublicPerformance = (model: string) =>
  getJSON<PublicPerformance>(`/v1/public/performance?model=${encodeURIComponent(model)}`);

export const fetchPublicArchived = () => getJSON<PublicArchived>("/v1/public/archived");
