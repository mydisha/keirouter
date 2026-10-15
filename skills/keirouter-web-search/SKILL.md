---
name: keirouter-web-search
description: Web search via KeiRouter /v1/search using Tavily / Exa / Brave Search / Serper. Use when the user wants to search the web, look up information, find articles, or query a search engine.
---

# KeiRouter — Web Search

Requires `KEIROUTER_URL` (and `KEIROUTER_KEY` if auth enabled). See https://raw.githubusercontent.com/mydisha/keirouter/main/skills/keirouter/SKILL.md for setup.

## Discover

```bash
curl $KEIROUTER_URL/v1/models/search | jq '.data[].id'
curl "$KEIROUTER_URL/v1/models/info?id=tavily/tavily-search"
```

`model` is `provider/model` (e.g. `tavily/tavily-search`), a chain name that lists several search providers for automatic fallback, or an alias.

## Endpoint

`POST $KEIROUTER_URL/v1/search`

| Field | Required | Notes |
|---|---|---|
| `model` | yes | from `/v1/models/search` |
| `query` | yes | search query |
| `max_results` | no | number of results (provider default when omitted) |
| `search_type` | no | `web` (default) or `news`, where the provider supports it |

## Examples

```bash
curl -X POST $KEIROUTER_URL/v1/search \
  -H "Authorization: Bearer $KEIROUTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"tavily/tavily-search","query":"KeiRouter open source","max_results":5}'
```

JS:

```js
const r = await fetch(`${process.env.KEIROUTER_URL}/v1/search`, {
  method: "POST",
  headers: { "Authorization": `Bearer ${process.env.KEIROUTER_KEY}`, "Content-Type": "application/json" },
  body: JSON.stringify({ model: "brave-search/brave-search", query: "latest LLM benchmarks", max_results: 10 }),
});
const { results } = await r.json();
for (const hit of results) console.log(hit.title, hit.url);
```

## Response shape

```json
{
  "query": "KeiRouter open source",
  "results": [
    { "title": "...", "url": "https://...", "snippet": "...", "score": 0.92 }
  ]
}
```

`snippet` and `score` are omitted when the provider doesn't return them. The `X-KeiRouter-Provider` header names the provider that answered.

## Provider quick reference

| Provider | `model` | Notes |
|---|---|---|
| Tavily | `tavily/tavily-search` | Also does web fetch |
| Exa | `exa/exa-search` | Also does web fetch |
| Brave Search | `brave-search/brave-search` | |
| Serper | `serper/serper-search` | Google results |

Google PSE, Linkup, SearchAPI and You.com are listed in the dashboard as coming soon. Chat models with built-in web search (Perplexity, xAI, Gemini) are used through `/v1/chat/completions`, not this endpoint.
