package connectors

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestCatalogHasCoreProviders(t *testing.T) {
	want := []string{"openai", "anthropic", "deepseek", "groq", "gemini", "openrouter", "tavily", "elevenlabs"}
	for _, id := range want {
		if _, ok := SpecByID(id); !ok {
			t.Errorf("catalog missing provider %q", id)
		}
	}
}

func TestSpecByAlias(t *testing.T) {
	spec, ok := SpecByAlias("ds")
	if !ok || spec.ID != "deepseek" {
		t.Fatalf("alias ds should resolve to deepseek, got %q ok=%v", spec.ID, ok)
	}
	// id also resolves.
	if spec, ok := SpecByAlias("openai"); !ok || spec.ID != "openai" {
		t.Fatalf("id openai should resolve, got %q ok=%v", spec.ID, ok)
	}
}

// A custom provider created before a release added a built-in with the same
// ID keeps its alias, so "<alias>/<model>" strings route as before upgrading.
func TestSpecByAliasPrefersCustomOverNewBuiltin(t *testing.T) {
	const customID = "custom-openai-lm-studio-test"
	RegisterDynamicProvider(DynamicProvider{
		ID: customID, DisplayName: "LM Studio", Alias: "lm-studio", Dialect: core.DialectOpenAI, BaseURL: "http://192.0.2.10:1234/v1",
	})
	t.Cleanup(func() { UnregisterDynamicProvider(customID) })

	if spec, ok := SpecByAlias("lm-studio"); !ok || spec.ID != customID {
		t.Fatalf("custom alias should win over the built-in, got %q ok=%v", spec.ID, ok)
	}
	if spec, ok := BuiltinSpecByAlias("lm-studio"); !ok || spec.ID != "lm-studio" {
		t.Fatalf("BuiltinSpecByAlias should ignore custom providers, got %q ok=%v", spec.ID, ok)
	}
	// IDs remain exact: the built-in is still found by SpecByID.
	if spec, ok := SpecByID("lm-studio"); !ok || spec.ID != "lm-studio" {
		t.Fatalf("SpecByID should still return the built-in, got %q ok=%v", spec.ID, ok)
	}
	// Unrelated built-in aliases are unaffected.
	if spec, ok := SpecByAlias("ds"); !ok || spec.ID != "deepseek" {
		t.Fatalf("alias ds should still resolve to deepseek, got %q ok=%v", spec.ID, ok)
	}
}

func TestSpecsByKind(t *testing.T) {
	cases := map[core.ServiceKind][]string{
		core.ServiceImage:     {"openai", "xai", "venice", "recraft"},
		core.ServiceSTT:       {"openai", "groq"},
		core.ServiceTTS:       {"openai"},
		core.ServiceEmbedding: {"openai", "cloudflare-ai", "bedrock", "voyage-ai"},
		core.ServiceSearch:    {"tavily", "brave-search", "serper", "exa"},
		core.ServiceFetch:     {"firecrawl", "exa", "tavily"},
	}
	for kind, wantProviders := range cases {
		specs := SpecsByKind(kind)
		got := map[string]bool{}
		for _, s := range specs {
			got[s.ID] = true
		}
		for _, id := range wantProviders {
			if !got[id] {
				t.Errorf("kind %q: expected provider %q in results", kind, id)
			}
		}
	}
}

func TestModelsByKind(t *testing.T) {
	// Every LLM model should belong to a provider that advertises the LLM kind.
	llms := ModelsByKind(core.ServiceLLM)
	if len(llms) == 0 {
		t.Fatal("expected at least one LLM model in catalog")
	}
	for _, pm := range llms {
		if pm.Model.Kind != core.ServiceLLM {
			t.Errorf("ModelsByKind(llm) returned non-llm model %q (%q)", pm.Model.ID, pm.Model.Kind)
		}
	}

	// Image models should be present and tagged correctly.
	images := ModelsByKind(core.ServiceImage)
	if len(images) == 0 {
		t.Fatal("expected at least one image model in catalog")
	}
	for _, pm := range images {
		if pm.Model.Kind != core.ServiceImage {
			t.Errorf("ModelsByKind(image) returned non-image model %q", pm.Model.ID)
		}
	}
}

func TestMediaCatalogCompleteness(t *testing.T) {
	// Video providers must surface video-tagged models.
	wantVideo := []string{"xai"}
	video := ModelsByKind(core.ServiceVideo)
	gotVideo := map[string]bool{}
	for _, pm := range video {
		if pm.Model.Kind != core.ServiceVideo {
			t.Errorf("ModelsByKind(video) returned non-video model %q", pm.Model.ID)
		}
		gotVideo[pm.Provider] = true
	}
	for _, id := range wantVideo {
		if !gotVideo[id] {
			t.Errorf("expected video provider %q in catalog", id)
		}
	}

	// ImageToText providers must surface image_to_text-tagged models.
	// (vertex is tagged too but stays Hidden, so it is intentionally not
	// surfaced in the media catalog.)
	wantI2T := []string{
		"anthropic", "gemini", "groq", "mistral", "minimax",
		"openrouter", "vercel-ai-gateway", "xai", "kimchi",
	}
	i2t := ModelsByKind(core.ServiceImageToText)
	gotI2T := map[string]bool{}
	for _, pm := range i2t {
		if pm.Model.Kind != core.ServiceImageToText {
			t.Errorf("ModelsByKind(image_to_text) returned wrong-kind model %q", pm.Model.ID)
		}
		gotI2T[pm.Provider] = true
	}
	for _, id := range wantI2T {
		if !gotI2T[id] {
			t.Errorf("expected image_to_text provider %q in catalog", id)
		}
	}
}

func TestFindModel(t *testing.T) {
	if _, ok := FindModel("openai", "gpt-4o"); !ok {
		t.Error("expected to find openai/gpt-4o")
	}
	if _, ok := FindModel("commandcode", "deepseek/deepseek-v4-pro"); !ok {
		t.Error("expected to find commandcode/deepseek/deepseek-v4-pro")
	}
	if _, ok := FindModel("deepseek", "deepseek-v4-pro-max"); !ok {
		t.Error("expected to find deepseek/deepseek-v4-pro-max")
	}
	if _, ok := FindModel("deepseek", "deepseek-v4-pro-none"); !ok {
		t.Error("expected to find deepseek/deepseek-v4-pro-none")
	}
	if _, ok := FindModel("openai", "nonexistent-model"); ok {
		t.Error("expected miss for nonexistent model")
	}
}

func TestDeepSeekPricing(t *testing.T) {
	flash, ok := ModelPriceByProviderModel("deepseek", "deepseek-v4-flash")
	if !ok {
		t.Fatal("missing deepseek-v4-flash price")
	}
	if flash.InputPerM != 0.3 || flash.OutputPerM != 1.2 || flash.CachedInputPerM != 0.006 {
		t.Fatalf("unexpected deepseek-v4-flash pricing: %+v", flash)
	}

	pro, ok := ModelPriceByProviderModel("deepseek", "deepseek-v4-pro")
	if !ok {
		t.Fatal("missing deepseek-v4-pro price")
	}
	if pro.InputPerM != 1.32 || pro.OutputPerM != 3.96 || pro.CachedInputPerM != 0.044 {
		t.Fatalf("unexpected deepseek-v4-pro pricing: %+v", pro)
	}
}

func TestCatalogPricesLatestFamilies(t *testing.T) {
	cases := []struct {
		provider, model    string
		wantInput, wantOut float64
	}{
		{"anthropic", "claude-opus-5-5", 4, 20},
		{"anthropic", "claude-sonnet-4-6", 3, 15},
		{"claude", "claude-haiku-5-5", 0.1, 0.5},
		{"openai", "gpt-6-sol", 2, 10},
		{"codex", "gpt-5.6-terra", 2, 12},
		{"gemini", "gemini-3.1-pro-preview", 2, 12},
		{"xai", "grok-4.5", 2, 6},
		{"kimi", "kimi-k3", 3, 15},
		{"glm", "glm-5.3", 1.4, 4.4},
	}
	for _, tc := range cases {
		mp, ok := ModelPriceByProviderModel(tc.provider, tc.model)
		if !ok {
			t.Errorf("missing price for %s/%s", tc.provider, tc.model)
			continue
		}
		if mp.InputPerM != tc.wantInput || mp.OutputPerM != tc.wantOut {
			t.Errorf("%s/%s = %v/%v, want %v/%v", tc.provider, tc.model, mp.InputPerM, mp.OutputPerM, tc.wantInput, tc.wantOut)
		}
	}
}

func TestModelPriceCanonicalPrefersFirstPartyRate(t *testing.T) {
	// Hosts resell DeepSeek V4 Pro at different rates; an unknown provider
	// should fall back to the vendor's own list price.
	mp, ok := ModelPriceByProviderModel("some-custom-endpoint", "deepseek-v4-pro")
	if !ok {
		t.Fatal("expected canonical match for deepseek-v4-pro")
	}
	if mp.Provider != "deepseek" || mp.InputPerM != 1.32 {
		t.Fatalf("canonical match = %s/%s %v, want deepseek list price", mp.Provider, mp.Model, mp.InputPerM)
	}
}

func TestModelPricingTableReturnsCopy(t *testing.T) {
	table := ModelPricingTable()
	table[0].InputPerM = -1
	if ModelPricingTable()[0].InputPerM == -1 {
		t.Fatal("ModelPricingTable must not expose the shared catalog slice")
	}
}

func TestCommandCodeCatalogVisible(t *testing.T) {
	spec, ok := SpecByID("commandcode")
	if !ok {
		t.Fatal("catalog missing commandcode")
	}
	if spec.Hidden {
		t.Fatal("commandcode should be visible")
	}
	if spec.APIKeyURL != "https://commandcode.ai/studio" {
		t.Fatalf("unexpected commandcode APIKeyURL %q", spec.APIKeyURL)
	}
	if len(ModelsForProvider("commandcode")) == 0 {
		t.Fatal("commandcode should have static models")
	}
}

func TestCatalogHasNewProviders(t *testing.T) {
	// Providers added for coverage parity. Each must exist, expose static
	// models, and (since they use drivable dialects) get a live connector.
	cases := []struct {
		id       string
		alias    string
		authKind string
	}{
		{"venice", "vn", "api_key"},
		{"featherless", "fl", "api_key"},
		{"perplexity-agent", "pa", "api_key"},
		{"mmf", "mmf", "none"},
		{"clinepass", "clinepass", "oauth"},
		{"grok-cli", "gcli", "oauth"},
	}
	r := DefaultRegistry()
	for _, c := range cases {
		spec, ok := SpecByID(c.id)
		if !ok {
			t.Errorf("catalog missing provider %q", c.id)
			continue
		}
		if spec.AuthKind != c.authKind {
			t.Errorf("%s: AuthKind = %q, want %q", c.id, spec.AuthKind, c.authKind)
		}
		if s, ok := SpecByAlias(c.alias); !ok || s.ID != c.id {
			t.Errorf("alias %q should resolve to %q, got %q ok=%v", c.alias, c.id, s.ID, ok)
		}
		if len(ModelsForProvider(c.id)) == 0 {
			t.Errorf("%s should have static models", c.id)
		}
		if !r.Has(c.id) {
			t.Errorf("registry should have connector for %q", c.id)
		}
	}
}

func TestDrivableDialect(t *testing.T) {
	for _, d := range []core.Dialect{
		core.DialectOpenAI, core.DialectAnthropic, core.DialectGemini, core.DialectOllama,
	} {
		if !DrivableDialect(d) {
			t.Errorf("dialect %q must be drivable", d)
		}
	}
	// All proprietary/subscription dialects are now drivable: the full
	// transport surface has been ported.
	for _, d := range []core.Dialect{
		core.DialectKiro, core.DialectGeminiCLI, core.DialectAntigravity,
		core.DialectCommandCode, core.DialectCursor, core.DialectQoder,
	} {
		if !DrivableDialect(d) {
			t.Errorf("dialect %q must be drivable", d)
		}
	}
	// An unknown dialect remains non-drivable.
	if DrivableDialect(core.Dialect("does-not-exist")) {
		t.Error("unknown dialect must not be drivable")
	}
}

func TestRegistryRegistersDrivableProviders(t *testing.T) {
	r := DefaultRegistry()
	// OpenAI/Anthropic chat, Gemini, Ollama, and web providers should be registered.
	for _, id := range []string{"openai", "anthropic", "gemini", "ollama", "ollama-local", "tavily", "firecrawl"} {
		if !r.Has(id) {
			t.Errorf("registry should have connector for %q", id)
		}
	}
	// Every subscription/proprietary provider now has a connector.
	for _, id := range []string{"kiro", "gemini-cli", "antigravity", "commandcode", "cursor", "github", "qwen", "iflow"} {
		if !r.Has(id) {
			t.Errorf("registry should have connector for %q", id)
		}
	}
	// An unknown provider still has no connector.
	if r.Has("does-not-exist") {
		t.Error("registry should not have a connector for an unknown provider")
	}
}
