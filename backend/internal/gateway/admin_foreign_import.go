package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// foreignImportMaxBytes caps 9router/OmniRoute backup JSON uploads.
// Larger than chat maxBodyBytes because full router exports can include
// many accounts, combos, and provider nodes in one document.
const foreignImportMaxBytes = 64 << 20 // 64 MiB

// Foreign config import: convert a 9router (or OmniRoute) backup JSON into
// KeiRouter's native data model. Credentials are re-sealed under the local
// master key, API keys are re-hashed with argon2id (the same plaintext keeps
// working), and routing combos become chains.
//
// 9router and OmniRoute share the same lineage as KeiRouter, so their provider
// ids and combo model-string format ("alias/model") are largely compatible.
// The import is additive: existing records are left alone and conflicts are
// skipped (best-effort), mirroring the native database import handler.

// foreignImportResult is the response body for the foreign import endpoint.
type foreignImportResult struct {
	Source          string   `json:"source"`
	Imported        int      `json:"imported"`
	Skipped         int      `json:"skipped"`
	Accounts        int      `json:"accounts"`
	CustomProviders int      `json:"custom_providers"`
	APIKeys         int      `json:"api_keys"`
	Chains          int      `json:"chains"`
	Aliases         int      `json:"aliases"`
	ProxyPools      int      `json:"proxy_pools"`
	UsageRecords    int      `json:"usage_records,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

// adminImportForeignConfig converts a foreign router backup into KeiRouter
// records. It accepts a JSON body:
//
//	{ "source": "9router" | "omniroute", "config": { ...foreign payload... } }
//
// The handler auto-detects the source when omitted by inspecting the payload
// shape, so callers may also POST the raw foreign payload directly.
func (s *Server) adminImportForeignConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, foreignImportMaxBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("import body too large: max %d MiB", foreignImportMaxBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	source, payload, derr := decodeForeignPayload(bodyBytes)
	if derr != nil {
		writeError(w, http.StatusBadRequest, derr.Error())
		return
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config JSON: "+err.Error())
		return
	}

	if source == "" {
		source = detectForeignSource(doc)
	}

	res := &foreignImportResult{Source: source}
	switch source {
	case "9router":
		s.importN9router(ctx, doc, res)
	case "omniroute", "omni":
		s.importOmniRoute(ctx, doc, res)
	default:
		writeError(w, http.StatusBadRequest, "unknown source: "+source+" (expected '9router' or 'omniroute')")
		return
	}

	res.Imported = res.Accounts + res.CustomProviders + res.APIKeys + res.Chains + res.Aliases + res.ProxyPools
	writeJSON(w, http.StatusOK, res)
}

// decodeForeignPayload accepts either the wrapped form {"source":..,"config":..}
// or a raw foreign payload posted directly. It returns the source label and the
// config bytes.
func decodeForeignPayload(b []byte) (string, json.RawMessage, error) {
	var wrapped struct {
		Source string          `json:"source"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(b, &wrapped); err == nil && len(wrapped.Config) > 0 {
		return strings.ToLower(strings.TrimSpace(wrapped.Source)), wrapped.Config, nil
	}
	// Treat the body as a raw foreign payload.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return "", nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return "", b, nil
}

// detectForeignSource inspects the payload to guess whether it came from
// 9router or OmniRoute. 9router exports a flat object with top-level arrays
// keyed by table name (providerConnections, providerNodes, combos, ...).
// OmniRoute's tar.gz sidecars use snake_case keys (provider_connections,
// provider_nodes) or carry an "omniroute" marker in metadata.
func detectForeignSource(doc map[string]json.RawMessage) string {
	if _, ok := doc["providerConnections"]; ok {
		return "9router"
	}
	if _, ok := doc["provider_connections"]; ok {
		return "omniroute"
	}
	if raw, ok := doc["format"]; ok {
		var format string
		_ = json.Unmarshal(raw, &format)
		if strings.Contains(strings.ToLower(format), "omniroute") {
			return "omniroute"
		}
	}
	// Default to 9router since the issue specifically requests it.
	return "9router"
}

// ── 9router ──────────────────────────────────────────────────────────

func (s *Server) importN9router(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	if s.vault == nil {
		res.Errors = append(res.Errors, "vault not configured: cannot seal imported credentials")
	}

	// Custom provider nodes first so account references resolve.
	nodeIDMap := s.importN9routerNodes(ctx, doc, res)

	// Provider connections (accounts) with plaintext credentials.
	s.importN9routerConnections(ctx, doc, res, nodeIDMap)

	// API keys: re-hash the plaintext so the same key string works.
	s.importN9routerAPIKeys(ctx, doc, res)

	// Combos -> chains.
	s.importN9routerCombos(ctx, doc, res)

	// Proxy pools.
	s.importN9routerProxyPools(ctx, doc, res)

	// Model aliases.
	s.importN9routerAliases(ctx, doc, res)

	// Custom models (user-registered model definitions on any provider).
	s.importN9routerCustomModels(ctx, doc, res)
}

// n9routerNode is the subset of a 9router providerNode entry we read.
type n9routerNode struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"` // openai-compatible | anthropic-compatible | custom-embedding
	Name    string         `json:"name"`
	Prefix  string         `json:"prefix"`
	APIType string         `json:"apiType"`
	BaseURL string         `json:"baseUrl"`
	Data    map[string]any `json:"-"`
}

func (s *Server) importN9routerNodes(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) map[string]string {
	nodeIDMap := map[string]string{} // 9router node id -> keirouter provider id
	raw, ok := doc["providerNodes"]
	if !ok {
		return nodeIDMap
	}
	var nodes []n9routerNode
	if err := json.Unmarshal(raw, &nodes); err != nil {
		res.Errors = append(res.Errors, "providerNodes: "+err.Error())
		return nodeIDMap
	}
	// 9router may embed node fields inside a `data` JSON column. Decode each
	// entry generically to recover baseUrl/prefix when top-level fields absent.
	var generic []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &generic)

	for i, n := range nodes {
		if i < len(generic) {
			if dataRaw, ok := generic[i]["data"]; ok && len(dataRaw) > 0 {
				_ = json.Unmarshal(dataRaw, &n.Data)
				if n.BaseURL == "" {
					n.BaseURL = strVal(n.Data["baseUrl"])
				}
				if n.Prefix == "" {
					n.Prefix = strVal(n.Data["prefix"])
				}
				if n.APIType == "" {
					n.APIType = strVal(n.Data["apiType"])
				}
			}
		}

		dialect, prefix, ok := customDialect(nodeDialectToken(n.Type))
		if !ok {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("node %s: unsupported type %q", n.ID, n.Type))
			continue
		}
		if strings.TrimSpace(n.BaseURL) == "" {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("node %s: missing baseUrl", n.ID))
			continue
		}

		name := strings.TrimSpace(n.Name)
		if name == "" {
			name = n.ID
		}
		id := uniqueCustomProviderID(prefix, name, func(candidate string) bool {
			if _, exists := connectors.SpecByID(candidate); exists {
				return true
			}
			if _, dup := s.db.CustomProviders().GetProvider(ctx, candidate); dup == nil {
				return true
			}
			return false
		})

		alias := n.Prefix
		if a, aerr := resolveCustomAlias(alias, name, id); aerr == nil {
			alias = a
		} else {
			alias = id
		}

		p := store.CustomProvider{
			ID:          id,
			TenantID:    adminTenant,
			DisplayName: name,
			Alias:       alias,
			Dialect:     string(dialect),
			BaseURL:     n.BaseURL,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := s.db.CustomProviders().CreateProvider(ctx, p); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("node %s: create custom provider: %v", n.ID, err))
			continue
		}
		connectors.RegisterDynamicProvider(connectors.DynamicProvider{
			ID: p.ID, DisplayName: p.DisplayName, Alias: p.Alias, Dialect: dialect, BaseURL: p.BaseURL,
		})
		nodeIDMap[n.ID] = id
		res.CustomProviders++
	}
	return nodeIDMap
}

func nodeDialectToken(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "openai-compatible":
		return "openai"
	case "anthropic-compatible":
		return "anthropic"
	case "custom-embedding":
		return "openai" // embeddings ride the OpenAI-compatible dialect
	default:
		return t
	}
}

// n9routerConnection mirrors the exported providerConnection shape.
type n9routerConnection struct {
	ID                   string         `json:"id"`
	Provider             string         `json:"provider"`
	AuthType             string         `json:"authType"`
	Name                 string         `json:"name"`
	DisplayName          string         `json:"displayName"`
	Email                string         `json:"email"`
	Priority             int            `json:"priority"`
	IsActive             *bool          `json:"isActive"`
	APIKey               string         `json:"apiKey"`
	AccessToken          string         `json:"accessToken"`
	RefreshToken         string         `json:"refreshToken"`
	ExpiresAt            string         `json:"expiresAt"`
	ProviderSpecificData map[string]any `json:"providerSpecificData"`
	Data                 map[string]any `json:"-"`
}

func (s *Server) importN9routerConnections(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult, nodeIDMap map[string]string) {
	raw, ok := doc["providerConnections"]
	if !ok {
		return
	}
	var conns []n9routerConnection
	if err := json.Unmarshal(raw, &conns); err != nil {
		res.Errors = append(res.Errors, "providerConnections: "+err.Error())
		return
	}
	// Recover fields nested under the `data` JSON column.
	var generic []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &generic)

	for i, c := range conns {
		if i < len(generic) {
			if dataRaw, ok := generic[i]["data"]; ok && len(dataRaw) > 0 {
				_ = json.Unmarshal(dataRaw, &c.Data)
				if c.APIKey == "" {
					c.APIKey = strVal(c.Data["apiKey"])
				}
				if c.AccessToken == "" {
					c.AccessToken = strVal(c.Data["accessToken"])
				}
				if c.RefreshToken == "" {
					c.RefreshToken = strVal(c.Data["refreshToken"])
				}
				if c.ExpiresAt == "" {
					c.ExpiresAt = strVal(c.Data["expiresAt"])
				}
				if c.ProviderSpecificData == nil {
					if psd, ok := c.Data["providerSpecificData"].(map[string]any); ok {
						c.ProviderSpecificData = psd
					}
				}
			}
		}

		provider := mapN9routerProvider(c.Provider, nodeIDMap)
		if provider == "" {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("connection %s: unmapped provider %q", c.ID, c.Provider))
			continue
		}
		spec, specOK := connectors.SpecByID(provider)

		authKind, secret := n9routerAuthSecret(c, spec, specOK)
		if s.vault == nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("connection %s: vault unavailable, skipped", c.ID))
			continue
		}

		label := strings.TrimSpace(c.Name)
		if label == "" {
			label = strings.TrimSpace(c.DisplayName)
		}
		if label == "" {
			if specOK {
				label = spec.DisplayName
			} else {
				label = provider
			}
		}
		// OAuth accounts are best identified by their email.
		if c.Email != "" && specOK && spec.AuthKind == "oauth" {
			label = c.Email
		}

		// Build metadata. providerSpecificData carries provider-specific
		// fields (qoder user_id/machine_id, cursor machine_id/ghost_mode,
		// codex workspaceId, base_url overrides, azure details, ...). These
		// surface as creds.Extra at request time, so they MUST transfer for
		// connectors like Qoder that require user_id to sign requests.
		meta := psdToMetadata(provider, c.ProviderSpecificData)
		// Top-level email is the canonical account email for OAuth providers;
		// ensure it lands in metadata (qoder/cursor/cloudcode read it).
		if c.Email != "" && meta["email"] == "" {
			meta["email"] = c.Email
		}

		now := time.Now()
		acc := store.Account{
			// Deterministic id: re-imports upsert instead of duplicating, and
			// usage_records.account_id (carrying the raw 9router connectionId)
			// resolves to this row.
			ID:        n9IDPrefix + c.ID,
			TenantID:  adminTenant,
			Provider:  provider,
			Label:     label,
			AuthKind:  authKind,
			Priority:  defaultInt(c.Priority, 100),
			Disabled:  c.IsActive != nil && !*c.IsActive,
			CreatedAt: now,
			UpdatedAt: now,
		}
		secret.ExpiresAt = parseRFC3339(c.ExpiresAt)
		secret.Metadata = meta
		if err := s.vault.Seal(&acc, secret); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("connection %s: seal failed: %v", c.ID, err))
			continue
		}
		if err := s.accounts.Create(ctx, acc); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("connection %s: create account: %v", c.ID, err))
			continue
		}
		res.Accounts++
	}
}

// n9routerAuthSecret maps a 9router authType + credentials to a KeiRouter
// AuthKind and a vault.NewSecret carrying the plaintext material.
func n9routerAuthSecret(c n9routerConnection, spec connectors.ProviderSpec, specOK bool) (store.AuthKind, vault.NewSecret) {
	authType := strings.ToLower(strings.TrimSpace(c.AuthType))
	if authType == "" {
		authType = "oauth"
	}
	sec := vault.NewSecret{Metadata: map[string]string{}}

	switch authType {
	case "oauth", "access_token":
		sec.AccessToken = c.AccessToken
		sec.RefreshToken = c.RefreshToken
		// Some OAuth providers (iflow, kimi-coding, xai) also accept an API
		// key; when only an API key is present, treat as api_key instead.
		if c.AccessToken == "" && c.RefreshToken == "" && c.APIKey != "" {
			sec.APIKey = c.APIKey
			return store.AuthAPIKey, sec
		}
		return store.AuthOAuth, sec
	case "apikey", "api_key", "api-key":
		sec.APIKey = c.APIKey
		if sec.APIKey == "" {
			sec.APIKey = c.AccessToken
		}
		if specOK && spec.AuthKind == "none" && c.APIKey == "" {
			return store.AuthNone, sec
		}
		return store.AuthAPIKey, sec
	case "cookie":
		// 9router cookie connections carry the session cookie in apiKey or
		// providerSpecificData. Store it as the secret so the connector can
		// use it; map to api_key auth (cookie-bearing providers use
		// SkipValidation in the catalog).
		sec.APIKey = c.APIKey
		return store.AuthAPIKey, sec
	default:
		if specOK && spec.AuthKind == "none" {
			return store.AuthNone, sec
		}
		sec.APIKey = c.APIKey
		return store.AuthAPIKey, sec
	}
}

func (s *Server) importN9routerAPIKeys(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	raw, ok := doc["apiKeys"]
	if !ok {
		return
	}
	var keys []struct {
		ID       string `json:"id"`
		Key      string `json:"key"`
		Name     string `json:"name"`
		IsActive *bool  `json:"isActive"`
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		res.Errors = append(res.Errors, "apiKeys: "+err.Error())
		return
	}
	for _, k := range keys {
		plaintext := strings.TrimSpace(k.Key)
		if plaintext == "" {
			res.Skipped++
			continue
		}
		name := strings.TrimSpace(k.Name)
		if name == "" {
			name = "imported"
		}
		hash, err := crypto.HashAPIKey(plaintext)
		if err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("key %s: hash failed: %v", k.ID, err))
			continue
		}
		rec := store.APIKey{
			ID:         n9IDPrefix + k.ID,
			TenantID:   adminTenant,
			Name:       name,
			KeyHash:    hash,
			LookupHash: crypto.LookupHash(plaintext),
			Display:    maskForeignKey(plaintext),
			Disabled:   k.IsActive != nil && !*k.IsActive,
			CreatedAt:  time.Now(),
		}
		if err := s.identity.Keys().Create(ctx, rec); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("key %s: create: %v", k.ID, err))
			continue
		}
		res.APIKeys++
	}
}

// maskForeignKey renders a foreign key for display without assuming the
// KeiRouter "kr_" prefix.
func maskForeignKey(plaintext string) string {
	body := plaintext
	for _, p := range []string{"kr_", "sk_", "sk-"} {
		body = strings.TrimPrefix(body, p)
	}
	if len(body) <= 8 {
		return "…"
	}
	prefixLen := len(plaintext) - len(body)
	return plaintext[:prefixLen] + body[:4] + "…" + body[len(body)-4:]
}

func (s *Server) importN9routerCombos(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	raw, ok := doc["combos"]
	if !ok {
		return
	}
	var combos []struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		Strategy string   `json:"strategy"`
		Models   []string `json:"models"`
	}
	if err := json.Unmarshal(raw, &combos); err != nil {
		res.Errors = append(res.Errors, "combos: "+err.Error())
		return
	}
	for _, c := range combos {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			res.Skipped++
			continue
		}
		if verr := validateChainName(name); verr != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("combo %s: %v", c.ID, verr))
			continue
		}
		strategy := mapN9routerStrategy(c.Kind, c.Strategy)
		now := time.Now()
		chain := store.Chain{
			ID:        n9IDPrefix + c.ID,
			TenantID:  adminTenant,
			Name:      name,
			Strategy:  strategy,
			CreatedAt: now,
			UpdatedAt: now,
		}
		pos := 0
		for _, m := range c.Models {
			provider, model := splitAliasModel(m)
			if provider == "" || model == "" {
				res.Skipped++
				continue
			}
			if _, ok := connectors.SpecByAlias(provider); !ok {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("combo %s: unknown provider alias %q", c.ID, provider))
				continue
			}
			chain.Steps = append(chain.Steps, store.ChainStep{
				ID:        uuid.NewString(),
				ChainID:   chain.ID,
				Position:  pos,
				Provider:  provider,
				Model:     model,
				CreatedAt: now,
			})
			pos++
		}
		if len(chain.Steps) == 0 {
			res.Skipped++
			continue
		}
		if err := s.chains.Create(ctx, chain); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("combo %s: create chain: %v", c.ID, err))
			continue
		}
		res.Chains++
	}
}

func mapN9routerStrategy(kind, strategy string) string {
	for _, v := range []string{strategy, kind} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "fallback", "priority":
			return "priority"
		case "roundrobin", "round-robin", "round_robin":
			return "round-robin"
		case "weighted":
			return "weighted"
		case "fill-first", "fillfirst":
			return "fill-first"
		case "random":
			return "random"
		}
	}
	return "priority"
}

func (s *Server) importN9routerProxyPools(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	raw, ok := doc["proxyPools"]
	if !ok {
		return
	}
	var pools []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		ProxyURL string `json:"proxyUrl"`
		NoProxy  string `json:"noProxy"`
		Type     string `json:"type"`
		Strict   bool   `json:"strictProxy"`
		IsActive *bool  `json:"isActive"`
	}
	if err := json.Unmarshal(raw, &pools); err != nil {
		res.Errors = append(res.Errors, "proxyPools: "+err.Error())
		return
	}
	for _, p := range pools {
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.ProxyURL) == "" {
			res.Skipped++
			continue
		}
		if p.ID == "" {
			p.ID = uuid.NewString()
		}
		now := time.Now()
		pool := store.ProxyPool{
			ID:         n9IDPrefix + p.ID,
			Name:       p.Name,
			Type:       defaultStr(p.Type, "http"),
			ProxyURL:   p.ProxyURL,
			NoProxy:    p.NoProxy,
			Strict:     p.Strict,
			IsActive:   p.IsActive == nil || *p.IsActive,
			TestStatus: "unknown",
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := s.pools.Create(ctx, pool); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("proxy pool %s: %v", p.Name, err))
			continue
		}
		res.ProxyPools++
	}
}

func (s *Server) importN9routerAliases(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	raw, ok := doc["modelAliases"]
	if !ok {
		return
	}
	var aliases map[string]string
	if err := json.Unmarshal(raw, &aliases); err != nil {
		res.Errors = append(res.Errors, "modelAliases: "+err.Error())
		return
	}
	for alias, target := range aliases {
		alias = strings.TrimSpace(alias)
		target = strings.TrimSpace(target)
		if alias == "" || target == "" {
			res.Skipped++
			continue
		}
		if err := s.aliases.Set(ctx, alias, target); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("alias %s: %v", alias, err))
			continue
		}
		res.Aliases++
	}
}

// importN9routerCustomModels imports 9router's customModels (user-registered
// model definitions attached to a provider alias) as KeiRouter CustomModels.
func (s *Server) importN9routerCustomModels(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	raw, ok := doc["customModels"]
	if !ok {
		return
	}
	var models []struct {
		ProviderAlias string `json:"providerAlias"`
		ID            string `json:"id"`
		Type          string `json:"type"`
		Name          string `json:"name"`
	}
	if err := json.Unmarshal(raw, &models); err != nil {
		res.Errors = append(res.Errors, "customModels: "+err.Error())
		return
	}
	for _, m := range models {
		if m.ID == "" || m.ProviderAlias == "" {
			res.Skipped++
			continue
		}
		providerID := mapN9routerProvider(m.ProviderAlias, nil)
		if providerID == "" {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("custom model %s: unknown provider %q", m.ID, m.ProviderAlias))
			continue
		}
		kind := strings.TrimSpace(m.Type)
		if kind == "" {
			kind = "llm"
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = m.ID
		}
		cm := store.CustomModel{
			ID:          uuid.NewString(),
			TenantID:    adminTenant,
			ProviderID:  providerID,
			ModelID:     m.ID,
			DisplayName: name,
			Kind:        kind,
			Source:      "imported",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := s.db.CustomProviders().CreateModel(ctx, cm); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("custom model %s/%s: %v", m.ProviderAlias, m.ID, err))
			continue
		}
		s.reloadCustomModels(ctx, providerID)
	}
}

// mapN9routerProvider resolves a 9router provider id (or a node id reference)
// to a KeiRouter provider id. Returns "" when it cannot be resolved.
func mapN9routerProvider(provider string, nodeIDMap map[string]string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return ""
	}
	// Node reference: custom OpenAI/Anthropic/embedding node.
	if mapped, ok := nodeIDMap[provider]; ok {
		return mapped
	}
	if _, ok := connectors.SpecByID(provider); ok {
		return provider
	}
	// Try alias resolution (9router aliases sometimes differ from ids).
	if spec, ok := connectors.SpecByAlias(provider); ok {
		return spec.ID
	}
	return ""
}

// ── OmniRoute ────────────────────────────────────────────────────────
//
// OmniRoute credentials are AES-256-GCM encrypted in its SQLite database and
// the JSON sidecar exports are deliberately redacted (no tokens/keys). We
// therefore import the structural config — custom provider nodes, combos,
// proxy pools, model aliases, and account stubs (provider + label only,
// marked disabled so the user re-authenticates) — but cannot transfer
// secrets. The UI makes this limitation explicit.

func (s *Server) importOmniRoute(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	// OmniRoute payloads may be a merged sidecar bundle (separate top-level
	// arrays) or a single object. Normalize to the 9router-ish key set.
	normalized := normalizeOmniPayload(doc)

	// Custom provider nodes (full data, no secrets).
	nodeIDMap := s.importN9routerNodes(ctx, normalized, res)

	// Provider connections: stub accounts only (no credentials available).
	s.importOmniConnections(ctx, normalized, res, nodeIDMap)

	// Combos -> chains. OmniRoute combo models carry richer step objects; the
	// shared importer handles the simple "alias/model" string form, so we
	// flatten OmniRoute steps into strings first.
	s.importOmniCombos(ctx, normalized, res)

	// API keys: OmniRoute redacts to a prefix only; skip (cannot reconstruct).
	if raw, ok := normalized["apiKeys"]; ok {
		var keys []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &keys); err == nil {
			res.Skipped += len(keys)
			if len(keys) > 0 {
				res.Errors = append(res.Errors, "omniroute api keys are redacted in exports: re-create them in KeiRouter")
			}
		}
	}

	// Proxy pools + aliases reuse the 9router importer shapes.
	s.importN9routerProxyPools(ctx, normalized, res)
	s.importN9routerAliases(ctx, normalized, res)
}

func (s *Server) importOmniConnections(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult, nodeIDMap map[string]string) {
	raw, ok := doc["providerConnections"]
	if !ok {
		return
	}
	var conns []struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Name     string `json:"name"`
		AuthType string `json:"auth_type"`
		Email    string `json:"email"`
	}
	if err := json.Unmarshal(raw, &conns); err != nil {
		res.Errors = append(res.Errors, "provider_connections: "+err.Error())
		return
	}
	for _, c := range conns {
		provider := mapN9routerProvider(c.Provider, nodeIDMap)
		if provider == "" {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("connection %s: unmapped provider %q", c.ID, c.Provider))
			continue
		}
		spec, specOK := connectors.SpecByID(provider)
		label := strings.TrimSpace(c.Name)
		if label == "" {
			label = strings.TrimSpace(c.Email)
		}
		if label == "" && specOK {
			label = spec.DisplayName
		}
		if label == "" {
			label = provider
		}
		now := time.Now()
		acc := store.Account{
			ID:        uuid.NewString(),
			TenantID:  adminTenant,
			Provider:  provider,
			Label:     label + " (re-auth needed)",
			AuthKind:  store.AuthAPIKey,
			Disabled:  true, // no credentials imported; user must re-authenticate
			CreatedAt: now,
			UpdatedAt: now,
		}
		// Seal an empty secret so the metadata blob is valid.
		if s.vault != nil {
			if err := s.vault.Seal(&acc, vault.NewSecret{Metadata: map[string]string{}}); err != nil {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("connection %s: seal stub: %v", c.ID, err))
				continue
			}
		}
		if err := s.accounts.Create(ctx, acc); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("connection %s: create account: %v", c.ID, err))
			continue
		}
		res.Accounts++
	}
}

func (s *Server) importOmniCombos(ctx context.Context, doc map[string]json.RawMessage, res *foreignImportResult) {
	raw, ok := doc["combos"]
	if !ok {
		return
	}
	// OmniRoute combos may be either an array of rows with a `data` JSON blob
	// (live schema) or already-flat objects. Flatten both into the simple
	// {name, models[]} shape the shared importer expects.
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		res.Errors = append(res.Errors, "combos: "+err.Error())
		return
	}
	flat := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var name string
		_ = json.Unmarshal(row["name"], &name)
		var dataRaw json.RawMessage
		_ = json.Unmarshal(row["data"], &dataRaw)
		var models []string
		if len(dataRaw) > 0 {
			var data struct {
				Name   string `json:"name"`
				Models []struct {
					Kind     string `json:"kind"`
					Model    string `json:"model"`
					Provider string `json:"providerId"`
				} `json:"models"`
			}
			if err := json.Unmarshal(dataRaw, &data); err == nil {
				if name == "" {
					name = data.Name
				}
				for _, m := range data.Models {
					if m.Kind != "model" {
						continue
					}
					if m.Provider != "" && m.Model != "" {
						models = append(models, m.Provider+"/"+m.Model)
					} else if m.Model != "" {
						models = append(models, m.Model)
					}
				}
			}
		}
		// Fallback: top-level models string array (older shape).
		if len(models) == 0 {
			_ = json.Unmarshal(row["models"], &models)
		}
		flat = append(flat, map[string]any{"name": name, "models": models})
	}
	encoded, _ := json.Marshal(flat)
	doc["combos"] = encoded
	s.importN9routerCombos(ctx, doc, res)
}

// normalizeOmniPayload maps OmniRoute's snake_case keys to the camelCase keys
// the shared 9router importers expect.
func normalizeOmniPayload(doc map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	rename := [][2]string{
		{"provider_connections", "providerConnections"},
		{"provider_nodes", "providerNodes"},
		{"api_keys", "apiKeys"},
		{"proxy_pools", "proxyPools"},
		{"model_aliases", "modelAliases"},
	}
	for _, r := range rename {
		if v, ok := out[r[0]]; ok {
			out[r[1]] = v
			delete(out, r[0])
		}
	}
	return out
}

// ── helpers ──────────────────────────────────────────────────────────

// psdKeyRemap maps 9router providerSpecificData keys to the KeiRouter metadata
// keys each provider's connector reads from creds.Extra. 9router and KeiRouter
// diverge on naming for a few providers (kiro stores authMethod/region without
// the "kiro_" prefix; cursor uses machineId). Without this remap, imported
// kiro accounts can't refresh tokens or fetch quota because the connector
// looks for kiro_auth_method / kiro_region and finds auth_method / region.
var psdKeyRemap = map[string]map[string]string{
	"kiro": {
		"authMethod":    "kiro_auth_method",
		"region":        "kiro_region",
		"profileArn":    "kiro_profile_arn",
		"clientId":      "kiro_client_id",
		"clientSecret":  "kiro_client_secret",
		"tokenEndpoint": "kiro_token_endpoint",
		"scope":         "kiro_scope",
		"scopes":        "kiro_scope",
	},
	"cursor": {
		"machineId": "machine_id",
		"ghostMode": "ghost_mode",
	},
	"qoder": {
		"userId":    "user_id",
		"machineId": "machine_id",
	},
	// The Cloudflare base URL template is .../accounts/{accountId}/ai/v1, so
	// the key must stay camelCase to resolve the placeholder.
	"cloudflare-ai": {
		"accountId": "accountId",
	},
}

// psdToMetadata converts a 9router providerSpecificData map into KeiRouter
// account metadata. Keys are normalized from camelCase to snake_case so that
// provider-specific fields land on the keys connectors read from creds.Extra.
// Provider-specific overrides (psdKeyRemap) take precedence over the generic
// camelCase conversion. Proxy-related keys are dropped because KeiRouter
// models per-account proxying via ProxyPoolID, not metadata.
func psdToMetadata(provider string, psd map[string]any) map[string]string {
	meta := map[string]string{}
	if psd == nil {
		return meta
	}
	skip := map[string]bool{
		"connectionProxyEnabled": true,
		"connectionProxyUrl":     true,
		"connectionNoProxy":      true,
		"connectionProxyPoolId":  true,
		"proxyPoolId":            true,
		"vercelRelayUrl":         true,
	}
	remap := psdKeyRemap[provider]
	for k, v := range psd {
		if skip[k] {
			continue
		}
		s := strVal(v)
		if s == "" {
			continue
		}
		if remap != nil {
			if mapped, ok := remap[k]; ok {
				meta[mapped] = s
				continue
			}
		}
		meta[camelToSnake(k)] = s
	}
	return meta
}

// camelToSnake converts a camelCase key to snake_case. Already-snake_case keys
// (containing underscores) pass through unchanged.
func camelToSnake(s string) string {
	if strings.Contains(s, "_") {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func strVal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%v", t)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func parseRFC3339(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// splitAliasModel splits a "alias/model" combo entry into its provider alias
// and model id. The first "/" separates them; a leading "chain:" prefix is
// stripped (it references another combo, which we skip by returning empty
// provider so the caller drops it).
func splitAliasModel(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "chain:") {
		return "", ""
	}
	idx := strings.Index(s, "/")
	if idx <= 0 {
		return "", s
	}
	return s[:idx], s[idx+1:]
}
