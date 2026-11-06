import assert from "node:assert/strict";
import test from "node:test";
import {
  buildModelMap,
  buildStaticProviderEntry,
  createKeiRouterAuthHook,
  createKeiRouterConfigHook,
  createKeiRouterProviderHook,
  defaultKeiRouterModelsFetcher,
  isSameBaseURL,
  mapRawModel,
  modelsURL,
  resolveKeiRouterPluginOptions,
  toOpenAICompatibleBaseURL,
  type KeiRouterRawModel,
} from "../src/index.ts";

const rawModels: KeiRouterRawModel[] = [
  {
    id: "claude/sonnet-4",
    name: "Claude Sonnet 4",
    context_length: 200_000,
    max_output_tokens: 8_192,
    capabilities: {
      attachment: true,
      reasoning: true,
      tool_calling: true,
      temperature: true,
    },
    input_modalities: ["text", "image"],
    output_modalities: ["text"],
  },
  {
    id: "fast-chain",
    owned_by: "combo",
    kind: "llm",
    name: "fast-chain",
  },
];

test("resolves defaults", () => {
  assert.deepEqual(resolveKeiRouterPluginOptions(), {
    providerId: "keirouter",
    displayName: "KeiRouter",
    baseURL: "http://127.0.0.1:20180",
    modelCacheTtl: 300_000,
  });
});

test("normalizes model and provider URLs", () => {
  assert.equal(modelsURL("http://localhost:8080"), "http://localhost:8080/v1/models");
  assert.equal(modelsURL("http://localhost:8080/v1"), "http://localhost:8080/v1/models");
  assert.equal(toOpenAICompatibleBaseURL("http://localhost:8080"), "http://localhost:8080/v1");
  assert.equal(toOpenAICompatibleBaseURL("http://localhost:8080/v1"), "http://localhost:8080/v1");
});

test("fetch auth prefix check is URL-safe", () => {
  assert.equal(isSameBaseURL("http://kei.example/v1/models", "http://kei.example"), true);
  assert.equal(isSameBaseURL("http://kei.example.evil/v1/models", "http://kei.example"), false);
  assert.equal(isSameBaseURL("http://kei.example-other/v1/models", "http://kei.example"), false);
});

test("maps raw /v1/models entries without combo synthesis", () => {
  assert.deepEqual(mapRawModel(rawModels[0]), {
    name: "Claude Sonnet 4",
    attachment: true,
    reasoning: true,
    tool_call: true,
    temperature: true,
    limit: { context: 200_000, output: 8_192 },
    modalities: { input: ["text", "image"], output: ["text"] },
  });

  assert.deepEqual(buildModelMap(rawModels), {
    "claude/sonnet-4": {
      name: "Claude Sonnet 4",
      attachment: true,
      reasoning: true,
      tool_call: true,
      temperature: true,
      limit: { context: 200_000, output: 8_192 },
      modalities: { input: ["text", "image"], output: ["text"] },
    },
    "keirouter/fast-chain": { name: "fast-chain" },
  });
});

test("provider hook fetches once then serves cache until ttl expires", async () => {
  let calls = 0;
  let now = 1_000;
  const hook = createKeiRouterProviderHook(
    { baseURL: "http://kei", modelCacheTtl: 100 },
    {
      now: () => now,
      fetcher: async () => {
        calls++;
        return rawModels;
      },
    }
  );

  const ctx = { auth: { type: "api", key: "kr-test" } };
  assert.equal(Object.keys(await hook.models?.({} as never, ctx as never)).length, 2);
  assert.equal(Object.keys(await hook.models?.({} as never, ctx as never)).length, 2);
  assert.equal(calls, 1);

  now = 1_101;
  await hook.models?.({} as never, ctx as never);
  assert.equal(calls, 2);
});

test("provider hook returns empty model map without api auth", async () => {
  let calls = 0;
  const hook = createKeiRouterProviderHook(undefined, {
    fetcher: async () => {
      calls++;
      return rawModels;
    },
  });
  assert.deepEqual(await hook.models?.({} as never, { auth: undefined } as never), {});
  assert.equal(calls, 0);
});

test("config hook shares same fetch/cache behavior", async () => {
  let calls = 0;
  let now = 10;
  const hook = createKeiRouterConfigHook(
    { baseURL: "http://kei", modelCacheTtl: 50 },
    {
      now: () => now,
      readAuthJson: async () => ({ keirouter: { type: "api", key: "kr-test" } }),
      fetcher: async () => {
        calls++;
        return rawModels;
      },
    }
  );
  const input = {};

  await hook(input as never);
  await hook(input as never);
  assert.equal(calls, 1);
  assert.deepEqual(input.provider?.keirouter.options, {
    baseURL: "http://kei/v1",
    apiKey: "kr-test",
  });
  assert.equal(Object.keys(input.provider?.keirouter.models ?? {}).length, 2);

  now = 61;
  await hook(input as never);
  assert.equal(calls, 2);
});

test("auth hook loads api key and baseURL", async () => {
  const hook = createKeiRouterAuthHook({ baseURL: "http://kei", providerId: "kr" });
  const loaded = await hook.loader?.(async () => ({ type: "api", key: "secret" }) as never, {} as never);
  assert.equal(loaded?.apiKey, "secret");
  assert.equal(loaded?.baseURL, "http://kei/v1");
  assert.equal(typeof loaded?.fetch, "function");
});

test("static provider entry uses OpenAI-compatible package", () => {
  const entry = buildStaticProviderEntry(
    rawModels,
    resolveKeiRouterPluginOptions({ baseURL: "http://kei" }),
    "http://kei",
    "secret"
  );
  assert.equal(entry.npm, "@ai-sdk/openai-compatible");
  assert.equal(entry.options.baseURL, "http://kei/v1");
  assert.equal(entry.models["keirouter/fast-chain"].name, "fast-chain");
});

test("default fetcher calls only /v1/models", async () => {
  const originalFetch = globalThis.fetch;
  const seen: string[] = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    seen.push(String(input));
    assert.equal((init?.headers as Record<string, string>).Authorization, "Bearer secret");
    return new Response(JSON.stringify({ object: "list", data: rawModels }), { status: 200 });
  }) as typeof fetch;
  try {
    const models = await defaultKeiRouterModelsFetcher("http://kei", "secret");
    assert.equal(models.length, 2);
    assert.deepEqual(seen, ["http://kei/v1/models"]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("reads the capability names KeiRouter's /v1/models sends", () => {
  // Shape of gateway modelEntry (backend/internal/gateway/models.go).
  const server = {
    id: "anthropic/claude-sonnet-4-5",
    object: "model",
    owned_by: "anthropic",
    provider: "anthropic",
    kind: "llm",
    capabilities: {
      vision: true,
      pdf: true,
      audio_input: false,
      tools: true,
      reasoning: true,
      context_window: 200_000,
      max_output: 64_000,
    },
  } as KeiRouterRawModel;

  const model = mapRawModel(server);
  assert.equal(model.tool_call, true);
  assert.deepEqual(model.limit, { context: 200_000, output: 64_000 });
  assert.deepEqual(model.modalities?.input, ["text", "image", "pdf"]);
});
