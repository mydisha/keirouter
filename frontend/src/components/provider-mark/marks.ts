import { SPRITE_IDS } from "./sprite-ids";

/**
 * Provider and tool ids that don't match a LobeHub file name. Several
 * providers resell another company's platform (Vertex, SageMaker, watsonx),
 * so they share that company's mark.
 */
const ALIASES: Record<string, string> = {
  "custom-openai": "openai",
  "custom-anthropic": "anthropic",
  "anthropic-m": "anthropic",
  "oai-cc": "openai",
  "oai-r": "openai",
  vertex: "vertexai",
  "vertex-partner": "vertexai",
  "gemini-cli": "geminicli",
  "ollama-local": "ollama",
  "cloudflare-ai": "workersai",
  byteplus: "bytedance",
  "mimo-free": "xiaomimimo",
  "xiaomi-mimo": "xiaomimimo",
  "xiaomi-tokenplan": "xiaomimimo",
  "kimi-coding": "kimi",
  moonshot: "moonshot",
  "grok-cli": "grok",
  "grok-web": "grok",
  glm: "zhipu",
  "glm-cn": "zhipu",
  zai: "zai",
  "minimax-cn": "minimax",
  alicode: "alibabacloud",
  "alicode-intl": "alibabacloud",
  dashscope: "alibabacloud",
  "volcengine-ark": "volcengine",
  "vercel-ai-gateway": "vercel",
  "opencode-go": "opencode",
  "perplexity-web": "perplexity",
  "perplexity-agent": "perplexity",
  siliconflow: "siliconcloud",
  lepton: "leptonai",
  "inference-net": "inference",
  "nous-research": "nousresearch",
  nlpcloud: "nplcloud",
  "aws-polly": "aws",
  bedrock: "bedrock",
  sagemaker: "aws",
  "amazon-nova": "nova",
  "google-tts": "google",
  "google-pse": "google",
  "fal-ai": "fal",
  "stability-ai": "stability",
  "black-forest-labs": "bfl",
  runwayml: "runway",
  topaz: "topazlabs",
  "voyage-ai": "voyage",
  "jina-ai": "jina",
  "jina-reader": "jina",
  "brave-search": "brave",
  watsonx: "ibm",
  "azure-ai": "azureai",
  databricks: "dbrx",
  "huggingface-router": "huggingface",
  codestral: "mistral",
  "meta-llama": "meta",
  "lambda-ai": "lambda",
  friendliai: "friendli",
  gmi: "gmicloud",
  "github-models": "github",
  "lm-studio": "lmstudio",
  triton: "nvidia",
  copilot: "githubcopilot",
  roo: "roocode",
  hermes: "hermesagent",
  kilo: "kilocode",
  "claude-code": "claudecode",
  "codex-cli": "codex",
  "cline-cli": "cline",
  "cursor-cli": "cursor",
  "opencode-cli": "opencode",
  "deepseek-tui": "deepseek",
  "qoder-cli": "qoder",
  "codebuddy-cli": "codebuddy",
};

/** The sprite symbol id for a provider or tool id, if a mark is vendored. */
export function markFor(id: string): string | undefined {
  const key = id.toLowerCase();
  const mark = ALIASES[key] ?? key;
  return SPRITE_IDS.has(mark) ? mark : undefined;
}

/** The id inside an icon path the gateway sends, e.g. "/providers/groq.png" → "groq". */
export function markKey(icon: string | undefined): string | undefined {
  if (!icon) return undefined;
  const base = icon.split("/").pop() ?? "";
  return base.replace(/\.[a-z]+$/i, "") || undefined;
}
