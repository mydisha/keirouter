---
name: keirouter-tts
description: Text-to-speech via KeiRouter /v1/audio/speech using OpenAI voices (or any OpenAI-compatible speech server added as a custom provider). Use when the user wants to convert text to speech, generate audio, voiceover, narrate, or read text aloud.
---

# KeiRouter — Text-to-Speech

Requires `KEIROUTER_URL` (and `KEIROUTER_KEY` if auth enabled). See https://raw.githubusercontent.com/mydisha/keirouter/main/skills/keirouter/SKILL.md for setup.

## Discover

```bash
# 1) List models
curl $KEIROUTER_URL/v1/models/tts | jq '.data[].id'
# 2) Per-model metadata
curl "$KEIROUTER_URL/v1/models/info?id=openai/tts-1"
```

## Endpoint

`POST $KEIROUTER_URL/v1/audio/speech` (OpenAI-compatible)

| Field | Required | Notes |
|---|---|---|
| `model` | yes | `provider/model` from `/v1/models/tts`, e.g. `openai/gpt-4o-mini-tts` |
| `input` | yes | text to speak |
| `voice` | no | voice name, e.g. `alloy`, `nova`, `verse` (default `alloy`) |
| `response_format` | no | audio format passed to the provider: `mp3` (default), `opus`, `aac`, `flac`, `wav`, `pcm` |
| `speed` | no | 0.25–4.0 |

## Examples

Save MP3:

```bash
curl -X POST "$KEIROUTER_URL/v1/audio/speech" \
  -H "Authorization: Bearer $KEIROUTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini-tts","voice":"alloy","input":"Hello world"}' \
  --output speech.mp3
```

JS (save file):

```js
import { writeFile } from "node:fs/promises";
const r = await fetch(`${process.env.KEIROUTER_URL}/v1/audio/speech`, {
  method: "POST",
  headers: { "Authorization": `Bearer ${process.env.KEIROUTER_KEY}`, "Content-Type": "application/json" },
  body: JSON.stringify({ model: "openai/tts-1", voice: "nova", input: "Hello world" }),
});
await writeFile("speech.mp3", Buffer.from(await r.arrayBuffer()));
```

## Response shape

Raw audio bytes. `Content-Type` is whatever the provider returned (e.g. `audio/mpeg`), and `X-KeiRouter-Provider` names the provider that served it.

## Provider quick reference

| Provider | `model` | Notes |
|---|---|---|
| OpenAI | `openai/tts-1`, `openai/tts-1-hd`, `openai/gpt-4o-mini-tts` | Native OpenAI shape |
| Custom provider | `<provider>/<model>` | Any server that speaks OpenAI `/audio/speech` |

ElevenLabs, Deepgram, Edge TTS, Google TTS and Inworld are listed in the dashboard as coming soon: KeiRouter has no adapter for their APIs yet.
