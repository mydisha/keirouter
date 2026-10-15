---
name: keirouter-stt
description: Speech-to-text via KeiRouter /v1/audio/transcriptions using OpenAI Whisper or Groq Whisper models (or any OpenAI-compatible transcription server). Use when the user wants to transcribe audio, convert speech to text, or get subtitles from audio files.
---

# KeiRouter — Speech-to-Text

Requires `KEIROUTER_URL` (and `KEIROUTER_KEY` if auth enabled). See https://raw.githubusercontent.com/mydisha/keirouter/main/skills/keirouter/SKILL.md for setup.

## Discover

```bash
curl $KEIROUTER_URL/v1/models/stt | jq '.data[].id'
curl "$KEIROUTER_URL/v1/models/info?id=openai/whisper-1"
```

`model` = `provider/model`, e.g. `openai/whisper-1` or `groq/whisper-large-v3-turbo`.

## Endpoint

`POST $KEIROUTER_URL/v1/audio/transcriptions` (OpenAI Whisper compatible, `multipart/form-data`)

| Field | Required | Notes |
|---|---|---|
| `model` | yes | from `/v1/models/stt` |
| `file` | yes | audio file (mp3, wav, m4a, webm, ogg, flac) |
| `language` | no | ISO-639-1 (e.g. `en`, `id`) |
| `prompt` | no | hint text to guide transcription |
| `response_format` | no | `json` (default) or `text` |
| `temperature` | no | 0–1 |

## Examples

```bash
curl -X POST "$KEIROUTER_URL/v1/audio/transcriptions" \
  -H "Authorization: Bearer $KEIROUTER_KEY" \
  -F "model=openai/whisper-1" \
  -F "file=@audio.mp3" \
  -F "language=en"
```

JS (Node):

```js
import { readFile } from "node:fs/promises";
const form = new FormData();
form.append("model", "groq/whisper-large-v3-turbo");
form.append("file", new Blob([await readFile("audio.mp3")]), "audio.mp3");
const r = await fetch(`${process.env.KEIROUTER_URL}/v1/audio/transcriptions`, {
  method: "POST",
  headers: { "Authorization": `Bearer ${process.env.KEIROUTER_KEY}` },
  body: form,
});
const { text } = await r.json();
console.log(text);
```

## Response shape

Default (`response_format=json`):
```json
{ "text": "Hello, this is the transcription.", "language": "en", "duration": 3.2 }
```

`response_format=text` returns the transcript as `text/plain`. Segment timestamps (`verbose_json`) and subtitle formats (`srt`, `vtt`) are not passed through; any other format comes back inside the `text` field.

## Provider quick reference

| Provider | `model` | Notes |
|---|---|---|
| OpenAI | `whisper-1`, `gpt-4o-transcribe`, `gpt-4o-mini-transcribe` | Native OpenAI shape |
| Groq | `whisper-large-v3`, `whisper-large-v3-turbo` | Fastest; OpenAI shape |
| Custom provider | `<provider>/<model>` | Any server that speaks OpenAI `/audio/transcriptions` |

Deepgram and AssemblyAI are listed in the dashboard as coming soon: KeiRouter has no adapter for their APIs yet.
