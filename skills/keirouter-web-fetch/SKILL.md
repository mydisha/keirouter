---
name: keirouter-web-fetch
description: Fetch URL → markdown / text / HTML via KeiRouter /v1/web/fetch using Firecrawl / Tavily Extract / Exa Contents. Use when the user wants to scrape a webpage, extract URL content, read article, or convert a URL to markdown.
---

# KeiRouter — Web Fetch

Requires `KEIROUTER_URL` (and `KEIROUTER_KEY` if auth enabled). See https://raw.githubusercontent.com/mydisha/keirouter/main/skills/keirouter/SKILL.md for setup.

## Discover

```bash
curl $KEIROUTER_URL/v1/models/fetch | jq '.data[].id'
curl "$KEIROUTER_URL/v1/models/info?id=firecrawl/firecrawl-scrape"
```

`model` is `provider/model` (e.g. `firecrawl/firecrawl-scrape`), a chain name that lists several fetch providers for automatic fallback, or an alias.

## Endpoint

`POST $KEIROUTER_URL/v1/web/fetch`

| Field | Required | Notes |
|---|---|---|
| `model` | yes | from `/v1/models/fetch` |
| `url` | yes | URL to extract |
| `format` | no | `markdown` (default) / `text` / `html` |
| `max_characters` | no | truncate output |

## Examples

### Firecrawl
```bash
curl -X POST $KEIROUTER_URL/v1/web/fetch \
  -H "Authorization: Bearer $KEIROUTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"firecrawl/firecrawl-scrape","url":"https://example.com","format":"markdown"}'
```

### Tavily
```bash
curl -X POST $KEIROUTER_URL/v1/web/fetch \
  -H "Authorization: Bearer $KEIROUTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"tavily/tavily-extract","url":"https://example.com","format":"markdown"}'
```

JS:

```js
const r = await fetch(`${process.env.KEIROUTER_URL}/v1/web/fetch`, {
  method: "POST",
  headers: { "Authorization": `Bearer ${process.env.KEIROUTER_KEY}`, "Content-Type": "application/json" },
  body: JSON.stringify({ model: "exa/exa-contents", url: "https://example.com", max_characters: 5000 }),
});
const page = await r.json();
console.log(page.title, page.content.length);
```

## Response shape

```json
{
  "url": "https://example.com",
  "title": "Example Domain",
  "content": "# Example Domain\n...",
  "format": "markdown"
}
```

The `X-KeiRouter-Provider` header names the provider that answered.

## Provider quick reference

| Provider | `model` | Best for |
|---|---|---|
| Firecrawl | `firecrawl/firecrawl-scrape` | JS-rendered pages, `format=markdown/html` |
| Tavily | `tavily/tavily-extract` | Bulk extract |
| Exa | `exa/exa-contents` | Pre-indexed pages; fast text extraction |
