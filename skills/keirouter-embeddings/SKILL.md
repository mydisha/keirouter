---
name: keirouter-embeddings
description: Generate vector embeddings via KeiRouter /v1/embeddings using OpenAI / Mistral / Cohere / Voyage / NVIDIA / AWS Bedrock and other embedding models for RAG, semantic search, similarity. Use when the user wants embeddings, vectors, RAG, semantic search, or to embed text.
---

# KeiRouter — Embeddings

Requires `KEIROUTER_URL` (and `KEIROUTER_KEY` if auth enabled). See https://raw.githubusercontent.com/mydisha/keirouter/main/skills/keirouter/SKILL.md for setup.

## Discover

```bash
curl $KEIROUTER_URL/v1/models/embedding | jq '.data[].id'
# Per-model dimensions
curl "$KEIROUTER_URL/v1/models/info?id=openai/text-embedding-3-small"
```

## Endpoint

`POST $KEIROUTER_URL/v1/embeddings`

| Field | Required | Notes |
|---|---|---|
| `model` | yes | from `/v1/models/embedding` |
| `input` | yes | string OR array of strings |
| `dimensions` | no | OpenAI v3 only |

## Examples

```bash
curl -X POST $KEIROUTER_URL/v1/embeddings \
  -H "Authorization: Bearer $KEIROUTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/text-embedding-3-small","input":["hello","world"]}'
```

JS:

```js
const r = await fetch(`${process.env.KEIROUTER_URL}/v1/embeddings`, {
  method: "POST",
  headers: { "Authorization": `Bearer ${process.env.KEIROUTER_KEY}`, "Content-Type": "application/json" },
  body: JSON.stringify({ model: "gemini/text-embedding-004", input: "RAG chunk text" }),
});
const { data } = await r.json();
console.log(data[0].embedding.length);  // dimension
```

## Response shape

```json
{ "object": "list", "model": "openai/text-embedding-3-small",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.0123, -0.045, ...] },
    { "object": "embedding", "index": 1, "embedding": [...] }
  ],
  "usage": { "prompt_tokens": 5, "total_tokens": 5 } }
```

## Provider quick reference

| Provider | Notes |
|---|---|
| OpenAI, Mistral, Cohere, Voyage, Jina, Fireworks, Together, Nebius, NVIDIA, OpenRouter, Venice, Cloudflare, vLLM | OpenAI shape — `dimensions` works only on models that support it (e.g. OpenAI `text-embedding-3-*`) |
| AWS Bedrock | Titan and Cohere Embed via InvokeModel; send OpenAI shape |
| Azure AI Foundry, Databricks, GitHub Models | OpenAI shape on the provider's own endpoint |
| GigaChat | Native connector; send OpenAI shape |
| Custom OpenAI | Any OpenAI-compatible `/embeddings` endpoint |

Vectors are always returned as float arrays.

Batch (`input` as array) is faster; some providers cap batch size.
