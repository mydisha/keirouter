package connectors

import (
	"fmt"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Registry resolves connectors by provider id. It is built once at startup from
// the provider catalog and is read-only thereafter, so it is safe for
// concurrent use without locking.
type Registry struct {
	byID map[string]core.Connector
}

// NewRegistry builds a registry from the given connectors.
func NewRegistry(conns ...core.Connector) *Registry {
	m := make(map[string]core.Connector, len(conns))
	for _, c := range conns {
		m[c.ID()] = c
	}
	return &Registry{byID: m}
}

// Get returns the connector for a provider id. Built-in connectors are resolved
// from the static map; user-defined dynamic provider instances are built on
// demand so newly-added custom providers are routable without a restart.
func (r *Registry) Get(provider string) (core.Connector, error) {
	if c, ok := r.byID[provider]; ok {
		return c, nil
	}
	if c, ok := dynamicConnector(provider); ok {
		return c, nil
	}
	return nil, fmt.Errorf("connectors: no connector for provider %q", provider)
}

// Has reports whether a provider is registered (built-in or dynamic).
func (r *Registry) Has(provider string) bool {
	if _, ok := r.byID[provider]; ok {
		return true
	}
	_, ok := DynamicProviderByID(provider)
	return ok
}

// Providers returns the registered provider ids.
func (r *Registry) Providers() []string {
	out := make([]string, 0, len(r.byID))
	for id := range r.byID {
		out = append(out, id)
	}
	return out
}

// webOnlyProviders serve only web search and/or web fetch. They get a
// WebConnector rather than the OpenAI chat connector, because their wire
// formats are provider-specific and they do not implement chat.
var webOnlyProviders = map[string]bool{
	"tavily": true, "exa": true, "serper": true, "brave-search": true,
	"searxng": true, "firecrawl": true, "jina-reader": true,
}

// adapterPendingProviders are listed in the catalog so they can be found, but
// their APIs are not OpenAI-compatible and KeiRouter has no adapter for them
// yet. They get no connector, because the generic connector would call OpenAI
// paths they do not serve, and the dashboard shows them as coming soon.
var adapterPendingProviders = map[string]bool{
	// Speech
	"deepgram": true, "assemblyai": true, "elevenlabs": true, "inworld": true, "aws-polly": true,
	"cartesia": true, "playht": true, "google-tts": true, "edge-tts": true, "local-device": true,
	"coqui": true, "tortoise": true,
	// Images
	"fal-ai": true, "stability-ai": true, "black-forest-labs": true, "topaz": true, "runwayml": true,
	"nanobanana": true, "sdwebui": true, "comfyui": true, "huggingface": true,
	// Search
	"google-pse": true, "linkup": true, "searchapi": true, "youcom": true,
}

// Drivable reports whether KeiRouter can route requests to p today: its
// dialect has a connector, or it is a web search/fetch provider, and it is
// not waiting for a native adapter.
func Drivable(p ProviderSpec) bool {
	if adapterPendingProviders[p.ID] {
		return false
	}
	return webOnlyProviders[p.ID] || DrivableDialect(p.Dialect)
}

// DefaultRegistry builds the built-in connector set from the provider catalog.
// Each entry maps a provider id to its dialect and default endpoint.
//
// Only providers whose upstream dialect KeiRouter can natively drive get a
// connector: OpenAI Chat Completions and Anthropic Messages, plus the
// dedicated web search/fetch connectors. Providers that speak a proprietary or
// not-yet-implemented dialect (kiro, cursor, gemini, vertex, ...) remain in the
// catalog for discovery and account management but are not routable until a
// dedicated connector lands.
func DefaultRegistry() *Registry {
	var conns []core.Connector
	for _, p := range Catalog() {
		if adapterPendingProviders[p.ID] {
			continue
		}
		switch {
		// Provider-id overrides: providers that share a dialect but need a
		// dedicated transport (special headers, token exchange, request shaping).
		case p.ID == "github":
			conns = append(conns, NewGitHubCopilot(p.ID, p.BaseURL))
		case p.ID == "qwen":
			conns = append(conns, NewQwen(p.ID, p.BaseURL))
		case p.ID == "iflow":
			conns = append(conns, NewIFlow(p.ID, p.BaseURL))
		case webOnlyProviders[p.ID]:
			conns = append(conns, NewWebConnector(p.ID, p.BaseURL))
		case p.Dialect == core.DialectAnthropic:
			conns = append(conns, NewAnthropic(p.ID, p.BaseURL))
		case p.Dialect == core.DialectOpenAI:
			conns = append(conns, NewOpenAICompatible(p.ID, p.BaseURL))
			// Register live model discovery: fetches GET /models from the
			// upstream so providers without a static catalog (e.g. opencode,
			// sumopod) auto-discover their models at runtime.
			if p.ID == "databricks" {
				RegisterLiveModelSource(p.ID, &databricksModelSource{conn: conns[len(conns)-1].(*OpenAICompatible)})
			} else if p.ID == "cloudflare-ai" {
				// Cloudflare uses a non-standard response envelope, so it gets
				// a dedicated model source that parses both formats.
				RegisterLiveModelSource(p.ID, &CloudflareModelSource{defaultBase: p.BaseURL})
			} else {
				RegisterLiveModelSource(p.ID, &OpenAICompatibleModelSource{provider: p.ID, defaultBase: p.BaseURL})
			}
		case p.Dialect == core.DialectGemini:
			conns = append(conns, NewGemini(p.ID, p.BaseURL))
		case p.Dialect == core.DialectOllama:
			conns = append(conns, NewOllama(p.ID, p.BaseURL))
		case p.Dialect == core.DialectVertex:
			conns = append(conns, NewVertex(p.ID, p.BaseURL))
		case p.Dialect == core.DialectOpenAIResponses:
			conns = append(conns, NewOpenAIResponses(p.ID, p.BaseURL))
		case p.Dialect == core.DialectGeminiCLI:
			conns = append(conns, NewGeminiCLI(p.ID, p.BaseURL))
		case p.Dialect == core.DialectAntigravity:
			conns = append(conns, NewAntigravity(p.ID, p.BaseURL))
		case p.Dialect == core.DialectCommandCode:
			conns = append(conns, NewCommandCode(p.ID, p.BaseURL))
		case p.Dialect == core.DialectKiro:
			conns = append(conns, NewKiro(p.ID, p.BaseURL))
		case p.Dialect == core.DialectCursor:
			conns = append(conns, NewCursor(p.ID, p.BaseURL))
		case p.Dialect == core.DialectQoder:
			qc := NewQoder(p.ID, p.BaseURL)
			conns = append(conns, qc)
			RegisterLiveModelSource(p.ID, NewQoderModelSource(qc))
		case p.Dialect == core.DialectMimoFree:
			conns = append(conns, NewMimoFree(p.ID, p.BaseURL))
		default:
			if f, ok := dialectFactories[p.Dialect]; ok {
				c := f(p.ID, p.BaseURL)
				conns = append(conns, c)
				if src, ok := c.(LiveModelSource); ok {
					RegisterLiveModelSource(p.ID, src)
				}
			}
			// Otherwise the dialect is not yet drivable; skip connector creation.
		}
	}
	return NewRegistry(conns...)
}

// DrivableDialect reports whether KeiRouter has a connector that can drive the
// given upstream dialect today.
func DrivableDialect(d core.Dialect) bool {
	switch d {
	case core.DialectOpenAI, core.DialectAnthropic, core.DialectGemini, core.DialectOllama,
		core.DialectVertex, core.DialectOpenAIResponses, core.DialectGeminiCLI, core.DialectAntigravity,
		core.DialectCommandCode, core.DialectKiro, core.DialectCursor, core.DialectQoder,
		core.DialectMimoFree:
		return true
	default:
		_, ok := dialectFactories[d]
		return ok
	}
}

// dialectFactories holds connectors that register themselves for a dialect
// from their own file (see registerDialect), so adding a dialect does not
// require touching DefaultRegistry.
var dialectFactories = map[core.Dialect]func(id, baseURL string) core.Connector{}

// registerDialect installs the connector constructor for a dialect. Call it
// from an init function.
func registerDialect(d core.Dialect, f func(id, baseURL string) core.Connector) {
	dialectFactories[d] = f
}
