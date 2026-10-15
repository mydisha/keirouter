package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// OCI drives Oracle Cloud Infrastructure Generative AI. Requests are signed
// per call with the account's API signing key (see oci_sign.go); there is no
// token exchange. Chat goes to the regional inference endpoint, model
// discovery and credential validation to the regional control plane.
type OCI struct {
	id          string
	defaultBase string
}

const (
	ociAPIVersion    = "20231130"
	ociDefaultBase   = "https://inference.generativeai.{oci_region}.oci.oraclecloud.com"
	ociDefaultRegion = "us-chicago-1"
)

// ociRegionRE guards the region before it is interpolated into a hostname.
var ociRegionRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

// ociRealmDomains maps the realm key embedded in an OCID (ocid1.<type>.<realm>.)
// to that realm's second-level domain. Commercial (oc1) is the default.
var ociRealmDomains = map[string]string{
	"oc1": "oraclecloud.com", "oc2": "oraclegovcloud.com", "oc3": "oraclegovcloud.com",
	"oc4": "oraclegovcloud.uk", "oc8": "oraclecloud8.com", "oc9": "oraclecloud9.com",
	"oc10": "oraclecloud10.com", "oc14": "oraclecloud14.com", "oc15": "oraclecloud15.com",
	"oc19": "oraclecloud.eu", "oc20": "oraclecloud20.com", "oc21": "oraclecloud21.com",
	"oc23": "oraclecloud23.com", "oc24": "oraclecloud24.com", "oc26": "oraclecloud26.com",
	"oc29": "oraclecloud29.com", "oc35": "oraclecloud35.com", "oc42": "oraclecloud42.com",
	"oc51": "oraclecloud51.com", "oc52": "oraclecloud52.com",
}

func init() {
	registerDialect(core.DialectOCI, func(id, base string) core.Connector { return NewOCI(id, base) })
	providerModels["oci"] = []ModelSpec{
		m("meta.llama-4-maverick-17b-128e-instruct-fp8", "Llama 4 Maverick 17B 128E"),
		m("meta.llama-4-scout-17b-16e-instruct", "Llama 4 Scout 17B 16E"),
		m("meta.llama-3.3-70b-instruct", "Llama 3.3 70B Instruct"),
		m("meta.llama-3.2-90b-vision-instruct", "Llama 3.2 90B Vision"),
		m("meta.llama-3.1-405b-instruct", "Llama 3.1 405B Instruct"),
		m("xai.grok-4", "Grok 4"),
		m("xai.grok-4-fast", "Grok 4 Fast"),
		m("xai.grok-code-fast-1", "Grok Code Fast 1"),
		m("xai.grok-3", "Grok 3"),
		m("xai.grok-3-mini", "Grok 3 Mini"),
		m("openai.gpt-5", "GPT-5"),
		m("openai.gpt-5-mini", "GPT-5 mini"),
		m("openai.gpt-oss-120b", "gpt-oss-120b"),
		m("openai.gpt-oss-20b", "gpt-oss-20b"),
		m("google.gemini-2.5-pro", "Gemini 2.5 Pro"),
		m("google.gemini-2.5-flash", "Gemini 2.5 Flash"),
		m("google.gemini-2.5-flash-lite", "Gemini 2.5 Flash-Lite"),
		m("cohere.command-a-03-2025", "Command A"),
		m("cohere.command-a-reasoning-08-2025", "Command A Reasoning"),
		m("cohere.command-a-vision-07-2025", "Command A Vision"),
		m("cohere.command-r-plus-08-2024", "Command R+ (08-2024)"),
		m("cohere.command-r-08-2024", "Command R (08-2024)"),
	}
}

// NewOCI builds an OCI Generative AI connector.
func NewOCI(id, defaultBaseURL string) *OCI {
	if defaultBaseURL == "" {
		defaultBaseURL = ociDefaultBase
	}
	return &OCI{id: id, defaultBase: defaultBaseURL}
}

func (c *OCI) ID() string            { return c.id }
func (c *OCI) Dialect() core.Dialect { return core.DialectOCI }

// ociActionSuffix matches a full action path pasted into a base URL override.
var ociActionSuffix = regexp.MustCompile(`/` + ociAPIVersion + `/actions/[^/?#]+/?$`)

// inferenceBase resolves the inference endpoint: an explicit base URL wins,
// otherwise the region (validated) and the realm (from the compartment or
// tenancy OCID) fill the default template.
func (c *OCI) inferenceBase(creds core.Credentials) (string, error) {
	if creds.BaseURL != "" {
		return strings.TrimRight(ociActionSuffix.ReplaceAllString(creds.BaseURL, ""), "/"), nil
	}
	region := extraOr(creds, "oci_region", ociDefaultRegion)
	if !ociRegionRE.MatchString(region) {
		return "", &core.ProviderError{Kind: core.ErrBadRequest, Scope: core.FailureScopeAccount, Provider: c.id,
			Message: fmt.Sprintf("invalid OCI region %q (expected e.g. us-chicago-1)", region)}
	}
	base := strings.ReplaceAll(c.defaultBase, "{oci_region}", region)
	base = resolveURLPlaceholders(base, creds.Extra)
	if domain := ociRealmDomain(extraOr(creds, "oci_compartment_id", extraValue(creds, "oci_tenancy"))); domain != "" &&
		domain != "oraclecloud.com" {
		base = strings.Replace(base, ".oraclecloud.com", "."+domain, 1)
	}
	return strings.TrimRight(base, "/"), nil
}

func ociRealmDomain(ocid string) string {
	parts := strings.SplitN(strings.ToLower(ocid), ".", 4)
	if len(parts) < 3 || parts[0] != "ocid1" {
		return ""
	}
	return ociRealmDomains[parts[2]]
}

// controlBase derives the control-plane endpoint (model listing) from the
// inference endpoint: the same host without the "inference." label.
func (c *OCI) controlBase(creds core.Credentials) (string, error) {
	base, err := c.inferenceBase(creds)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", &core.ProviderError{Kind: core.ErrBadRequest, Provider: c.id, Message: "invalid OCI base URL"}
	}
	u.Host = strings.TrimPrefix(u.Host, "inference.")
	return strings.TrimRight(u.String(), "/"), nil
}

// ociServing chooses on-demand (model name or OCID) or dedicated (endpoint
// OCID) serving. A model that is itself an endpoint OCID is always dedicated.
func ociServing(creds core.Credentials, model string) ociServingMode {
	mode := strings.ToUpper(extraOr(creds, "oci_serving_mode", "ON_DEMAND"))
	if mode == "DEDICATED" || strings.HasPrefix(strings.ToLower(model), "ocid1.generativeaiendpoint.") {
		return ociServingMode{ServingType: "DEDICATED", EndpointID: model}
	}
	return ociServingMode{ServingType: "ON_DEMAND", ModelID: model}
}

// prepare renders and signs a chat call, returning the URL, body and headers.
func (c *OCI) prepare(req *core.ChatRequest, creds core.Credentials, stream bool) (string, []byte, map[string]string, error) {
	signer, err := ociSignerFor(c.id, creds)
	if err != nil {
		return "", nil, nil, c.attribute(err, req.Model)
	}
	compartment := extraValue(creds, "oci_compartment_id")
	if compartment == "" {
		return "", nil, nil, &core.ProviderError{Kind: core.ErrAuth, Scope: core.FailureScopeAccount, Provider: c.id,
			Model: req.Model, Message: "missing OCI compartment OCID"}
	}
	base, err := c.inferenceBase(creds)
	if err != nil {
		return "", nil, nil, c.attribute(err, req.Model)
	}
	body, err := renderOCIChat(req, compartment, ociServing(creds, req.Model), ociAPIFormat(req.Model), stream)
	if err != nil {
		return "", nil, nil, c.attribute(err, req.Model)
	}
	endpoint := base + "/" + ociAPIVersion + "/actions/chat"
	signed, err := ociSignRequest(http.MethodPost, endpoint, body, signer, time.Now())
	if err != nil {
		return "", nil, nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return endpoint, body, mergeHeaders(creds.Headers, signed), nil
}

func (c *OCI) attribute(err error, model string) error {
	pe := core.AsProviderError(err)
	cp := *pe
	if cp.Provider == "" {
		cp.Provider = c.id
	}
	if cp.Model == "" {
		cp.Model = model
	}
	return &cp
}

// Chat performs a non-streaming chat call.
func (c *OCI) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	endpoint, body, headers, err := c.prepare(req, creds, false)
	if err != nil {
		return nil, err
	}
	respBody, err := doJSON(ctx, c.id, req.Model, endpoint, body, headers)
	if err != nil {
		return nil, err
	}
	resp, err := parseOCIChatResponse(respBody, req.Model)
	if err != nil {
		var pe *core.ProviderError
		if errors.As(err, &pe) {
			return nil, c.attribute(pe, req.Model)
		}
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// Stream performs a streaming chat call (isStream=true, SSE frames).
func (c *OCI) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	endpoint, body, headers, err := c.prepare(req, creds, true)
	if err != nil {
		return nil, err
	}
	resp, err := openStream(ctx, c.id, req.Model, endpoint, body, headers)
	if err != nil {
		return nil, err
	}
	out := make(chan core.StreamChunk, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		state := &ociStreamState{}
		pump := newSSEPump(ctx, c.id, req.Model, out, cfg)
		pump.run(resp.Body, sseDataLines, state.parse)
		if pump.errored || ctx.Err() != nil {
			return
		}
		pump.emitAll(state.tail())
	}()
	return out, nil
}

// signedGET issues a signed GET and returns the body and response headers.
func (c *OCI) signedGET(ctx context.Context, model, endpoint string, signer ociSigner) ([]byte, http.Header, error) {
	signed, err := ociSignRequest(http.MethodGet, endpoint, nil, signer, time.Now())
	if err != nil {
		return nil, nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: model, Message: err.Error(), Cause: err}
	}
	resp, err := sendRequest(ctx, c.id, model, jsonRequest(ctx, http.MethodGet, endpoint, nil, mergeHeaders(signed, map[string]string{"accept": "application/json"})))
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := readBodyLimited(c.id, model, resp)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, nil, httpStatusError(c.id, model, resp, b)
	}
	return b, resp.Header, nil
}

// modelsURL builds a control-plane ListModels URL for chat-capable models.
func (c *OCI) modelsURL(creds core.Credentials, limit int, page string) (string, error) {
	base, err := c.controlBase(creds)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("compartmentId", extraValue(creds, "oci_compartment_id"))
	q.Set("capability", "CHAT")
	q.Set("limit", fmt.Sprint(limit))
	if page != "" {
		q.Set("page", page)
	}
	return base + "/" + ociAPIVersion + "/models?" + q.Encode(), nil
}

// Validate lists one chat model through the control plane with a signed
// request: a rejected key or signature answers 401, an unknown compartment
// or missing IAM policy 404.
func (c *OCI) Validate(ctx context.Context, creds core.Credentials) error {
	signer, err := ociSignerFor(c.id, creds)
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	if extraValue(creds, "oci_compartment_id") == "" {
		return fmt.Errorf("validation failed for %s: missing OCI compartment OCID", c.id)
	}
	endpoint, err := c.modelsURL(creds, 1, "")
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	if _, _, err := c.signedGET(egressCtx(ctx, creds), "validate", endpoint, signer); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}

// ociModelSummary is one entry of the control-plane model collection.
type ociModelSummary struct {
	ID                  string     `json:"id"`
	DisplayName         string     `json:"displayName"`
	Vendor              string     `json:"vendor"`
	Type                string     `json:"type"`
	LifecycleState      string     `json:"lifecycleState"`
	Capabilities        []string   `json:"capabilities"`
	TimeOnDemandRetired *time.Time `json:"timeOnDemandRetired"`
}

// ociMaxModelPages caps pagination of the model listing.
const ociMaxModelPages = 10

// ListModels returns the active, on-demand chat models of the account's
// region. The chat API accepts a base model's display name (for example
// meta.llama-3.3-70b-instruct) as the on-demand modelId, so that is the id
// returned. Fine-tuned (CUSTOM) models need a dedicated endpoint and are
// skipped, as are models already retired from on-demand serving.
func (c *OCI) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	signer, err := ociSignerFor(c.id, creds)
	if err != nil {
		return nil, err
	}
	if extraValue(creds, "oci_compartment_id") == "" {
		return nil, fmt.Errorf("missing OCI compartment OCID")
	}
	ctx = egressCtx(ctx, creds)
	now := time.Now()
	seen := map[string]bool{}
	var out []ModelSpec
	page := ""
	for i := 0; i < ociMaxModelPages; i++ {
		endpoint, err := c.modelsURL(creds, 100, page)
		if err != nil {
			return nil, err
		}
		body, hdr, err := c.signedGET(ctx, "models", endpoint, signer)
		if err != nil {
			return nil, err
		}
		var coll struct {
			Items []ociModelSummary `json:"items"`
		}
		if err := json.Unmarshal(body, &coll); err != nil {
			return nil, fmt.Errorf("decode OCI model list: %w", err)
		}
		for _, it := range coll.Items {
			name := it.DisplayName
			switch {
			case name == "" || seen[name]:
				continue
			case it.LifecycleState != "" && !strings.EqualFold(it.LifecycleState, "ACTIVE"):
				continue
			case strings.EqualFold(it.Type, "CUSTOM"):
				continue
			case it.TimeOnDemandRetired != nil && it.TimeOnDemandRetired.Before(now):
				continue
			}
			seen[name] = true
			out = append(out, ModelSpec{ID: name, Name: name, Kind: core.ServiceLLM})
		}
		page = hdr.Get("opc-next-page")
		if page == "" {
			break
		}
	}
	return out, nil
}
