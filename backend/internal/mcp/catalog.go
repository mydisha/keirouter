package mcp

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Preset prefills the "add server" form. A server created from a preset is an
// ordinary server row; nothing at request time depends on the preset.
type Preset struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Category    string `json:"category" yaml:"category"`
	// URL may contain {key} placeholders filled from Vars.
	URL string `json:"url" yaml:"url"`
	// AuthTypes lists the auth types the server accepts, the default first.
	AuthTypes      []string    `json:"auth_types" yaml:"auth_types"`
	AuthHeader     string      `json:"auth_header,omitempty" yaml:"auth_header"`
	SecretLabel    string      `json:"secret_label,omitempty" yaml:"secret_label"`
	SecretHelpURL  string      `json:"secret_help_url,omitempty" yaml:"secret_help_url"`
	SecretOptional bool        `json:"secret_optional,omitempty" yaml:"secret_optional"`
	Vars           []PresetVar `json:"vars,omitempty" yaml:"vars"`
	// StaticHeaders are non-secret headers every request carries.
	StaticHeaders map[string]string `json:"static_headers,omitempty" yaml:"static_headers"`
	Notes         string            `json:"notes,omitempty" yaml:"notes"`
	DocsURL       string            `json:"docs_url,omitempty" yaml:"docs_url"`
	Source        string            `json:"source" yaml:"-"`
}

// PresetVar is one placeholder in a preset URL.
type PresetVar struct {
	Key      string         `json:"key" yaml:"key"`
	Label    string         `json:"label" yaml:"label"`
	Type     string         `json:"type" yaml:"type"` // text | select | bool
	Options  []PresetOption `json:"options,omitempty" yaml:"options"`
	Default  string         `json:"default,omitempty" yaml:"default"`
	Required bool           `json:"required,omitempty" yaml:"required"`
	Hint     string         `json:"hint,omitempty" yaml:"hint"`
}

// PresetOption is one choice of a select variable.
type PresetOption struct {
	Value string `json:"value" yaml:"value"`
	Label string `json:"label" yaml:"label"`
}

// Preset sources.
const (
	SourceBuiltin = "builtin"
	SourceCompany = "company"
)

var (
	presetIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	placeholderPattern = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
	textVarValue       = `[A-Za-z0-9._-]+`
)

// Catalog is the merged list of presets: company presets first.
type Catalog struct {
	presets []Preset
	byID    map[string]*Preset
	only    bool
}

// NewCatalog merges built-in presets with the company file (when path is not
// empty). Every company preset is validated; a bad file is an error so the
// operator sees it at startup rather than as a half-empty picker.
func NewCatalog(companyFile string, catalogOnly bool) (*Catalog, error) {
	var company []Preset
	if companyFile != "" {
		var err error
		company, err = loadCompanyPresets(companyFile)
		if err != nil {
			return nil, err
		}
	}
	c := &Catalog{byID: map[string]*Preset{}, only: catalogOnly}
	for _, p := range company {
		p.Source = SourceCompany
		c.presets = append(c.presets, p)
	}
	for _, p := range builtinPresets() {
		p.Source = SourceBuiltin
		if _, clash := c.byIDOf(p.ID); clash {
			continue // a company preset with the same id replaces the built-in one
		}
		c.presets = append(c.presets, p)
	}
	for i := range c.presets {
		c.byID[c.presets[i].ID] = &c.presets[i]
	}
	if catalogOnly && len(c.presets) == 0 {
		return nil, errors.New("mcp.catalog_only is set but the catalog has no presets")
	}
	return c, nil
}

func (c *Catalog) byIDOf(id string) (*Preset, bool) {
	for i := range c.presets {
		if c.presets[i].ID == id {
			return &c.presets[i], true
		}
	}
	return nil, false
}

// Presets returns every preset.
func (c *Catalog) Presets() []Preset { return c.presets }

// CatalogOnly reports whether servers may only be created from presets.
func (c *Catalog) CatalogOnly() bool { return c.only }

// Get returns one preset.
func (c *Catalog) Get(id string) (*Preset, bool) {
	p, ok := c.byID[id]
	return p, ok
}

// MatchesURL reports whether rawURL is this preset's URL with every
// placeholder filled with an allowed value.
func (p *Preset) MatchesURL(rawURL string) bool {
	var re strings.Builder
	re.WriteString("^")
	last := 0
	for _, m := range placeholderPattern.FindAllStringSubmatchIndex(p.URL, -1) {
		re.WriteString(regexp.QuoteMeta(p.URL[last:m[0]]))
		v := p.varByKey(p.URL[m[2]:m[3]])
		re.WriteString(varPattern(v))
		last = m[1]
	}
	re.WriteString(regexp.QuoteMeta(p.URL[last:]))
	re.WriteString("$")
	ok, _ := regexp.MatchString(re.String(), rawURL)
	return ok
}

func (p *Preset) varByKey(key string) *PresetVar {
	for i := range p.Vars {
		if p.Vars[i].Key == key {
			return &p.Vars[i]
		}
	}
	return nil
}

func varPattern(v *PresetVar) string {
	if v == nil {
		return `[^\s\S]` // matches nothing
	}
	switch v.Type {
	case "bool":
		return "(?:true|false)"
	case "select":
		alts := make([]string, len(v.Options))
		for i, o := range v.Options {
			alts[i] = regexp.QuoteMeta(o.Value)
		}
		return "(?:" + strings.Join(alts, "|") + ")"
	default:
		return textVarValue
	}
}

// AllowsAuth reports whether the preset accepts authType.
func (p *Preset) AllowsAuth(authType string) bool { return slices.Contains(p.AuthTypes, authType) }

// RenderURL fills the placeholders with defaults, for validation.
func (p *Preset) RenderURL() string {
	return placeholderPattern.ReplaceAllStringFunc(p.URL, func(m string) string {
		v := p.varByKey(m[1 : len(m)-1])
		switch {
		case v == nil:
			return m
		case v.Default != "":
			return v.Default
		case v.Type == "bool":
			return "false"
		case v.Type == "select" && len(v.Options) > 0:
			return v.Options[0].Value
		default:
			return "example"
		}
	})
}

// Validate checks a preset's fields.
func (p *Preset) Validate() error {
	if !presetIDPattern.MatchString(p.ID) {
		return fmt.Errorf("id %q must be 1-48 characters of a-z, 0-9 and -", p.ID)
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if len(p.AuthTypes) == 0 {
		return errors.New("auth_types is required")
	}
	for _, a := range p.AuthTypes {
		if !ValidAuthType(a) {
			return fmt.Errorf("auth type %q is not one of none, bearer, header, basic", a)
		}
		if a == AuthHeader && !IsToken(p.AuthHeader) {
			return errors.New("auth_header must be a valid header name when auth type header is allowed")
		}
	}
	keys := map[string]bool{}
	for _, v := range p.Vars {
		if keys[v.Key] {
			return fmt.Errorf("variable %q is declared twice", v.Key)
		}
		keys[v.Key] = true
		switch v.Type {
		case "text", "bool":
		case "select":
			if len(v.Options) == 0 {
				return fmt.Errorf("variable %q is a select without options", v.Key)
			}
			if v.Default != "" && !slices.ContainsFunc(v.Options, func(o PresetOption) bool { return o.Value == v.Default }) {
				return fmt.Errorf("variable %q default %q is not one of its options", v.Key, v.Default)
			}
		default:
			return fmt.Errorf("variable %q has type %q; use text, select or bool", v.Key, v.Type)
		}
	}
	for _, m := range placeholderPattern.FindAllStringSubmatch(p.URL, -1) {
		if !keys[m[1]] {
			return fmt.Errorf("url placeholder {%s} has no variable", m[1])
		}
	}
	u, err := url.Parse(p.RenderURL())
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("url %q is not an http(s) URL", p.URL)
	}
	for k, v := range p.StaticHeaders {
		if err := ValidateStaticHeader(k, v); err != nil {
			return err
		}
	}
	return nil
}

// ValidAuthType reports whether a is a supported upstream auth type.
func ValidAuthType(a string) bool {
	return a == AuthNone || a == AuthBearer || a == AuthHeader || a == AuthBasic
}

// headers the operator may not set or forward: they belong to the transport,
// the gateway's own auth, or the protocol.
var reservedHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "x-api-key": true, "cookie": true,
	"host": true, "content-length": true, "content-type": true, "accept": true,
	"connection": true, "transfer-encoding": true, "te": true, "upgrade": true, "trailer": true,
	"keep-alive": true, "mcp-session-id": true, "mcp-protocol-version": true, "mcp-method": true, "mcp-name": true,
}

// IsReservedHeader reports whether name may not be configured as a static or
// forwarded header.
func IsReservedHeader(name string) bool {
	l := strings.ToLower(name)
	return reservedHeaders[l] || strings.HasPrefix(l, "mcp-param-") || strings.HasPrefix(l, "proxy-")
}

// ValidateStaticHeader checks one static header.
func ValidateStaticHeader(name, value string) error {
	if !IsToken(name) {
		return fmt.Errorf("header name %q is not valid", name)
	}
	if IsReservedHeader(name) {
		return fmt.Errorf("header %q cannot be set here", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("header %q value contains a line break", name)
	}
	return nil
}

func loadCompanyPresets(path string) ([]Preset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mcp.catalog_file: %w", err)
	}
	var doc struct {
		Presets []Preset `yaml:"presets"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("mcp.catalog_file %s: %w", path, err)
	}
	seen := map[string]bool{}
	for i := range doc.Presets {
		p := &doc.Presets[i]
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("mcp.catalog_file %s: preset %d (%s): %w", path, i+1, p.ID, err)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("mcp.catalog_file %s: preset id %q appears twice", path, p.ID)
		}
		seen[p.ID] = true
	}
	return doc.Presets, nil
}

// builtinPresets are remote MCP servers that accept a static credential. Each
// entry was checked against the vendor's documentation on 2026-11-10; servers
// that only accept OAuth are left out until upstream OAuth exists.
func builtinPresets() []Preset {
	readOnly := func(readValue, fullValue string) PresetVar {
		return PresetVar{Key: "access", Label: "Access", Type: "select", Default: readValue,
			Options: []PresetOption{{Value: readValue, Label: "Read-only"}, {Value: fullValue, Label: "Read and write"}},
			Hint:    "Every API key allowed to use this server shares one credential, so read-only is the safer default."}
	}
	return []Preset{
		{
			ID: "github", Name: "GitHub", Category: "Code and projects",
			Description: "Repositories, issues, pull requests and Actions.",
			URL:         "https://api.githubcopilot.com/mcp/{access}",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "Personal access token",
			SecretHelpURL: "https://github.com/settings/personal-access-tokens",
			Vars:          []PresetVar{readOnly("readonly", "")},
			DocsURL:       "https://docs.github.com/en/copilot/how-tos/provide-context/use-mcp/use-the-github-mcp-server",
		},
		{
			ID: "linear", Name: "Linear", Category: "Code and projects",
			Description: "Issues, projects and comments.",
			URL:         "https://mcp.linear.app/mcp{access}",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "API key",
			SecretHelpURL: "https://linear.app/settings/account/security",
			Vars:          []PresetVar{readOnly("/readonly", "")},
			DocsURL:       "https://linear.app/docs/mcp",
		},
		{
			ID: "atlassian", Name: "Atlassian", Category: "Code and projects",
			Description: "Jira and Confluence.",
			URL:         "https://mcp.atlassian.com/v2/mcp",
			AuthTypes:   []string{AuthBearer, AuthBasic}, SecretLabel: "Service account API key",
			Notes:   "An organization admin must turn on API token authentication. Bearer takes a service account key; Basic takes your email and a personal API token.",
			DocsURL: "https://support.atlassian.com/atlassian-rovo-mcp-server/docs/configuring-authentication-via-api-token/",
		},
		{
			ID: "context7", Name: "Context7", Category: "Documentation",
			Description: "Current library documentation and code examples.",
			URL:         "https://mcp.context7.com/mcp",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "API key", SecretOptional: true,
			Notes:   "Works without a key. A key raises rate limits and adds private repositories.",
			DocsURL: "https://context7.com/docs/resources/all-clients",
		},
		{
			ID: "deepwiki", Name: "DeepWiki", Category: "Documentation",
			Description: "Documentation generated from public GitHub repositories.",
			URL:         "https://mcp.deepwiki.com/mcp",
			AuthTypes:   []string{AuthNone},
			DocsURL:     "https://docs.devin.ai/work-with-devin/deepwiki-mcp",
		},
		{
			ID: "microsoft-learn", Name: "Microsoft Learn", Category: "Documentation",
			Description: "Microsoft and Azure documentation and code samples.",
			URL:         "https://learn.microsoft.com/api/mcp",
			AuthTypes:   []string{AuthNone},
			DocsURL:     "https://learn.microsoft.com/en-us/training/support/mcp",
		},
		{
			ID: "aws-knowledge", Name: "AWS Knowledge", Category: "Documentation",
			Description: "AWS documentation, guidance and regional availability.",
			URL:         "https://knowledge-mcp.global.api.aws",
			AuthTypes:   []string{AuthNone},
			Notes:       "Rate limited.",
			DocsURL:     "https://awslabs.github.io/mcp/servers/aws-knowledge-mcp-server",
		},
		{
			ID: "exa", Name: "Exa", Category: "Web and research",
			Description: "Web search and page fetching.",
			URL:         "https://mcp.exa.ai/mcp",
			AuthTypes:   []string{AuthHeader}, AuthHeader: "x-api-key", SecretLabel: "API key", SecretOptional: true,
			SecretHelpURL: "https://dashboard.exa.ai/api-keys",
			Notes:         "Search and fetch work without a key at a free rate limit.",
			DocsURL:       "https://docs.exa.ai/reference/exa-mcp",
		},
		{
			ID: "firecrawl", Name: "Firecrawl", Category: "Web and research",
			Description: "Scraping, crawling and structured extraction.",
			URL:         "https://mcp.firecrawl.dev/v2/mcp",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "API key",
			SecretHelpURL: "https://www.firecrawl.dev/app/api-keys",
			DocsURL:       "https://docs.firecrawl.dev/mcp-server",
		},
		{
			ID: "supabase", Name: "Supabase", Category: "Databases",
			Description: "Database, schema, logs and edge functions of one project.",
			URL:         "https://mcp.supabase.com/mcp?project_ref={project_ref}&read_only={read_only}",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "Personal access token",
			SecretHelpURL: "https://supabase.com/dashboard/account/tokens",
			Vars: []PresetVar{
				{Key: "project_ref", Label: "Project ref", Type: "text", Required: true, Hint: "From the project URL: supabase.com/dashboard/project/<ref>."},
				{Key: "read_only", Label: "Read-only", Type: "bool", Default: "true", Hint: "Runs every query as a read-only Postgres user."},
			},
			DocsURL: "https://supabase.com/docs/guides/getting-started/mcp",
		},
		{
			ID: "neon", Name: "Neon", Category: "Databases",
			Description: "Neon Postgres projects, branches and queries.",
			URL:         "https://mcp.neon.tech/mcp",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "API key",
			SecretHelpURL: "https://console.neon.tech/app/settings/api-keys",
			DocsURL:       "https://neon.com/docs/ai/neon-mcp-server",
		},
		{
			ID: "stripe", Name: "Stripe", Category: "Business and observability",
			Description: "Customers, payments, subscriptions and Stripe docs.",
			URL:         "https://mcp.stripe.com",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "Agent API key",
			SecretHelpURL: "https://dashboard.stripe.com/apikeys",
			Notes:         "Use an agent API key. From December 2026 Stripe rejects secret and restricted keys without the Agent tag.",
			DocsURL:       "https://docs.stripe.com/mcp",
		},
		{
			ID: "datadog", Name: "Datadog", Category: "Business and observability",
			Description: "Logs, metrics, monitors, incidents and dashboards.",
			URL:         "https://mcp.{site}/v1/mcp",
			AuthTypes:   []string{AuthBearer}, SecretLabel: "Personal or service access token",
			Vars: []PresetVar{{Key: "site", Label: "Datadog site", Type: "select", Default: "datadoghq.com", Options: []PresetOption{
				{Value: "datadoghq.com", Label: "US1"},
				{Value: "us3.datadoghq.com", Label: "US3"},
				{Value: "us5.datadoghq.com", Label: "US5"},
				{Value: "datadoghq.eu", Label: "EU1"},
				{Value: "ap1.datadoghq.com", Label: "AP1"},
				{Value: "ap2.datadoghq.com", Label: "AP2"},
				{Value: "uk1.datadoghq.com", Label: "UK1"},
			}}},
			DocsURL: "https://docs.datadoghq.com/bits_ai/mcp_server/setup/",
		},
	}
}
