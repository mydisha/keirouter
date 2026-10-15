package capability

import "testing"

func TestCloudflareVisionResolution(t *testing.T) {
	unknown := Resolve("cloudflare-ai", "@cf/example/new-model")
	if unknown.VisionState != SupportUnknown || unknown.Profile.Vision || ImagePolicy("cloudflare-ai", "@cf/example/new-model") != ImageOptimistic {
		t.Fatalf("unknown Cloudflare resolution = %+v", unknown)
	}

	textOnly := Resolve("cloudflare-ai", "@cf/zai-org/glm-4.7-flash")
	if textOnly.VisionState != SupportUnsupported || ImagePolicy("cloudflare-ai", "@cf/zai-org/glm-4.7-flash") != ImageStrip {
		t.Fatalf("text-only Cloudflare resolution = %+v", textOnly)
	}

	glm := Resolve("cloudflare-ai", "@cf/zai-org/glm-5.3-flash")
	if glm.Source != SourceProvider || glm.VisionState != SupportSupported || !glm.Profile.Vision || !glm.Profile.Reasoning || !glm.Profile.Tools || glm.Profile.ThinkingFormat != "openai" || glm.Profile.ContextWindow != 1048576 {
		t.Fatalf("GLM 5.3 resolution = %+v", glm)
	}
	if Resolve("", "glm-5.2").Profile.Vision {
		t.Fatal("generic GLM 5.2 must not gain vision")
	}
}

func TestCloudflareSeededCapabilities(t *testing.T) {
	qwq := Resolve("cloudflare-ai", "@cf/qwen/qwq-32b")
	if qwq.Source != SourceProvider || qwq.Profile.Tools || !qwq.Profile.Reasoning || qwq.Profile.ContextWindow != 24000 {
		t.Fatalf("qwq resolution = %+v", qwq)
	}
	scout := Resolve("cloudflare-ai", "@cf/meta/llama-4-scout-17b-16e-instruct")
	if !scout.Profile.Vision || !scout.Profile.Tools || scout.VisionState != SupportSupported {
		t.Fatalf("llama-4-scout resolution = %+v", scout)
	}
	if ImagePolicy("cloudflare-ai", "@cf/meta/llama-3.3-70b-instruct-fp8-fast") != ImageStrip {
		t.Fatal("known text-only Cloudflare model must strip images rather than probe optimistically")
	}
}
