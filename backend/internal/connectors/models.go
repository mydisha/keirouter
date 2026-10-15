package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// ModelSpec describes a single model offered by a provider, tagged with the
// service kind it serves. It backs the per-kind discovery endpoints
// (GET /v1/models/<kind>) and model info lookups (GET /v1/models/info).
type ModelSpec struct {
	// ID is the model id the client passes (provider-local, no alias prefix).
	ID string `json:"id"`
	// Name is a human-friendly display name.
	Name string `json:"name"`
	// Kind is the service kind this model serves (defaults to LLM).
	Kind core.ServiceKind `json:"kind"`
	// Dimensions is the embedding vector width (embedding models only).
	Dimensions int `json:"dimensions,omitempty"`
}

// k tags a model with a non-LLM service kind.
func k(id, name string, kind core.ServiceKind) ModelSpec {
	return ModelSpec{ID: id, Name: name, Kind: kind}
}

// m tags an LLM model.
func m(id, name string) ModelSpec { return ModelSpec{ID: id, Name: name, Kind: core.ServiceLLM} }

// emb tags an embedding model with its dimension count.
func emb(id, name string, dims int) ModelSpec {
	return ModelSpec{ID: id, Name: name, Kind: core.ServiceEmbedding, Dimensions: dims}
}

// providerModels maps a provider id to the curated set of models it offers.
// Providers marked passthrough upstream (openrouter, vercel, ...) accept any
// model id, so their listed set here is only a discovery hint, not an allow-list.
var providerModels = map[string][]ModelSpec{
	"openai": {
		m("gpt-6.1-sol", "GPT-6.1 Sol"), m("gpt-6-astra", "GPT-6 Astra"), m("gpt-6-sol", "GPT-6 Sol"),
		m("gpt-6-luna", "GPT-6 Luna"),
		m("gpt-5.6-sol", "GPT-5.6 Sol"), m("gpt-5.6-terra", "GPT-5.6 Terra"), m("gpt-5.6-luna", "GPT-5.6 Luna"),
		m("gpt-5.5", "GPT-5.5"), m("gpt-5.5-pro", "GPT-5.5 Pro"),
		m("gpt-5.4", "GPT-5.4"), m("gpt-5.4-pro", "GPT-5.4 Pro"), m("gpt-5.4-mini", "GPT-5.4 Mini"),
		m("gpt-5.4-nano", "GPT-5.4 Nano"), m("gpt-5.2", "GPT-5.2"), m("gpt-5.1", "GPT-5.1"),
		m("gpt-5", "GPT-5"), m("gpt-5-mini", "GPT-5 Mini"), m("gpt-4o", "GPT-4o"),
		m("gpt-4o-mini", "GPT-4o Mini"), m("gpt-4.1", "GPT-4.1"), m("gpt-4.1-mini", "GPT-4.1 Mini"),
		m("gpt-4.1-nano", "GPT-4.1 Nano"), m("o3-pro", "o3 Pro"), m("o3", "o3"), m("o4-mini", "o4 Mini"),
		m("o3-mini", "o3 Mini"), m("o1", "o1"),
		emb("text-embedding-3-large", "Text Embedding 3 Large", 3072),
		emb("text-embedding-3-small", "Text Embedding 3 Small", 1536),
		emb("text-embedding-ada-002", "Text Embedding Ada 002", 1536),
		k("tts-1", "TTS-1", core.ServiceTTS), k("tts-1-hd", "TTS-1 HD", core.ServiceTTS),
		k("gpt-4o-mini-tts", "GPT-4o Mini TTS", core.ServiceTTS),
		k("whisper-1", "Whisper 1", core.ServiceSTT), k("gpt-4o-transcribe", "GPT-4o Transcribe", core.ServiceSTT),
		k("gpt-4o-mini-transcribe", "GPT-4o Mini Transcribe", core.ServiceSTT),
		k("gpt-image-2", "GPT Image 2", core.ServiceImage), k("gpt-image-1.5", "GPT Image 1.5", core.ServiceImage),
		k("gpt-image-1", "GPT Image 1", core.ServiceImage), k("gpt-image-1-mini", "GPT Image 1 Mini", core.ServiceImage),
		k("dall-e-3", "DALL-E 3", core.ServiceImage),
		k("dall-e-2", "DALL-E 2", core.ServiceImage),
	},
	"codex": {
		m("gpt-5.6-sol", "GPT-5.6 Sol"), m("gpt-5.6-terra", "GPT-5.6 Terra"), m("gpt-5.6-luna", "GPT-5.6 Luna"),
		m("gpt-5.5", "GPT-5.5"), m("gpt-5.4", "GPT-5.4"), m("gpt-5.4-mini", "GPT-5.4 Mini"),
		m("gpt-5.2", "GPT-5.2"), m("gpt-5.1", "GPT-5.1"),
		m("gpt-5.3-codex", "GPT-5.3 Codex"), m("gpt-5.3-codex-xhigh", "GPT-5.3 Codex (xHigh)"),
		m("gpt-5.3-codex-high", "GPT-5.3 Codex (High)"), m("gpt-5.3-codex-low", "GPT-5.3 Codex (Low)"),
		m("gpt-5.3-codex-none", "GPT-5.3 Codex (None)"), m("gpt-5.3-codex-spark", "GPT-5.3 Codex (Spark)"),
		m("gpt-5.2-codex", "GPT-5.2 Codex"), m("gpt-5.1-codex-max", "GPT-5.1 Codex Max"),
		m("gpt-5.1-codex", "GPT-5.1 Codex"), m("gpt-5.1-codex-mini", "GPT-5.1 Codex Mini"),
		m("gpt-5.1-codex-mini-high", "GPT-5.1 Codex Mini (High)"),
		m("gpt-5-codex", "GPT-5 Codex"), m("gpt-5-codex-mini", "GPT-5 Codex Mini"),
		k("gpt-5.5-image", "GPT-5.5 Image", core.ServiceImage), k("gpt-5.4-image", "GPT-5.4 Image", core.ServiceImage),
		k("gpt-5.3-image", "GPT-5.3 Image", core.ServiceImage), k("gpt-5.2-image", "GPT-5.2 Image", core.ServiceImage),
	},
	"anthropic": {
		m("claude-fable-5-1", "Claude Fable 5.1"), m("claude-mythos-5-1", "Claude Mythos 5.1"),
		m("claude-opus-5-5", "Claude Opus 5.5"), m("claude-sonnet-5-5", "Claude Sonnet 5.5"),
		m("claude-haiku-5-5", "Claude Haiku 5.5"), m("claude-opus-5", "Claude Opus 5"),
		m("claude-sonnet-5", "Claude Sonnet 5"), m("claude-opus-4-8", "Claude Opus 4.8"),
		m("claude-opus-4-7", "Claude Opus 4.7"), m("claude-opus-4-6", "Claude Opus 4.6"),
		m("claude-sonnet-4-6", "Claude Sonnet 4.6"),
		m("claude-opus-4-5-20251101", "Claude Opus 4.5"),
		m("claude-sonnet-4-5-20250929", "Claude Sonnet 4.5"),
		m("claude-haiku-4-5-20251001", "Claude Haiku 4.5"),
		m("claude-sonnet-4-20250514", "Claude Sonnet 4"),
		m("claude-opus-4-20250514", "Claude Opus 4"),
		m("claude-3-5-sonnet-20241022", "Claude 3.5 Sonnet"),
		k("claude-sonnet-5-5", "Claude Sonnet 5.5 (Vision)", core.ServiceImageToText),
		k("claude-opus-4-5-20251101", "Claude Opus 4.5 (Vision)", core.ServiceImageToText),
	},
	"deepseek": {
		m("deepseek-v4-pro", "DeepSeek V4 Pro"), m("deepseek-v4-pro-max", "DeepSeek V4 Pro Max"),
		m("deepseek-v4-pro-none", "DeepSeek V4 Pro No Thinking"), m("deepseek-v4-flash", "DeepSeek V4 Flash"),
		m("deepseek-chat", "DeepSeek V3.2 Chat"), m("deepseek-reasoner", "DeepSeek V3.2 Reasoner"),
	},
	"glm": {
		m("glm-5.3", "GLM 5.3"), m("glm-5.3-flash", "GLM 5.3 Flash"), m("glm-5.2", "GLM 5.2"),
		m("glm-5.1", "GLM 5.1"), m("glm-5-code", "GLM 5 Code"), m("glm-5", "GLM 5"), m("glm-4.7", "GLM 4.7"),
		m("glm-4.6v", "GLM 4.6V (Vision)"),
	},
	"glm-cn": {m("glm-5.1", "GLM 5.1"), m("glm-5", "GLM 5"), m("glm-4.7", "GLM-4.7"), m("glm-4.6", "GLM-4.6"), m("glm-4.5-air", "GLM-4.5-Air")},
	"kimi":   {m("kimi-k3", "Kimi K3"), m("kimi-k2.7-code", "Kimi K2.7 Code"), m("kimi-k2.6", "Kimi K2.6"), m("kimi-k2.5", "Kimi K2.5"), m("kimi-k2.5-thinking", "Kimi K2.5 Thinking"), m("kimi-latest", "Kimi Latest")},
	"minimax": {
		m("MiniMax-M3", "MiniMax M3"),
		m("MiniMax-M2.7", "MiniMax M2.7"), m("MiniMax-M2.5", "MiniMax M2.5"), m("MiniMax-M2.1", "MiniMax M2.1"),
		k("minimax-image-01", "MiniMax Image 01", core.ServiceImage),
		k("MiniMax-VL-01", "MiniMax VL 01 (Vision)", core.ServiceImageToText),
	},
	"minimax-cn": {m("MiniMax-M2.7", "MiniMax M2.7"), m("MiniMax-M2.5", "MiniMax M2.5"), m("MiniMax-M2.1", "MiniMax M2.1")},
	"groq": {
		m("llama-3.3-70b-versatile", "Llama 3.3 70B"), m("meta-llama/llama-4-maverick-17b-128e-instruct", "Llama 4 Maverick"),
		m("qwen/qwen3.8-27b", "Qwen3.8 27B"), m("qwen/qwen3-32b", "Qwen3 32B"),
		m("openai/gpt-oss-120b", "GPT-OSS 120B"), m("openai/gpt-oss-20b", "GPT-OSS 20B"),
		k("whisper-large-v3", "Whisper Large v3", core.ServiceSTT),
		k("whisper-large-v3-turbo", "Whisper Large v3 Turbo", core.ServiceSTT),
		k("distil-whisper-large-v3-en", "Distil Whisper Large v3 EN", core.ServiceSTT),
		k("meta-llama/llama-4-maverick-17b-128e-instruct", "Llama 4 Maverick (Vision)", core.ServiceImageToText),
	},
	"xai": {
		m("grok-4.7", "Grok 4.7"), m("grok-4.6", "Grok 4.6"), m("grok-4.5", "Grok 4.5"),
		m("grok-4.3", "Grok 4.3"), m("grok-4.20-reasoning", "Grok 4.20 Reasoning"),
		m("grok-4.20-non-reasoning", "Grok 4.20 Non-Reasoning"), m("grok-4.20-multi-agent", "Grok 4.20 Multi-Agent"),
		m("grok-build-latest", "Grok Build"),
		m("grok-4", "Grok 4"), m("grok-4-fast-reasoning", "Grok 4 Fast Reasoning"),
		m("grok-code-fast-1", "Grok Code Fast"), m("grok-3", "Grok 3"),
		k("grok-2-image-1212", "Grok 2 Image", core.ServiceImage),
		k("grok-2-vision-1212", "Grok 2 Vision", core.ServiceImageToText),
		k("grok-imagine-video", "Grok Imagine Video", core.ServiceVideo),
	},
	"mistral": {
		m("mistral-large-4", "Mistral Large 4"), m("mistral-large-latest", "Mistral Large 3"),
		m("mistral-medium-3.5", "Mistral Medium 3.5"), m("mistral-medium-latest", "Mistral Medium"),
		m("mistral-small-latest", "Mistral Small"), m("magistral-medium-latest", "Magistral Medium"),
		m("devstral-latest", "Devstral"), m("codestral-latest", "Codestral"),
		m("ministral-14b-latest", "Ministral 14B"), emb("mistral-embed", "Mistral Embed", 1024),
		k("voxtral-small-latest", "Voxtral Small (Audio)", core.ServiceSTT),
		k("pixtral-large-latest", "Pixtral Large (Vision)", core.ServiceImageToText),
	},
	"perplexity": {
		m("sonar-pro", "Sonar Pro"), m("sonar", "Sonar"), m("sonar-reasoning-pro", "Sonar Reasoning Pro"),
		m("sonar-deep-research", "Sonar Deep Research"),
	},
	"together": {
		m("meta-llama/Llama-3.3-70B-Instruct-Turbo", "Llama 3.3 70B Turbo"),
		m("deepseek-ai/DeepSeek-R1", "DeepSeek R1"), m("Qwen/Qwen3-235B-A22B", "Qwen3 235B"),
		m("MiniMaxAI/MiniMax-M3", "MiniMax M3"),
		emb("BAAI/bge-large-en-v1.5", "BGE Large EN v1.5", 1024),
		emb("togethercomputer/m2-bert-80M-8k-retrieval", "M2 BERT 80M 8K", 768),
	},
	"fireworks": {
		m("accounts/fireworks/models/kimi-k3", "Kimi K3"),
		m("accounts/fireworks/models/glm-5p3", "GLM 5.3"),
		m("accounts/fireworks/models/minimax-m3", "MiniMax M3"),
		m("accounts/fireworks/models/qwen3p8-max", "Qwen3.8 Max"),
		m("accounts/fireworks/models/deepseek-v3p1", "DeepSeek V3.1"),
		m("accounts/fireworks/models/llama-v3p3-70b-instruct", "Llama 3.3 70B"),
		m("accounts/fireworks/models/qwen3-235b-a22b", "Qwen3 235B"),
		emb("nomic-ai/nomic-embed-text-v1.5", "Nomic Embed Text v1.5", 768),
	},
	"cerebras": {
		m("gpt-oss-120b", "GPT OSS 120B"), m("zai-glm-4.7", "ZAI GLM 4.7"),
		m("llama-3.3-70b", "Llama 3.3 70B"), m("qwen-3-235b-a22b-instruct-2507", "Qwen3 235B A22B"),
		m("qwen-3-32b", "Qwen3 32B"), m("qwen-3.8-27b", "Qwen3.8 27B"), m("gemma-4-31b", "Gemma 4 31B"),
	},
	"cohere": {m("command-a-03-2025", "Command A"), m("command-r-plus-08-2024", "Command R+"), m("command-r-08-2024", "Command R")},
	"nvidia": {
		m("minimaxai/minimax-m2.7", "MiniMax M2.7"), m("minimaxai/minimax-m3", "MiniMax M3"),
		m("z-ai/glm-5.2", "GLM 5.2"), m("z-ai/glm4.7", "GLM 4.7"),
		m("deepseek-ai/deepseek-v4-pro", "DeepSeek V4 Pro"), m("deepseek-ai/deepseek-v4-flash", "DeepSeek V4 Flash"),
		m("moonshotai/kimi-k2.6", "Kimi K2.6"),
		m("nvidia/nemotron-3-ultra-550b-a55b", "Nemotron 3 Ultra"),
		emb("nvidia/nv-embedqa-e5-v5", "NV EmbedQA E5 v5", 1024),
		k("nvidia/parakeet-ctc-1.1b-asr", "Parakeet CTC 1.1B", core.ServiceSTT),
		k("fastpitch", "FastPitch", core.ServiceTTS),
		k("tacotron2", "Tacotron2", core.ServiceTTS),
	},
	"nebius": {m("meta-llama/Llama-3.3-70B-Instruct", "Llama 3.3 70B Instruct"), emb("Qwen/Qwen3-Embedding-8B", "Qwen3 Embedding 8B", 4096)},
	"siliconflow": {
		m("deepseek-ai/DeepSeek-V3.2", "DeepSeek V3.2"), m("Qwen/Qwen3-235B-A22B-Instruct-2507", "Qwen3 235B"),
		m("moonshotai/Kimi-K2.5", "Kimi K2.5"), m("zai-org/GLM-4.7", "GLM 4.7"), m("openai/gpt-oss-120b", "GPT OSS 120B"),
	},
	"hyperbolic": {
		m("Qwen/QwQ-32B", "QwQ 32B"), m("deepseek-ai/DeepSeek-R1", "DeepSeek R1"),
		m("meta-llama/Llama-3.3-70B-Instruct", "Llama 3.3 70B"),
	},
	"blackbox": {
		m("gpt-4o", "GPT-4o"), m("claude-sonnet-4.6", "Claude Sonnet 4.6"),
		m("deepseek-chat", "DeepSeek Chat"), m("deepseek-r1", "DeepSeek R1"),
		m("gemini-3-flash-preview", "Gemini 3 Flash Preview"), m("qwen3-max", "Qwen3 Max"),
	},
	"chutes": {m("deepseek-ai/DeepSeek-V3.2", "DeepSeek V3.2"), m("Qwen/Qwen3-235B", "Qwen3 235B")},
	"alicode": {
		m("qwen3.5-plus", "Qwen3.5 Plus"), m("kimi-k2.5", "Kimi K2.5"), m("glm-5", "GLM 5"),
		m("qwen3-coder-plus", "Qwen3 Coder Plus"),
	},
	"alicode-intl": {m("qwen3.5-plus", "Qwen3.5 Plus"), m("kimi-k2.5", "Kimi K2.5"), m("glm-5", "GLM 5"), m("qwen3-coder-plus", "Qwen3 Coder Plus")},
	"mimo-free":    {m("mimo-auto", "MiMo Auto")},
	"xiaomi-mimo":  {m("mimo-v2.6-pro", "MiMo V2.6 Pro"), m("mimo-v2.6-flash", "MiMo V2.6 Flash"), m("mimo-v2.5-pro", "MiMo V2.5 Pro"), m("mimo-v2.5", "MiMo V2.5"), m("mimo-v2-omni", "MiMo V2 Omni"), m("mimo-v2-flash", "MiMo V2 Flash")},
	"xiaomi-tokenplan": {
		m("mimo-v2.5-pro", "MiMo V2.5 Pro"), m("mimo-v2.5", "MiMo V2.5"), m("mimo-v2-pro", "MiMo V2 Pro"), m("mimo-v2-omni", "MiMo V2 Omni"),
		k("mimo-v2-tts", "MiMo V2 TTS", core.ServiceTTS), k("mimo-v2.5-tts", "MiMo V2.5 TTS", core.ServiceTTS),
		k("mimo-v2.5-tts-voiceclone", "MiMo V2.5 TTS Voice Clone", core.ServiceTTS), k("mimo-v2.5-tts-voicedesign", "MiMo V2.5 TTS Voice Design", core.ServiceTTS),
	},
	"volcengine-ark": {
		m("Doubao-Seed-2.0-pro", "Doubao Seed 2.0 Pro"), m("DeepSeek-V4-Pro", "DeepSeek V4 Pro"),
		m("GLM-5.1", "GLM 5.1"), m("Kimi-K2.6", "Kimi K2.6"),
	},
	"commandcode": {
		m("deepseek/deepseek-v4-pro", "DeepSeek V4 Pro"),
		m("deepseek/deepseek-v4-flash", "DeepSeek V4 Flash"),
		m("moonshotai/Kimi-K2.6", "Kimi K2.6"),
		m("moonshotai/Kimi-K2.5", "Kimi K2.5"),
		m("zai-org/GLM-5.1", "GLM 5.1"),
		m("zai-org/GLM-5", "GLM 5"),
		m("MiniMaxAI/MiniMax-M2.7", "MiniMax M2.7"),
		m("MiniMaxAI/MiniMax-M2.5", "MiniMax M2.5"),
		m("Qwen/Qwen3.6-Max-Preview", "Qwen 3.6 Max Preview"),
		m("Qwen/Qwen3.6-Plus", "Qwen 3.6 Plus"),
		m("stepfun/Step-3.5-Flash", "Step 3.5 Flash"),
	},
	"byteplus": {m("seed-2-0-pro-260328", "Seed 2.0 Pro"), m("kimi-k2-thinking-251104", "Kimi K2 Thinking"), m("glm-4-7-251222", "GLM 4.7")},
	"cloudflare-ai": {
		// Llama family
		m("@cf/meta/llama-3.2-1b-instruct", "Llama 3.2 1B Instruct"),
		m("@cf/meta/llama-3.2-3b-instruct", "Llama 3.2 3B Instruct"),
		m("@cf/meta/llama-3.1-8b-instruct-fp8-fast", "Llama 3.1 8B Instruct (Fast)"),
		m("@cf/meta/llama-3.1-8b-instruct-awq", "Llama 3.1 8B Instruct (AWQ)"),
		m("@cf/meta/llama-3.1-70b-instruct-fp8-fast", "Llama 3.1 70B Instruct (Fast)"),
		m("@cf/meta/llama-3.3-70b-instruct-fp8-fast", "Llama 3.3 70B Instruct (Fast)"),
		// Mistral
		m("@cf/mistralai/mistral-small-3.1-24b-instruct", "Mistral Small 3.1 24B"),
		// DeepSeek
		m("@cf/deepseek-ai/deepseek-r1-distill-qwen-32b", "DeepSeek R1 Distill Qwen 32B"),
		// Kimi
		m("@cf/moonshotai/kimi-k2.7-code", "Kimi K2.7 Code"),
		m("@cf/moonshotai/kimi-k2.6", "Kimi K2.6"),
		m("@cf/moonshotai/kimi-k2.5", "Kimi K2.5"),
		// OpenAI open-weight
		m("@cf/openai/gpt-oss-120b", "GPT-OSS 120B"),
		m("@cf/openai/gpt-oss-20b", "GPT-OSS 20B"),
		// Google / NVIDIA
		m("@cf/google/gemma-4-26b-a4b-it", "Gemma 4 26B A4B"),
		m("@cf/nvidia/nemotron-3-120b-a12b", "Nemotron 3 120B A12B"),
		// GLM
		m("@cf/zai-org/glm-5.2", "GLM 5.2"),
		m("@cf/zai-org/glm-4.7-flash", "GLM 4.7 Flash"),
		m("@cf/zai-org/glm-5.3-flash", "GLM 5.3 Flash"),
		// Qwen
		m("@cf/qwen/qwq-32b", "QwQ 32B"),
		m("@cf/qwen/qwen2.5-coder-32b-instruct", "Qwen 2.5 Coder 32B"),
		m("@cf/qwen/qwen3-30b-a3b-fp8", "Qwen3 30B A3B"),
		// Image models
		k("@cf/black-forest-labs/flux-1-schnell", "FLUX.1 Schnell", core.ServiceImage),
		k("@cf/stabilityai/stable-diffusion-xl-base-1.0", "Stable Diffusion XL", core.ServiceImage),
	},
	"kiro": {
		m("auto", "Kiro Auto"), m("auto-thinking", "Kiro Auto (Thinking)"),
		m("claude-sonnet-5", "Kiro Claude Sonnet 5"), m("claude-sonnet-5-thinking", "Kiro Claude Sonnet 5 (Thinking)"),
		m("claude-sonnet-5-agentic", "Kiro Claude Sonnet 5 (Agentic)"), m("claude-sonnet-5-thinking-agentic", "Kiro Claude Sonnet 5 (Thinking + Agentic)"),
		m("claude-sonnet-4.5", "Kiro Claude Sonnet 4.5"), m("claude-sonnet-4.5-thinking", "Kiro Claude Sonnet 4.5 (Thinking)"),
		m("claude-sonnet-4.5-agentic", "Kiro Claude Sonnet 4.5 (Agentic)"), m("claude-sonnet-4.5-thinking-agentic", "Kiro Claude Sonnet 4.5 (Thinking + Agentic)"),
		m("claude-haiku-4.5", "Kiro Claude Haiku 4.5"), m("claude-haiku-4.5-thinking", "Kiro Claude Haiku 4.5 (Thinking)"),
		m("claude-haiku-4.5-agentic", "Kiro Claude Haiku 4.5 (Agentic)"), m("claude-haiku-4.5-thinking-agentic", "Kiro Claude Haiku 4.5 (Thinking + Agentic)"),
		m("claude-sonnet-4.6", "Kiro Claude Sonnet 4.6"), m("claude-sonnet-4.6-thinking", "Kiro Claude Sonnet 4.6 (Thinking)"),
		m("claude-sonnet-4.6-agentic", "Kiro Claude Sonnet 4.6 (Agentic)"), m("claude-sonnet-4.6-thinking-agentic", "Kiro Claude Sonnet 4.6 (Thinking + Agentic)"),
		m("claude-opus-4.5", "Kiro Claude Opus 4.5"), m("claude-opus-4.5-thinking", "Kiro Claude Opus 4.5 (Thinking)"),
		m("claude-opus-4.5-agentic", "Kiro Claude Opus 4.5 (Agentic)"), m("claude-opus-4.5-thinking-agentic", "Kiro Claude Opus 4.5 (Thinking + Agentic)"),
		m("claude-opus-4.6", "Kiro Claude Opus 4.6"), m("claude-opus-4.6-thinking", "Kiro Claude Opus 4.6 (Thinking)"),
		m("claude-opus-4.6-agentic", "Kiro Claude Opus 4.6 (Agentic)"), m("claude-opus-4.6-thinking-agentic", "Kiro Claude Opus 4.6 (Thinking + Agentic)"),
		m("claude-sonnet-4.7", "Kiro Claude Sonnet 4.7"), m("claude-sonnet-4.7-thinking", "Kiro Claude Sonnet 4.7 (Thinking)"),
		m("claude-sonnet-4.7-agentic", "Kiro Claude Sonnet 4.7 (Agentic)"), m("claude-sonnet-4.7-thinking-agentic", "Kiro Claude Sonnet 4.7 (Thinking + Agentic)"),
		m("claude-opus-4.7", "Kiro Claude Opus 4.7"), m("claude-opus-4.7-thinking", "Kiro Claude Opus 4.7 (Thinking)"),
		m("claude-opus-4.7-agentic", "Kiro Claude Opus 4.7 (Agentic)"), m("claude-opus-4.7-thinking-agentic", "Kiro Claude Opus 4.7 (Thinking + Agentic)"),
		m("claude-sonnet-4.8", "Kiro Claude Sonnet 4.8"), m("claude-sonnet-4.8-thinking", "Kiro Claude Sonnet 4.8 (Thinking)"),
		m("claude-sonnet-4.8-agentic", "Kiro Claude Sonnet 4.8 (Agentic)"), m("claude-sonnet-4.8-thinking-agentic", "Kiro Claude Sonnet 4.8 (Thinking + Agentic)"),
		m("claude-opus-4.8", "Kiro Claude Opus 4.8"), m("claude-opus-4.8-thinking", "Kiro Claude Opus 4.8 (Thinking)"),
		m("claude-opus-4.8-agentic", "Kiro Claude Opus 4.8 (Agentic)"), m("claude-opus-4.8-thinking-agentic", "Kiro Claude Opus 4.8 (Thinking + Agentic)"),
		m("gpt-5.6-sol", "Kiro GPT 5.6 Sol"), m("gpt-5.6-sol-thinking", "Kiro GPT 5.6 Sol (Thinking)"),
		m("gpt-5.6-sol-agentic", "Kiro GPT 5.6 Sol (Agentic)"), m("gpt-5.6-sol-thinking-agentic", "Kiro GPT 5.6 Sol (Thinking + Agentic)"),
		m("gpt-5.6-terra", "Kiro GPT 5.6 Terra"), m("gpt-5.6-terra-thinking", "Kiro GPT 5.6 Terra (Thinking)"),
		m("gpt-5.6-terra-agentic", "Kiro GPT 5.6 Terra (Agentic)"), m("gpt-5.6-terra-thinking-agentic", "Kiro GPT 5.6 Terra (Thinking + Agentic)"),
		m("gpt-5.6-luna", "Kiro GPT 5.6 Luna"), m("gpt-5.6-luna-thinking", "Kiro GPT 5.6 Luna (Thinking)"),
		m("gpt-5.6-luna-agentic", "Kiro GPT 5.6 Luna (Agentic)"), m("gpt-5.6-luna-thinking-agentic", "Kiro GPT 5.6 Luna (Thinking + Agentic)"),
		m("deepseek-3.2", "Kiro DeepSeek 3.2"), m("qwen3-coder-next", "Kiro Qwen3 Coder Next"),
		m("glm-5", "Kiro GLM 5"), m("MiniMax-M2.5", "Kiro MiniMax M2.5"),
	},
	"qoder": {
		m("auto", "Auto"), m("ultimate", "Ultimate"), m("performance", "Performance"),
		m("efficient", "Efficient"), m("lite", "Lite"),
		m("qmodel", "Q Model"), m("qmodel_latest", "Q Model (Latest)"),
		m("dmodel", "D Model"), m("dfmodel", "DF Model"),
		m("gm51model", "GM 5.1 Model"), m("kmodel", "K Model"), m("mmodel", "M Model"),
	},
	"gemini-cli": {
		m("gemini-3-flash-preview", "Gemini 3 Flash Preview"),
		m("gemini-3-pro-preview", "Gemini 3 Pro Preview"),
	},
	"antigravity": {
		m("claude-opus-4-6-thinking", "Claude Opus 4.6 (Thinking)"),
		m("claude-sonnet-4-6", "Claude Sonnet 4.6"),
		m("gemini-3.7-flash-low", "Gemini 3.7 Flash (Low)"),
		m("gemini-3.7-flash-medium", "Gemini 3.7 Flash (Medium)"),
		m("gemini-3.7-flash-high", "Gemini 3.7 Flash (High)"),
		m("gemini-3.5-flash-low", "Gemini 3.5 Flash (Low)"),
		m("gemini-3.5-flash-medium", "Gemini 3.5 Flash (Medium)"),
		m("gemini-3.5-flash-high", "Gemini 3.5 Flash (High)"),
		m("gemini-3.1-pro-high", "Gemini 3.1 Pro (High)"),
		m("gemini-3.1-pro-low", "Gemini 3.1 Pro (Low)"),
		m("gemini-3.1-flash-lite", "Gemini 3.1 Flash Lite"),
		m("gpt-oss-120b-medium", "GPT-OSS 120B (Medium)"),
	},
	"cursor": {
		m("default", "Auto (Server Picks)"),
		m("claude-4.5-opus-high-thinking", "Claude 4.5 Opus High Thinking"),
		m("claude-4.5-opus-high", "Claude 4.5 Opus High"),
		m("claude-4.5-sonnet-thinking", "Claude 4.5 Sonnet Thinking"),
		m("claude-4.5-sonnet", "Claude 4.5 Sonnet"),
		m("claude-4.5-haiku", "Claude 4.5 Haiku"),
		m("claude-4.5-opus", "Claude 4.5 Opus"),
		m("gpt-5.2-codex", "GPT 5.2 Codex"),
		m("claude-4.6-opus-max", "Claude 4.6 Opus Max"),
		m("claude-4.6-sonnet-medium-thinking", "Claude 4.6 Sonnet Medium Thinking"),
		m("kimi-k2.5", "Kimi K2.5"), m("gemini-3-flash-preview", "Gemini 3 Flash Preview"),
		m("gpt-5.2", "GPT 5.2"), m("gpt-5.3-codex", "GPT 5.3 Codex"),
	},
	"kilocode": {
		m("anthropic/claude-sonnet-4-20250514", "Claude Sonnet 4"),
		m("anthropic/claude-opus-4-20250514", "Claude Opus 4"),
		m("google/gemini-2.5-pro", "Gemini 2.5 Pro"),
		m("google/gemini-2.5-flash", "Gemini 2.5 Flash"),
		m("openai/gpt-4.1", "GPT-4.1"), m("openai/o3", "o3"),
		m("deepseek/deepseek-chat", "DeepSeek Chat"),
		m("deepseek/deepseek-reasoner", "DeepSeek Reasoner"),
	},
	"cline": {
		m("anthropic/claude-opus-4.7", "Claude Opus 4.7"),
		m("anthropic/claude-sonnet-4.6", "Claude Sonnet 4.6"),
		m("anthropic/claude-opus-4.6", "Claude Opus 4.6"),
		m("openai/gpt-5.3-codex", "GPT-5.3 Codex"),
		m("openai/gpt-5.4", "GPT-5.4"),
		m("google/gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview"),
		m("google/gemini-3.1-flash-lite-preview", "Gemini 3.1 Flash Lite Preview"),
		m("kwaipilot/kat-coder-pro", "KAT Coder Pro"),
	},
	"codebuddy": {
		m("glm-5.2", "GLM-5.2"), m("glm-5.1", "GLM-5.1"), m("glm-5.0", "GLM-5.0"),
		m("glm-5.0-turbo", "GLM-5.0-Turbo"), m("glm-5v-turbo", "GLM-5v-Turbo"), m("glm-4.7", "GLM-4.7"),
		m("minimax-m3", "MiniMax-M3"), m("minimax-m2.7", "MiniMax-M2.7"),
		m("kimi-k2.7", "Kimi-K2.7-Code"), m("kimi-k2.6", "Kimi-K2.6"), m("kimi-k2.5", "Kimi-K2.5"),
		m("hy3-preview", "Hy3 Preview"),
		m("deepseek-v4-pro", "DeepSeek-V4-Pro"), m("deepseek-v4-flash", "DeepSeek-V4-Flash"),
		m("deepseek-v3-2-volc", "DeepSeek-V3.2"),
	},
	"kimchi": {
		m("minimax-m3", "MiniMax-M3"), m("kimi-k2.7", "Kimi-K2.7"), m("kimi-k2.6", "Kimi-K2.6"),
		m("kimi-k2.5", "Kimi-K2.5"), m("nemotron-3-ultra-fp4", "Nemotron 3 Ultra FP4"),
		m("minimax-m2.7", "MiniMax-M2.7"), m("claude-opus-4-6", "Claude Opus 4.6"),
		m("claude-sonnet-4-6", "Claude Sonnet 4.6"),
		k("claude-opus-4-6", "Claude Opus 4.6 (Vision)", core.ServiceImageToText),
	},
	"clinepass": {
		m("cline-pass/glm-5.2", "GLM-5.2 (ClinePass)"),
		m("cline-pass/kimi-k2.7-code", "Kimi K2.7 Code (ClinePass)"),
		m("cline-pass/kimi-k2.6", "Kimi K2.6 (ClinePass)"),
		m("cline-pass/deepseek-v4-pro", "DeepSeek V4 Pro (ClinePass)"),
		m("cline-pass/deepseek-v4-flash", "DeepSeek V4 Flash (ClinePass)"),
		m("cline-pass/minimax-m3", "MiniMax M3 (ClinePass)"),
		m("cline-pass/qwen3.7-max", "Qwen3.7 Max (ClinePass)"),
		m("cline-pass/qwen3.7-plus", "Qwen3.7 Plus (ClinePass)"),
	},
	"grok-cli": {
		m("grok-build", "Grok Build"), m("grok-4.5", "Grok 4.5"),
		m("grok-4.5-high", "Grok 4.5 (High)"), m("grok-4.5-medium", "Grok 4.5 (Medium)"),
		m("grok-4.5-low", "Grok 4.5 (Low)"),
	},
	"venice": {
		m("venice-uncensored-1-2", "Venice Uncensored 1.2"), m("zai-org-glm-5", "GLM-5"),
		m("qwen3-235b-a22b-instruct-2507", "Qwen3 235B A22B Instruct"),
		m("qwen3-coder-480b-a35b-instruct-turbo", "Qwen3 Coder 480B A35B Turbo"),
		m("qwen3-vl-235b-a22b", "Qwen3 VL 235B A22B"), m("deepseek-v4-pro", "DeepSeek V4 Pro"),
		m("llama-3.3-70b", "Llama 3.3 70B"), m("hermes-3-llama-3.1-405b", "Hermes 3 Llama 3.1 405B"),
		m("mistral-small-3-2-24b-instruct", "Mistral Small 3.2 24B"),
		emb("text-embedding-3-large", "Text Embedding 3 Large", 3072),
		emb("text-embedding-bge-m3", "BGE-M3 Embedding", 1024),
		emb("text-embedding-qwen3-8b", "Qwen3 8B Embedding", 4096),
		k("venice-sd35", "Venice SD3.5", core.ServiceImage), k("flux-2-pro", "FLUX.2 Pro", core.ServiceImage),
		k("gpt-image-2", "GPT Image 2 (via Venice)", core.ServiceImage),
	},
	"featherless": {
		m("deepseek-ai/DeepSeek-V4-Pro", "DeepSeek V4 Pro"), m("deepseek-ai/DeepSeek-V4-Flash", "DeepSeek V4 Flash"),
		m("zai-org/GLM-5.2", "GLM 5.2"), m("zai-org/GLM-5.1", "GLM 5.1"),
		m("moonshotai/Kimi-K2.7-Code", "Kimi K2.7 Code"), m("moonshotai/Kimi-K2.6", "Kimi K2.6"),
		m("moonshotai/Kimi-K2.5", "Kimi K2.5"),
	},
	"perplexity-agent": {
		m("perplexity/sonar", "Perplexity Sonar"), m("openai/gpt-5.5", "GPT-5.5"),
		m("openai/gpt-5.4", "GPT-5.4"), m("openai/gpt-5.4-mini", "GPT-5.4 Mini"),
		m("anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6"), m("anthropic/claude-opus-4-8", "Claude Opus 4.8"),
		m("google/gemini-3.1-pro-preview", "Gemini 3.1 Pro"), m("xai/grok-4.20-reasoning", "Grok 4.20 Reasoning"),
		m("perplexity/glm-5.2", "GLM 5.2"), m("perplexity/kimi-k2.7-code", "Kimi K2.7 Code"),
		m("nvidia/nemotron-3-super-120b-a12b", "Nemotron 3 Super 120B"),
	},
	"mmf": {m("mimo-auto", "MiMo Auto")},

	// Passthrough providers: expose representative media-tagged models so the
	// media catalog can surface their image-understanding / video routes.
	"openrouter": {
		k("openai/gpt-4o", "GPT-4o (Vision)", core.ServiceImageToText),
		k("anthropic/claude-opus-4.5", "Claude Opus 4.5 (Vision)", core.ServiceImageToText),
	},
	"vertex": {
		k("gemini-2.5-pro", "Gemini 2.5 Pro (Vision)", core.ServiceImageToText),
	},
	"vercel-ai-gateway": {
		k("openai/gpt-4o", "GPT-4o (Vision)", core.ServiceImageToText),
	},
	"opencode-go":  {m("kimi-k2.6", "Kimi K2.6"), m("glm-5.1", "GLM 5.1"), m("qwen3.6-plus", "Qwen 3.6 Plus")},
	"ollama":       {m("gpt-oss:120b", "GPT OSS 120B"), m("kimi-k2.5", "Kimi K2.5"), m("glm-5", "GLM 5"), m("qwen3.5", "Qwen3.5")},
	"ollama-local": {m("llama3.2", "Llama 3.2"), m("qwen2.5-coder", "Qwen 2.5 Coder")},
	"gemini": {
		m("gemini-pro-latest", "Gemini Pro (Latest)"), m("gemini-flash-latest", "Gemini Flash (Latest)"),
		m("gemini-3.8-flash", "Gemini 3.8 Flash"), m("gemini-3.7-flash", "Gemini 3.7 Flash"),
		m("gemini-3.6-flash", "Gemini 3.6 Flash"), m("gemini-3.5-flash", "Gemini 3.5 Flash"),
		m("gemini-3.5-flash-lite", "Gemini 3.5 Flash Lite"), m("gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview"),
		m("gemini-3.1-flash-lite", "Gemini 3.1 Flash Lite"), m("gemini-3-flash-preview", "Gemini 3 Flash Preview"),
		m("gemma-4-31b-it", "Gemma 4 31B"), m("gemini-2.5-pro", "Gemini 2.5 Pro"), m("gemini-2.5-flash", "Gemini 2.5 Flash"),
		emb("gemini-embedding-001", "Gemini Embedding 001", 768), emb("text-embedding-004", "Text Embedding 004", 768),
		k("gemini-2.5-flash-image", "Gemini 2.5 Flash Image (Nano Banana)", core.ServiceImage),
		k("gemini-2.5-pro-preview-tts", "Gemini 2.5 Pro TTS", core.ServiceTTS),
		k("gemini-2.5-pro", "Gemini 2.5 Pro (Vision)", core.ServiceImageToText),
	},
	// Media providers.
	"deepgram":   {k("nova-3", "Nova 3", core.ServiceSTT), k("nova-2", "Nova 2", core.ServiceSTT), k("whisper-large", "Whisper Large", core.ServiceSTT)},
	"assemblyai": {k("universal-3-pro", "Universal 3 Pro", core.ServiceSTT), k("universal-2", "Universal 2", core.ServiceSTT)},
	"elevenlabs": {k("eleven_multilingual_v2", "Eleven Multilingual v2", core.ServiceTTS), k("eleven_turbo_v2_5", "Eleven Turbo v2.5", core.ServiceTTS)},
	"inworld":    {k("inworld-tts-1.5-mini", "Inworld TTS 1.5 Mini", core.ServiceTTS), k("inworld-tts-1.5-max", "Inworld TTS 1.5 Max", core.ServiceTTS)},
	"nanobanana": {k("nanobanana-flash", "NanoBanana Flash", core.ServiceImage), k("nanobanana-pro", "NanoBanana Pro", core.ServiceImage)},
	"fal-ai": {
		k("fal-ai/flux/schnell", "FLUX Schnell", core.ServiceImage), k("fal-ai/flux/dev", "FLUX Dev", core.ServiceImage),
		k("fal-ai/flux-pro/v1.1", "FLUX Pro v1.1", core.ServiceImage),
	},
	"stability-ai": {
		k("stable-image-ultra", "Stable Image Ultra", core.ServiceImage), k("stable-image-core", "Stable Image Core", core.ServiceImage),
		k("sd3.5-large", "Stable Diffusion 3.5 Large", core.ServiceImage),
	},
	"black-forest-labs": {
		k("flux-pro-1.1", "FLUX Pro 1.1", core.ServiceImage), k("flux-pro", "FLUX Pro", core.ServiceImage),
		k("flux-dev", "FLUX Dev", core.ServiceImage),
	},
	"voyage-ai": {
		emb("voyage-3-large", "Voyage 3 Large", 1024), emb("voyage-3.5", "Voyage 3.5", 1024),
		emb("voyage-code-3", "Voyage Code 3", 1024),
	},
	"jina-ai": {
		emb("jina-embeddings-v3", "Jina Embeddings v3", 1024),
		emb("jina-embeddings-v2-base-code", "Jina Embeddings v2 Base Code", 768),
	},
	// Search/fetch providers expose a single synthetic model id named after the
	// service, so clients can target them by name (provider/web-search).
	"tavily":       {k("tavily-search", "Tavily Search", core.ServiceSearch), k("tavily-extract", "Tavily Extract", core.ServiceFetch)},
	"brave-search": {k("brave-search", "Brave Search", core.ServiceSearch)},
	"serper":       {k("serper-search", "Serper Search", core.ServiceSearch)},
	"exa":          {k("exa-search", "Exa Search", core.ServiceSearch), k("exa-contents", "Exa Contents", core.ServiceFetch)},
	"searxng":      {k("searxng-search", "SearXNG Search", core.ServiceSearch)},
	"firecrawl":    {k("firecrawl-scrape", "Firecrawl Scrape", core.ServiceFetch)},
	"jina-reader":  {k("jina-reader", "Jina Reader", core.ServiceFetch)},
}

// ModelsForProvider returns the curated model list for a provider id, merging
// the static catalog with any user-registered custom models. Custom models
// override static entries with the same id.
func ModelsForProvider(providerID string) []ModelSpec {
	static := providerModels[providerID]
	custom := dynamicModelsFor(providerID)
	if len(custom) == 0 {
		return static
	}
	if len(static) == 0 {
		return custom
	}
	merged := make([]ModelSpec, 0, len(static)+len(custom))
	customByID := make(map[string]bool, len(custom))
	for _, m := range custom {
		customByID[m.ID] = true
	}
	for _, m := range static {
		if customByID[m.ID] {
			continue // custom entry takes precedence
		}
		merged = append(merged, m)
	}
	return append(merged, custom...)
}

// ModelsByKind returns all (providerID, model) pairs across the catalog that
// serve the given service kind, excluding hidden providers.
type ProviderModel struct {
	Provider string
	Model    ModelSpec
}

// ModelsByKind collects every model of the given kind across all non-hidden,
// drivable providers in the catalog.
func ModelsByKind(kind core.ServiceKind) []ProviderModel {
	var out []ProviderModel
	for _, spec := range Catalog() {
		if spec.Hidden || !Drivable(spec) {
			continue
		}
		if !core.HasServiceKind(spec.ServiceKinds, kind) {
			continue
		}
		for _, mdl := range ModelsForProvider(spec.ID) {
			if mdl.Kind == kind {
				out = append(out, ProviderModel{Provider: spec.ID, Model: mdl})
			}
		}
	}
	return out
}

// FindModel locates a model by provider id and model id.
func FindModel(providerID, modelID string) (ModelSpec, bool) {
	for _, mdl := range ModelsForProvider(providerID) {
		if mdl.ID == modelID {
			return mdl, true
		}
	}
	return ModelSpec{}, false
}

// LiveModelSource is implemented by connectors that can fetch their model
// catalog from the upstream API at runtime (e.g. Kiro's ListAvailableModels).
// The gateway uses this to supplement the static providerModels catalog with
// live data when an account is connected.
type LiveModelSource interface {
	// ListModels fetches the live model catalog from the upstream. The returned
	// models should already include any synthetic variants (e.g. Kiro's
	// -thinking/-agentic expansions). The creds carry the access token needed
	// to authenticate with the upstream.
	ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error)
}

// liveModelSources is the registry of providers that support live model
// discovery. Populated at init time.
var liveModelSources = map[string]LiveModelSource{}

// RegisterLiveModelSource registers a live model source for a provider.
func RegisterLiveModelSource(provider string, src LiveModelSource) {
	liveModelSources[provider] = src
}

// GetLiveModelSource returns the live model source for a provider, or nil.
func GetLiveModelSource(provider string) LiveModelSource {
	if src, ok := liveModelSources[provider]; ok {
		return src
	}
	// Dynamic (user-defined) providers are not in the static registry, so build
	// a discovery source on demand. This lets a custom provider's /models
	// endpoint populate the catalog just like a built-in one.
	if p, ok := DynamicProviderByID(provider); ok {
		switch p.Dialect {
		case core.DialectOpenAI:
			return &OpenAICompatibleModelSource{provider: p.ID, defaultBase: p.BaseURL}
		case core.DialectAnthropic:
			return &AnthropicCompatibleModelSource{provider: p.ID, defaultBase: p.BaseURL}
		}
	}
	return nil
}

// AnthropicCompatibleModelSource implements LiveModelSource for custom
// anthropic-compatible dynamic providers by fetching GET <base>/v1/models
// (Anthropic shape: {"data":[{"id":"...","display_name":"..."}]}).
type AnthropicCompatibleModelSource struct {
	provider    string
	defaultBase string
}

func (s *AnthropicCompatibleModelSource) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	base := s.defaultBase
	if creds.BaseURL != "" {
		base = creds.BaseURL
	}
	for key, val := range creds.Extra {
		base = strings.ReplaceAll(base, "{"+key+"}", val)
	}
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/messages")
	base = strings.TrimSuffix(base, "/v1")
	url := strings.TrimRight(base, "/") + "/v1/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if k := strings.TrimSpace(creds.APIKey); k != "" {
		req.Header.Set("x-api-key", k)
	} else if t := strings.TrimSpace(creds.AccessToken); t != "" {
		req.Header.Set("Authorization", bearer(t))
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")

	resp, err := proxyDo(egressCtx(ctx, creds), req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("GET /v1/models returned %d: %s", resp.StatusCode, truncateError(body))
	}

	var envelope struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode /v1/models response: %w", err)
	}
	out := make([]ModelSpec, 0, len(envelope.Data))
	for _, e := range envelope.Data {
		if e.ID == "" {
			continue
		}
		name := e.DisplayName
		if name == "" {
			name = e.ID
		}
		out = append(out, ModelSpec{ID: e.ID, Name: name, Kind: core.ServiceLLM})
	}
	return out, nil
}

// QuotaEntry is one upstream quota bucket (e.g. AGENTIC_REQUEST usage).
type QuotaEntry struct {
	ResourceType string `json:"resource_type"`
	Used         int    `json:"used"`
	Limit        int    `json:"limit"`
	Remaining    int    `json:"remaining"`
	ResetAt      string `json:"reset_at,omitempty"`
	PlanName     string `json:"plan_name,omitempty"`
}

// QuotaResult holds the upstream quota info for an account.
type QuotaResult struct {
	PlanName string       `json:"plan_name,omitempty"`
	Quotas   []QuotaEntry `json:"quotas"`
	Message  string       `json:"message,omitempty"`
}

// QuotaSource is implemented by connectors that can fetch upstream quota/usage
// info (e.g. Kiro's getUsageLimits).
type QuotaSource interface {
	FetchQuota(ctx context.Context, creds core.Credentials) (*QuotaResult, error)
}

var quotaSources = map[string]QuotaSource{}

// RegisterQuotaSource registers a quota source for a provider.
func RegisterQuotaSource(provider string, src QuotaSource) {
	quotaSources[provider] = src
}

// GetQuotaSource returns the quota source for a provider, or nil.
func GetQuotaSource(provider string) QuotaSource {
	return quotaSources[provider]
}
