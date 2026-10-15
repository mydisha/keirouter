---
name: keirouter-image
description: Generate images via KeiRouter /v1/images/generations using OpenAI (DALL-E, gpt-image), xAI, Venice or Recraft models, or any OpenAI-compatible image server. Use when the user wants to create, generate, draw, or render an image, picture, or text-to-image (txt2img).
---

# KeiRouter — Image Generation

Requires `KEIROUTER_URL` (and `KEIROUTER_KEY` if auth enabled). See https://raw.githubusercontent.com/mydisha/keirouter/main/skills/keirouter/SKILL.md for setup.

## Discover

```bash
curl $KEIROUTER_URL/v1/models/image | jq '.data[].id'
# Per-model params/options (size enum, quality enum)
curl "$KEIROUTER_URL/v1/models/info?id=openai/dall-e-3"
```

## Endpoint

`POST $KEIROUTER_URL/v1/images/generations` (OpenAI-compatible)

| Field | Required | Notes |
|---|---|---|
| `model` | yes | `provider/model` from `/v1/models/image` |
| `prompt` | yes | image description |
| `n` | no | count (provider-dependent; Venice supports 1) |
| `size` | no | `1024x1024`, `1792x1024`, ... |
| `quality` | no | `standard` / `hd` (OpenAI) |
| `style` | no | provider-specific style preset |
| `response_format` | no | `url` or `b64_json` |

## Examples

Save to file:

```bash
curl -s -X POST "$KEIROUTER_URL/v1/images/generations" \
  -H "Authorization: Bearer $KEIROUTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/dall-e-3","prompt":"watercolor mountains at sunrise","size":"1024x1024","response_format":"b64_json"}' \
  | jq -r '.data[0].b64_json' | base64 -d > out.png
```

JS (URL or base64):

```js
const r = await fetch(`${process.env.KEIROUTER_URL}/v1/images/generations`, {
  method: "POST",
  headers: { "Authorization": `Bearer ${process.env.KEIROUTER_KEY}`, "Content-Type": "application/json" },
  body: JSON.stringify({ model: "xai/grok-2-image-1212", prompt: "neon city", n: 1 }),
});
const { data } = await r.json();
console.log(data[0].url || data[0].b64_json.slice(0, 40));
```

## Response shape

Always JSON, in the OpenAI shape:
```json
{ "created": 1735000000, "data": [{ "url": "https://..." }] }
```

With `response_format=b64_json`:
```json
{ "created": 1735000000, "data": [{ "b64_json": "iVBORw0KGgo..." }] }
```

## Provider quick reference

| Provider | ID | Notes |
|---|---|---|
| OpenAI | `openai` | DALL-E 3, gpt-image; standard/hd quality |
| xAI | `xai` | Grok image models |
| Venice AI | `venice` | One image per request; `url` is a data URL |
| Recraft | `recraft` | OpenAI-compatible endpoint |
| Custom provider | `<provider>` | Any server that speaks OpenAI `/images/generations` |

Gemini, MiniMax, Stability AI, Fal.ai, Black Forest Labs and NanoBanana are not served by this endpoint today: their image APIs need adapters KeiRouter doesn't have yet.
