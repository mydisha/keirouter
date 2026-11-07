import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";
import type { AuthHook, Config, Plugin, ProviderHook } from "@opencode-ai/plugin";
import type { Model as ModelV2 } from "@opencode-ai/sdk/v2";

export interface KeiRouterPluginOptions {
  /** OpenCode provider id for this plugin instance. */
  providerId?: string;
  /** Label shown by OpenCode. */
  displayName?: string;
  /** KeiRouter base URL. May include or omit /v1. */
  baseURL?: string;
  /** In-memory /v1/models cache TTL in milliseconds. */
  modelCacheTtl?: number;
}

export interface KeiRouterResolvedOptions {
  providerId: string;
  displayName: string;
  baseURL: string;
  modelCacheTtl: number;
}

export interface KeiRouterRawModel {
  id: string;
  object?: string;
  owned_by?: string;
  provider?: string;
  kind?: string;
  name?: string;
  dimensions?: number;
  context_length?: number;
  max_input_tokens?: number;
  max_output_tokens?: number;
  release_date?: string;
  capabilities?: {
    attachment?: boolean;
    vision?: boolean;
    reasoning?: boolean;
    thinking?: boolean;
    tool_calling?: boolean;
    temperature?: boolean;
    // Names KeiRouter's /v1/models uses.
    tools?: boolean;
    pdf?: boolean;
    audio_input?: boolean;
    video_input?: boolean;
    image_output?: boolean;
    audio_output?: boolean;
    context_window?: number;
    max_output?: number;
  };
  input_modalities?: string[];
  output_modalities?: string[];
}

export interface KeiRouterStaticModel {
  name: string;
  attachment?: boolean;
  reasoning?: boolean;
  tool_call?: boolean;
  temperature?: boolean;
  limit?: {
    context: number;
    output: number;
  };
  modalities?: {
    input: string[];
    output: string[];
  };
}

export interface KeiRouterStaticProvider {
  npm: "@ai-sdk/openai-compatible";
  name: string;
  options: {
    baseURL: string;
    apiKey: string;
  };
  models: Record<string, KeiRouterStaticModel>;
}

export interface KeiRouterFetchCacheEntry {
  rawModels: KeiRouterRawModel[];
  expiresAt: number;
}

export type KeiRouterFetchCache = Map<string, KeiRouterFetchCacheEntry>;
export type KeiRouterModelsFetcher = (
  baseURL: string,
  apiKey: string,
  timeoutMs?: number
) => Promise<KeiRouterRawModel[]>;
export type KeiRouterReadAuthJson = () => Promise<Record<string, unknown> | undefined | null>;

const DEFAULT_PROVIDER_ID = "keirouter";
const DEFAULT_DISPLAY_NAME = "KeiRouter";
const DEFAULT_BASE_URL = "http://127.0.0.1:20180";
const DEFAULT_MODEL_CACHE_TTL_MS = 300_000;

export function resolveKeiRouterPluginOptions(opts?: KeiRouterPluginOptions): KeiRouterResolvedOptions {
  const providerId = cleanString(opts?.providerId) ?? DEFAULT_PROVIDER_ID;
  const displayName = cleanString(opts?.displayName) ?? DEFAULT_DISPLAY_NAME;
  const modelCacheTtl =
    typeof opts?.modelCacheTtl === "number" && Number.isFinite(opts.modelCacheTtl) && opts.modelCacheTtl > 0
      ? opts.modelCacheTtl
      : DEFAULT_MODEL_CACHE_TTL_MS;
  return {
    providerId,
    displayName,
    baseURL: cleanString(opts?.baseURL) ?? DEFAULT_BASE_URL,
    modelCacheTtl,
  };
}

export function createKeiRouterAuthHook(opts?: KeiRouterPluginOptions): AuthHook {
  const resolved = resolveKeiRouterPluginOptions(opts);
  return {
    provider: resolved.providerId,
    methods: [
      {
        type: "api",
        label: `${resolved.displayName} API Key`,
        prompts: [
          {
            type: "text",
            key: "apiKey",
            message: `KeiRouter API key (${resolved.providerId})`,
          },
        ],
      },
    ],
    loader: async (getAuth) => {
      const auth = await getAuth();
      const apiKey = apiKeyFromAuth(auth);
      if (!apiKey) return {};

      return {
        apiKey,
        baseURL: toOpenAICompatibleBaseURL(resolved.baseURL),
        fetch: createKeiRouterFetch({ apiKey, baseURL: resolved.baseURL }),
      };
    },
  };
}

export function createKeiRouterProviderHook(
  opts?: KeiRouterPluginOptions,
  deps: {
    fetcher?: KeiRouterModelsFetcher;
    now?: () => number;
    cache?: KeiRouterFetchCache;
  } = {}
): ProviderHook {
  const resolved = resolveKeiRouterPluginOptions(opts);
  const fetcher = deps.fetcher ?? defaultKeiRouterModelsFetcher;
  const now = deps.now ?? Date.now;
  const cache = deps.cache ?? new Map<string, KeiRouterFetchCacheEntry>();

  return {
    id: resolved.providerId,
    async models(_provider, ctx) {
      const apiKey = apiKeyFromAuth(ctx?.auth);
      if (!apiKey) return {};

      const baseURL = resolved.baseURL;
      const key = modelsCacheKey(baseURL, apiKey);
      const t = now();
      let rawModels = cache.get(key)?.rawModels;

      if (!rawModels || (cache.get(key)?.expiresAt ?? 0) <= t) {
        rawModels = await fetcher(baseURL, apiKey, 10_000);
        cache.set(key, { rawModels, expiresAt: t + resolved.modelCacheTtl });
      }

      return buildModelV2Map(rawModels, resolved, baseURL);
    },
  };
}

export function createKeiRouterConfigHook(
  opts?: KeiRouterPluginOptions,
  deps: {
    readAuthJson?: KeiRouterReadAuthJson;
    fetcher?: KeiRouterModelsFetcher;
    now?: () => number;
    cache?: KeiRouterFetchCache;
  } = {}
): (input: Config) => Promise<void> {
  const resolved = resolveKeiRouterPluginOptions(opts);
  const readAuthJson = deps.readAuthJson ?? defaultReadAuthJson;
  const fetcher = deps.fetcher ?? defaultKeiRouterModelsFetcher;
  const now = deps.now ?? Date.now;
  const cache = deps.cache ?? new Map<string, KeiRouterFetchCacheEntry>();

  return async (input: Config) => {
    const authJson = await readAuthJson().catch(() => undefined);
    const auth = authJson?.[resolved.providerId];
    const apiKey = apiKeyFromAuth(auth);
    if (!apiKey) return;

    const baseURL = resolved.baseURL;
    const key = modelsCacheKey(baseURL, apiKey);
    const t = now();
    let rawModels = cache.get(key)?.rawModels;

    if (!rawModels || (cache.get(key)?.expiresAt ?? 0) <= t) {
      rawModels = await fetcher(baseURL, apiKey, 10_000);
      cache.set(key, { rawModels, expiresAt: t + resolved.modelCacheTtl });
    }

    input.provider ??= {};
    input.provider[resolved.providerId] = buildStaticProviderEntry(
      rawModels,
      resolved,
      baseURL,
      apiKey
    ) as NonNullable<Config["provider"]>[string];
  };
}

export function buildModelV2Map(
  rawModels: KeiRouterRawModel[],
  opts: KeiRouterResolvedOptions,
  baseURL: string
): Record<string, ModelV2> {
  const models: Record<string, ModelV2> = {};
  for (const raw of rawModels) {
    if (!raw?.id) continue;
    models[modelKey(raw.id, opts.providerId)] = mapRawModelToModelV2(raw, opts, baseURL);
  }
  return models;
}

// withServerFields fills the fields this plugin reads from the names
// KeiRouter's /v1/models sends inside capabilities.
export function withServerFields(raw: KeiRouterRawModel): KeiRouterRawModel {
  const caps = raw.capabilities;
  if (!caps) return raw;
  const input = ["text"];
  if (caps.vision) input.push("image");
  if (caps.pdf) input.push("pdf");
  if (caps.audio_input) input.push("audio");
  if (caps.video_input) input.push("video");
  const output = ["text"];
  if (caps.image_output) output.push("image");
  if (caps.audio_output) output.push("audio");
  return {
    ...raw,
    context_length: raw.context_length ?? caps.context_window,
    max_output_tokens: raw.max_output_tokens ?? caps.max_output,
    input_modalities: raw.input_modalities ?? input,
    output_modalities: raw.output_modalities ?? output,
    capabilities: { ...caps, tool_calling: caps.tool_calling ?? caps.tools },
  };
}

export function mapRawModelToModelV2(
  serverRaw: KeiRouterRawModel,
  opts: KeiRouterResolvedOptions,
  baseURL: string
): ModelV2 {
  const raw = withServerFields(serverRaw);
  const caps = raw.capabilities ?? {};
  const inMods = new Set(normalizeModalities(raw.input_modalities));
  const outMods = new Set(normalizeModalities(raw.output_modalities));
  return {
    id: raw.id.includes("/") ? raw.id : `${opts.providerId}/${raw.id}`,
    providerID: opts.providerId,
    api: {
      id: "openai-compatible",
      url: toOpenAICompatibleBaseURL(baseURL),
      npm: "@ai-sdk/openai-compatible",
    },
    name: raw.name || raw.id,
    capabilities: {
      temperature: caps.temperature ?? true,
      reasoning: Boolean(caps.reasoning || caps.thinking),
      attachment: Boolean(caps.attachment ?? caps.vision ?? false),
      toolcall: Boolean(caps.tool_calling ?? false),
      input: {
        text: inMods.has("text"),
        audio: inMods.has("audio"),
        image: inMods.has("image"),
        video: inMods.has("video"),
        pdf: inMods.has("pdf"),
      },
      output: {
        text: outMods.has("text"),
        audio: outMods.has("audio"),
        image: outMods.has("image"),
        video: outMods.has("video"),
        pdf: outMods.has("pdf"),
      },
      interleaved: Boolean(caps.thinking),
    },
    cost: { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: {
      context: typeof raw.context_length === "number" ? raw.context_length : 0,
      ...(typeof raw.max_input_tokens === "number" ? { input: raw.max_input_tokens } : {}),
      output: typeof raw.max_output_tokens === "number" ? raw.max_output_tokens : 0,
    },
    status: "active",
    options: {},
    headers: {},
    release_date: raw.release_date ?? "",
  };
}

export function buildStaticProviderEntry(
  rawModels: KeiRouterRawModel[],
  opts: KeiRouterResolvedOptions,
  baseURL: string,
  apiKey: string
): KeiRouterStaticProvider {
  return {
    npm: "@ai-sdk/openai-compatible",
    name: opts.displayName,
    options: {
      baseURL: toOpenAICompatibleBaseURL(baseURL),
      apiKey,
    },
    models: buildModelMap(rawModels, opts.providerId),
  };
}

export function mapRawModel(serverRaw: KeiRouterRawModel): KeiRouterStaticModel {
  const raw = withServerFields(serverRaw);
  const caps = raw.capabilities ?? {};
  const model: KeiRouterStaticModel = { name: raw.name || raw.id };

  const attachment = caps.attachment ?? caps.vision;
  if (typeof attachment === "boolean") model.attachment = attachment;
  if (typeof caps.reasoning === "boolean" || typeof caps.thinking === "boolean") {
    model.reasoning = Boolean(caps.reasoning || caps.thinking);
  }
  if (typeof caps.tool_calling === "boolean") model.tool_call = caps.tool_calling;
  if (typeof caps.temperature === "boolean") model.temperature = caps.temperature;
  if (
    typeof raw.context_length === "number" &&
    raw.context_length > 0 &&
    typeof raw.max_output_tokens === "number" &&
    raw.max_output_tokens > 0
  ) {
    model.limit = { context: raw.context_length, output: raw.max_output_tokens };
  }
  if (Array.isArray(raw.input_modalities) || Array.isArray(raw.output_modalities)) {
    model.modalities = {
      input: normalizeModalities(raw.input_modalities),
      output: normalizeModalities(raw.output_modalities),
    };
  }

  return model;
}

export function modelKey(id: string, providerId: string): string {
  return id.includes("/") ? id : `${providerId}/${id}`;
}

export function buildModelMap(
  rawModels: KeiRouterRawModel[],
  providerId = DEFAULT_PROVIDER_ID
): Record<string, KeiRouterStaticModel> {
  const models: Record<string, KeiRouterStaticModel> = {};
  for (const raw of rawModels) {
    if (!raw?.id) continue;
    models[modelKey(raw.id, providerId)] = mapRawModel(raw);
  }
  return models;
}

export async function defaultKeiRouterModelsFetcher(
  baseURL: string,
  apiKey: string,
  timeoutMs = 10_000
): Promise<KeiRouterRawModel[]> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(modelsURL(baseURL), {
      method: "GET",
      headers: { Authorization: `Bearer ${apiKey}` },
      signal: controller.signal,
    });
    const text = await res.text();
    if (!res.ok) {
      throw new Error(`GET /v1/models returned ${res.status}: ${text.slice(0, 500)}`);
    }
    const body = JSON.parse(text) as { data?: unknown };
    if (!Array.isArray(body.data)) {
      throw new Error("GET /v1/models response missing data[]");
    }
    return body.data.filter(isRawModel);
  } finally {
    clearTimeout(timeout);
  }
}

export function modelsURL(baseURL: string): string {
  const clean = baseURL.replace(/\/+$/, "");
  return /\/v\d+$/.test(clean) ? `${clean}/models` : `${clean}/v1/models`;
}

export function toOpenAICompatibleBaseURL(baseURL: string): string {
  const clean = baseURL.replace(/\/+$/, "");
  return /\/v\d+$/.test(clean) ? clean : `${clean}/v1`;
}

export function createKeiRouterFetch(opts: { apiKey: string; baseURL: string }): typeof fetch {
  const root = opts.baseURL.replace(/\/+$/, "");
  return async (input, init) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const headers = new Headers(init?.headers ?? (typeof input === "string" || input instanceof URL ? undefined : input.headers));
    if (isSameBaseURL(url, root)) headers.set("Authorization", `Bearer ${opts.apiKey}`);
    return fetch(input as RequestInfo | URL, { ...init, headers });
  };
}

export const defaultReadAuthJson: KeiRouterReadAuthJson = async () => {
  const candidates = authJsonCandidates();
  let lastErr: unknown;
  for (const file of candidates) {
    try {
      const raw = await readFile(file, "utf8");
      const parsed = JSON.parse(raw) as unknown;
      return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : null;
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code === "ENOENT") continue;
      lastErr = err;
    }
  }
  if (lastErr) throw lastErr;
  return undefined;
};

export function authJsonCandidates(): string[] {
  if (process.env.OPENCODE_DATA_DIR) return [path.join(process.env.OPENCODE_DATA_DIR, "auth.json")];
  const home = os.homedir();
  return [
    path.join(home, ".local/share/opencode/auth.json"),
    path.join(home, "Library/Application Support/opencode/auth.json"),
  ];
}

export function isSameBaseURL(rawURL: string, root: string): boolean {
  try {
    const url = new URL(rawURL);
    const base = new URL(root);
    const basePath = base.pathname.replace(/\/+$/, "");
    return (
      url.origin === base.origin &&
      (basePath === "" || url.pathname === basePath || url.pathname.startsWith(basePath + "/"))
    );
  } catch {
    return rawURL === root || rawURL.startsWith(root + "/") || rawURL.startsWith(root + "?");
  }
}

export const KeiRouterPlugin: Plugin = async (_input, options) => {
  const resolved = resolveKeiRouterPluginOptions(options as KeiRouterPluginOptions | undefined);
  const cache: KeiRouterFetchCache = new Map();
  return {
    auth: createKeiRouterAuthHook(resolved),
    provider: createKeiRouterProviderHook(resolved, { cache }),
    config: createKeiRouterConfigHook(resolved, { cache }),
  };
};

export default {
  id: "@keirouter/opencode-plugin",
  server: KeiRouterPlugin,
};

function apiKeyFromAuth(auth: unknown): string | undefined {
  if (!auth || typeof auth !== "object") return undefined;
  const typed = auth as { type?: unknown; key?: unknown };
  if (typed.type !== "api") return undefined;
  if (typeof typed.key === "string" && typed.key.length > 0) return typed.key;
  return undefined;
}

function modelsCacheKey(baseURL: string, apiKey: string): string {
  const keyHash = createHash("sha256").update(apiKey).digest("hex");
  return `${baseURL.replace(/\/+$/, "")}::${keyHash}`;
}

function isRawModel(value: unknown): value is KeiRouterRawModel {
  return Boolean(value && typeof value === "object" && typeof (value as { id?: unknown }).id === "string");
}

function normalizeModalities(values: unknown): string[] {
  if (!Array.isArray(values)) return ["text"];
  const out = values.filter((v): v is string => typeof v === "string" && v.length > 0);
  return out.length > 0 ? out : ["text"];
}

function cleanString(value: unknown): string | undefined {
  return typeof value === "string" && value.trim().length > 0 ? value.trim() : undefined;
}
