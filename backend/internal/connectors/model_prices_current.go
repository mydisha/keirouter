package connectors

// currentOfficialModelPrices holds curated rates the catalog does not carry:
// vendor aliases, context-tiered estimates, and subscription retail
// equivalents. These entries come last so they override catalog values for
// the same provider/model key.
func currentOfficialModelPrices() []ModelPrice {
	const (
		cloudflareURL = "https://developers.cloudflare.com/workers-ai/platform/pricing/"
		qwenURL       = "https://modelstudio.alibabacloud.com/"
	)
	prices := []ModelPrice{
		// Alibaba retail list price. Qoder promotions and subscription credits
		// are not token prices and are therefore not applied here.
		{Provider: "qwen", Model: "qwen-3.7-max", InputPerM: 2.5, OutputPerM: 7.5, CachedInputPerM: .5, Source: "official", SourceURL: qwenURL},
		// Plus is tiered by context on the vendor page. The lower standard tier
		// is retained as a retail-equivalent estimate until the request captures
		// the vendor's exact tier metadata.
		{Provider: "qwen", Model: "qwen3.7-plus", InputPerM: .4, OutputPerM: 1.6, CachedInputPerM: .04, CacheWritePerM: .4, Source: "official_range", SourceURL: qwenURL, Estimated: true},
		{Provider: "qwen", Model: "qwen-3.7-plus", InputPerM: .4, OutputPerM: 1.6, CachedInputPerM: .04, CacheWritePerM: .4, Source: "official_range", SourceURL: qwenURL, Estimated: true},

		// Cloudflare Workers AI models no longer in the catalog listing.
		{Provider: "cloudflare-ai", Model: "@cf/moonshotai/kimi-k2.5", InputPerM: .6, CachedInputPerM: .1, OutputPerM: 3, Source: "official", SourceURL: cloudflareURL},
		// Cloudflare Workers AI models added after the catalog refresh. Without
		// an exact row, cached input fell through to a rate without a cached
		// tier and was billed at the full input price.
		{Provider: "cloudflare-ai", Model: "@cf/zai-org/glm-5.3", InputPerM: 1.4, CachedInputPerM: .26, OutputPerM: 4.4, Source: "official", SourceURL: cloudflareURL},
		{Provider: "cloudflare-ai", Model: "@cf/zai-org/glm-5.3-flash", InputPerM: .15, CachedInputPerM: .03, OutputPerM: .5, Source: "official", SourceURL: cloudflareURL},
		{Provider: "cloudflare-ai", Model: "@cf/deepseek-ai/deepseek-v4-flash-0731", InputPerM: .44, CachedInputPerM: .014, OutputPerM: 1.32, Source: "official", SourceURL: cloudflareURL},
		{Provider: "cloudflare-ai", Model: "@cf/deepseek-ai/deepseek-v4.1-flash-1017", InputPerM: .3, CachedInputPerM: .006, OutputPerM: 1.2, Source: "official", SourceURL: cloudflareURL},
		{Provider: "cloudflare-ai", Model: "@cf/deepseek-ai/deepseek-v4-pro-0813", InputPerM: 1.32, CachedInputPerM: .044, OutputPerM: 3.96, Source: "official", SourceURL: cloudflareURL},
	}

	// Kiro is subscription/credit based. GPT 5.6 entries mirror the OpenAI list
	// price and are explicitly marked retail-equivalent rather than actual spend.
	gpt56 := map[string]bool{"gpt-5.6-sol": true, "gpt-5.6-terra": true, "gpt-5.6-luna": true}
	for _, p := range catalogModelPrices() {
		if p.Provider != "openai" || !gpt56[p.Model] {
			continue
		}
		p.Provider = "kiro"
		p.Source = "retail_equivalent"
		p.Estimated = true
		for _, suffix := range []string{"", "-thinking", "-agentic", "-thinking-agentic"} {
			variant := p
			variant.Model += suffix
			prices = append(prices, variant)
		}
	}
	return prices
}
