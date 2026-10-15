package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func init() {
	providerModels["bedrock"] = []ModelSpec{
		// Anthropic Claude via US cross-region inference profiles.
		m("us.anthropic.claude-opus-5-5", "Claude Opus 5.5"),
		m("us.anthropic.claude-sonnet-5-5", "Claude Sonnet 5.5"),
		m("us.anthropic.claude-haiku-5-5", "Claude Haiku 5.5"),
		m("us.anthropic.claude-opus-5", "Claude Opus 5"),
		m("us.anthropic.claude-sonnet-5", "Claude Sonnet 5"),
		m("us.anthropic.claude-opus-4-8", "Claude Opus 4.8"),
		m("us.anthropic.claude-opus-4-7", "Claude Opus 4.7"),
		m("us.anthropic.claude-sonnet-4-6", "Claude Sonnet 4.6"),
		m("us.anthropic.claude-opus-4-6-v1", "Claude Opus 4.6"),
		m("us.anthropic.claude-sonnet-4-5-20250929-v1:0", "Claude Sonnet 4.5"),
		m("us.anthropic.claude-opus-4-5-20251101-v1:0", "Claude Opus 4.5"),
		m("us.anthropic.claude-haiku-4-5-20251001-v1:0", "Claude Haiku 4.5"),
		m("us.anthropic.claude-opus-4-1-20250805-v1:0", "Claude Opus 4.1"),
		m("us.anthropic.claude-3-7-sonnet-20250219-v1:0", "Claude 3.7 Sonnet"),
		// Amazon Nova.
		m("us.amazon.nova-premier-v1:0", "Amazon Nova Premier"),
		m("us.amazon.nova-2-pro-preview-20251202-v1:0", "Amazon Nova 2 Pro (Preview)"),
		m("us.amazon.nova-2-lite-v1:0", "Amazon Nova 2 Lite"),
		m("us.amazon.nova-pro-v1:0", "Amazon Nova Pro"),
		m("us.amazon.nova-lite-v1:0", "Amazon Nova Lite"),
		m("us.amazon.nova-micro-v1:0", "Amazon Nova Micro"),
		// Meta Llama.
		m("us.meta.llama4-maverick-17b-instruct-v1:0", "Llama 4 Maverick 17B"),
		m("us.meta.llama4-scout-17b-instruct-v1:0", "Llama 4 Scout 17B"),
		m("us.meta.llama3-3-70b-instruct-v1:0", "Llama 3.3 70B"),
		// Mistral.
		m("mistral.mistral-large-3-675b-instruct", "Mistral Large 3"),
		m("us.mistral.pixtral-large-2502-v1:0", "Pixtral Large"),
		m("mistral.mistral-large-2407-v1:0", "Mistral Large (24.07)"),
		m("mistral.devstral-2-123b", "Devstral 2"),
		m("mistral.magistral-small-2509", "Magistral Small"),
		// DeepSeek.
		m("us.deepseek.r1-v1:0", "DeepSeek R1"),
		m("us.deepseek.v3.2", "DeepSeek V3.2"),
		// OpenAI open-weight and GPT models.
		m("openai.gpt-oss-120b-1:0", "gpt-oss 120B"),
		m("openai.gpt-oss-20b-1:0", "gpt-oss 20B"),
		m("us.openai.gpt-5.5", "GPT-5.5"),
		// Qwen.
		m("qwen.qwen3-235b-a22b-2507-v1:0", "Qwen3 235B A22B"),
		m("qwen.qwen3-coder-480b-a35b-v1:0", "Qwen3 Coder 480B"),
		m("qwen.qwen3-32b-v1:0", "Qwen3 32B"),
		// Others.
		m("cohere.command-r-plus-v1:0", "Cohere Command R+"),
		m("moonshotai.kimi-k2.5", "Kimi K2.5"),
		m("zai.glm-5", "GLM-5"),
		m("minimax.minimax-m2.5", "MiniMax M2.5"),
		// Embeddings (InvokeModel).
		emb("amazon.titan-embed-text-v2:0", "Titan Text Embeddings V2", 1024),
		emb("amazon.titan-embed-text-v1", "Titan Text Embeddings", 1536),
		emb("cohere.embed-v4:0", "Cohere Embed v4", 1536),
		emb("cohere.embed-english-v3", "Cohere Embed English v3", 1024),
		emb("cohere.embed-multilingual-v3", "Cohere Embed Multilingual v3", 1024),
	}
}

// bedrockMaxProfilePages bounds ListInferenceProfiles pagination.
const bedrockMaxProfilePages = 10

type bedrockFoundationModel struct {
	ModelID                    string   `json:"modelId"`
	ModelName                  string   `json:"modelName"`
	ProviderName               string   `json:"providerName"`
	OutputModalities           []string `json:"outputModalities"`
	InferenceTypesSupported    []string `json:"inferenceTypesSupported"`
	ResponseStreamingSupported *bool    `json:"responseStreamingSupported"`
	ModelLifecycle             *struct {
		Status string `json:"status"`
	} `json:"modelLifecycle"`
}

type bedrockInferenceProfile struct {
	ID     string `json:"inferenceProfileId"`
	Name   string `json:"inferenceProfileName"`
	ARN    string `json:"inferenceProfileArn"`
	Status string `json:"status"`
	Type   string `json:"type"`
	Models []struct {
		ModelARN string `json:"modelArn"`
	} `json:"models"`
}

// ListModels discovers the account's models from the control plane:
// on-demand foundation models (text and embedding output) plus the system
// and application inference profiles, which serve the newer models that are
// not invocable on demand by their bare id.
func (c *Bedrock) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	ctx = egressCtx(ctx, creds)
	base, err := c.controlBase(creds)
	if err != nil {
		return nil, err
	}
	raw, _, err := c.call(ctx, creds, http.MethodGet, base+"/foundation-models", nil, "list-models")
	if err != nil {
		return nil, err
	}
	var fm struct {
		ModelSummaries []bedrockFoundationModel `json:"modelSummaries"`
	}
	if err := json.Unmarshal(raw, &fm); err != nil {
		return nil, fmt.Errorf("bedrock: parse foundation models: %w", err)
	}

	// kindOf records each foundation model's output kind so profiles, which
	// only reference model ARNs, can be classified.
	kindOf := map[string]core.ServiceKind{}
	var out []ModelSpec
	seen := map[string]bool{}
	for _, s := range fm.ModelSummaries {
		kind, ok := bedrockModelKind(s.OutputModalities)
		if !ok {
			continue
		}
		kindOf[s.ModelID] = kind
		if s.ModelLifecycle != nil && s.ModelLifecycle.Status != "" && s.ModelLifecycle.Status != "ACTIVE" {
			continue
		}
		if !bedrockHas(s.InferenceTypesSupported, "ON_DEMAND") || seen[s.ModelID] {
			continue
		}
		seen[s.ModelID] = true
		out = append(out, ModelSpec{ID: s.ModelID, Name: strings.TrimSpace(s.ProviderName + " " + s.ModelName), Kind: kind})
	}

	profiles, err := c.listInferenceProfiles(ctx, creds, base)
	if err != nil {
		// Profiles need their own IAM permission; the on-demand list
		// stands alone when that is missing.
		if len(out) > 0 {
			return out, nil
		}
		return nil, err
	}
	for _, p := range profiles {
		if p.Status != "" && p.Status != "ACTIVE" {
			continue
		}
		id := p.ID
		if p.Type == "APPLICATION" && p.ARN != "" {
			id = p.ARN
		}
		if id == "" || seen[id] {
			continue
		}
		kind := core.ServiceLLM
		known := false
		for _, pm := range p.Models {
			if i := strings.LastIndex(pm.ModelARN, "foundation-model/"); i >= 0 {
				if k, ok := kindOf[pm.ModelARN[i+len("foundation-model/"):]]; ok {
					kind, known = k, true
					break
				}
			}
		}
		if !known && len(kindOf) > 0 && len(p.Models) > 0 && p.Type != "APPLICATION" {
			// The underlying model is not a text or embedding model (image,
			// video, speech): not routable here.
			continue
		}
		seen[id] = true
		out = append(out, ModelSpec{ID: id, Name: bedrockFirstNonEmpty(p.Name, id), Kind: kind})
	}
	return out, nil
}

func (c *Bedrock) listInferenceProfiles(ctx context.Context, creds core.Credentials, base string) ([]bedrockInferenceProfile, error) {
	var all []bedrockInferenceProfile
	for _, typ := range []string{"SYSTEM_DEFINED", "APPLICATION"} {
		next := ""
		for page := 0; page < bedrockMaxProfilePages; page++ {
			q := url.Values{"maxResults": {"1000"}, "typeEquals": {typ}}
			if next != "" {
				q.Set("nextToken", next)
			}
			raw, _, err := c.call(ctx, creds, http.MethodGet, base+"/inference-profiles?"+q.Encode(), nil, "list-models")
			if err != nil {
				return nil, err
			}
			var resp struct {
				Summaries []bedrockInferenceProfile `json:"inferenceProfileSummaries"`
				NextToken string                    `json:"nextToken"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return nil, fmt.Errorf("bedrock: parse inference profiles: %w", err)
			}
			all = append(all, resp.Summaries...)
			if resp.NextToken == "" {
				break
			}
			next = resp.NextToken
		}
	}
	return all, nil
}

// bedrockModelKind classifies a foundation model by its output modalities.
func bedrockModelKind(outputs []string) (core.ServiceKind, bool) {
	switch {
	case bedrockHas(outputs, "TEXT"):
		return core.ServiceLLM, true
	case bedrockHas(outputs, "EMBEDDING"):
		return core.ServiceEmbedding, true
	}
	return "", false
}

func bedrockHas(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
