package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/mcp"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// Limits on operator input for MCP servers.
const (
	mcpMaxNameLen     = 80
	mcpMaxDescLen     = 500
	mcpMaxSecretLen   = 8192
	mcpMaxHeaders     = 20
	mcpMaxHeaderValue = 4096
	mcpMinTimeoutMS   = 1000
	mcpMaxTimeoutMS   = 600000
	mcpDefaultTimeout = 60000
	mcpProbeTimeout   = 30 * time.Second
)

// mcpSlugPattern: 1-32 characters of a-z, 0-9 and "-", not starting or
// ending with "-". The slug prefixes tool names as slug__tool.
var mcpSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// mountMCPAdmin registers the MCP server admin endpoints under /api/mcp.
func (s *Server) mountMCPAdmin(r chi.Router) {
	r.Route("/mcp", func(r chi.Router) {
		r.Use(s.requireMCP)
		r.Get("/catalog", s.adminMCPCatalog)
		r.Get("/servers", s.adminListMCPServers)
		r.Post("/servers", s.adminCreateMCPServer)
		r.Post("/servers/test", s.adminTestMCPServer)
		r.Get("/servers/{id}", s.adminGetMCPServer)
		r.Patch("/servers/{id}", s.adminUpdateMCPServer)
		r.Delete("/servers/{id}", s.adminDeleteMCPServer)
		r.Post("/servers/{id}/sync", s.adminSyncMCPServer)
		r.Put("/servers/{id}/tools", s.adminSetMCPTools)
		r.Post("/servers/{id}/call", s.adminCallMCPTool)
		r.Get("/servers/{id}/access", s.adminMCPServerAccess)
		r.Get("/logs", s.adminListMCPLogs)
		r.Get("/stats", s.adminMCPStats)
	})
	r.With(s.requireMCP).Get("/keys/{id}/mcp-access", s.adminGetKeyMCPAccess)
	r.With(s.requireMCP).Put("/keys/{id}/mcp-access", s.adminSetKeyMCPAccess)
}

func (s *Server) requireMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.mcp == nil {
			writeError(w, http.StatusServiceUnavailable, "the MCP gateway is not running")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mcpServerInput is the create/update body. Pointer fields are optional on
// update: nil keeps the stored value.
type mcpServerInput struct {
	PresetID       *string            `json:"preset_id"`
	Slug           string             `json:"slug"`
	Name           *string            `json:"name"`
	Description    *string            `json:"description"`
	URL            *string            `json:"url"`
	AuthType       *string            `json:"auth_type"`
	AuthHeader     *string            `json:"auth_header"`
	Username       *string            `json:"username"`
	Secret         *string            `json:"secret"`
	StaticHeaders  *map[string]string `json:"static_headers"`
	ForwardHeaders *[]string          `json:"forward_headers"`
	TimeoutMS      *int               `json:"timeout_ms"`
	Enabled        *bool              `json:"enabled"`
	// ServerID lets the edit form test a changed configuration while reusing
	// the stored secret. Only read by the test endpoint.
	ServerID string `json:"server_id"`
}

type mcpToolJSON struct {
	Name          string          `json:"name"`
	Title         string          `json:"title,omitempty"`
	Description   string          `json:"description,omitempty"`
	InputSchema   json.RawMessage `json:"input_schema,omitempty"`
	Enabled       bool            `json:"enabled"`
	InvalidReason string          `json:"invalid_reason,omitempty"`
	ChangedAt     *time.Time      `json:"changed_at,omitempty"`
	ExposedName   string          `json:"exposed_name"`
}

type mcpServerJSON struct {
	ID                string        `json:"id"`
	Slug              string        `json:"slug"`
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	PresetID          string        `json:"preset_id"`
	URL               string        `json:"url"`
	AuthType          string        `json:"auth_type"`
	AuthHeader        string        `json:"auth_header"`
	Username          string        `json:"username"`
	HasSecret         bool          `json:"has_secret"`
	StaticHeaderNames []string      `json:"static_header_names"`
	ForwardHeaders    []string      `json:"forward_headers"`
	ToolMode          string        `json:"tool_mode"`
	TimeoutMS         int           `json:"timeout_ms"`
	Enabled           bool          `json:"enabled"`
	ProtocolVersion   string        `json:"protocol_version"`
	HealthStatus      string        `json:"health_status"`
	HealthError       string        `json:"health_error"`
	HealthCheckedAt   *time.Time    `json:"health_checked_at"`
	ToolsSyncedAt     *time.Time    `json:"tools_synced_at"`
	ToolCount         int           `json:"tool_count"`
	ExposedToolCount  int           `json:"exposed_tool_count"`
	ChangedToolCount  int           `json:"changed_tool_count"`
	InvalidToolCount  int           `json:"invalid_tool_count"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	Tools             []mcpToolJSON `json:"tools,omitempty"`
}

func (s *Server) adminMCPCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"presets":      s.mcp.Catalog.Presets(),
		"catalog_only": s.mcp.Catalog.CatalogOnly(),
	})
}

func (s *Server) adminListMCPServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	servers, err := s.mcp.Repo.ListServers(ctx, s.mcp.Tenant())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list mcp servers"))
		return
	}
	tools, err := s.mcp.Repo.ListTenantTools(ctx, s.mcp.Tenant())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list mcp tools"))
		return
	}
	byServer := map[string][]store.MCPServerTool{}
	for _, t := range tools {
		byServer[t.ServerID] = append(byServer[t.ServerID], t)
	}
	allKeys, err := s.mcp.Repo.CountKeysWithMCPMode(ctx, s.mcp.Tenant(), store.MCPModeAll)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "count mcp keys"))
		return
	}
	out := make([]mcpServerJSON, 0, len(servers))
	for _, srv := range servers {
		out = append(out, s.mcpServerView(srv, byServer[srv.ID], false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out, "keys_with_all_access": allKeys})
}

func (s *Server) adminGetMCPServer(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadMCPServer(w, r)
	if !ok {
		return
	}
	tools, err := s.mcp.Repo.ListTools(r.Context(), srv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list mcp tools"))
		return
	}
	writeJSON(w, http.StatusOK, s.mcpServerView(srv, tools, true))
}

func (s *Server) adminCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	var in mcpServerInput
	if !decodeJSON(w, r, &in) {
		return
	}
	now := time.Now().UTC()
	srv := store.MCPServer{
		ID: uuid.NewString(), TenantID: s.mcp.Tenant(), Slug: in.Slug,
		AuthType: mcp.AuthNone, ToolMode: store.MCPModeAll, TimeoutMS: mcpDefaultTimeout,
		Enabled: true, HealthStatus: store.MCPHealthUnknown, CreatedAt: now, UpdatedAt: now,
	}
	if !mcpSlugPattern.MatchString(srv.Slug) {
		writeError(w, http.StatusBadRequest, "slug must be 1-32 characters of a-z, 0-9 and -, and cannot start or end with -")
		return
	}
	sec, msg := s.applyMCPInput(&srv, mcp.Secret{}, in, true)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := mcp.SealSecret(s.mcp.Sealer, &srv, sec); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "seal mcp secret"))
		return
	}
	if err := s.mcp.Repo.CreateServer(r.Context(), srv); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "slug \""+srv.Slug+"\" is already used by another server")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "create mcp server"))
		return
	}
	s.mcp.Changed(r.Context())
	s.respondAfterMCPSync(w, r, srv.ID, http.StatusCreated)
}

func (s *Server) adminUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadMCPServer(w, r)
	if !ok {
		return
	}
	var in mcpServerInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Slug != "" && in.Slug != srv.Slug {
		writeError(w, http.StatusBadRequest, "slug cannot be changed; clients use it in tool names")
		return
	}
	old, err := mcp.OpenSecret(s.mcp.Sealer, srv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "open mcp secret"))
		return
	}
	before := srv
	sec, msg := s.applyMCPInput(&srv, old, in, false)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	connChanged := srv.URL != before.URL || srv.AuthType != before.AuthType || srv.AuthHeader != before.AuthHeader ||
		in.Secret != nil || in.Username != nil || in.StaticHeaders != nil
	if connChanged {
		srv.ProtocolVersion = "" // negotiate again against the new endpoint
	}
	if err := mcp.SealSecret(s.mcp.Sealer, &srv, sec); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "seal mcp secret"))
		return
	}
	srv.UpdatedAt = time.Now().UTC()
	if err := s.mcp.Repo.UpdateServer(r.Context(), srv); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "update mcp server"))
		return
	}
	if connChanged {
		s.mcp.ForgetServer(srv.ID)
	}
	s.mcp.Changed(r.Context())
	if connChanged || (srv.Enabled && !before.Enabled) {
		s.respondAfterMCPSync(w, r, srv.ID, http.StatusOK)
		return
	}
	s.respondMCPServer(w, r, srv.ID, http.StatusOK, "")
}

func (s *Server) adminDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.mcp.Repo.DeleteServer(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "MCP server not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "delete mcp server"))
		return
	}
	s.mcp.ForgetServer(id)
	s.mcp.Changed(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSyncMCPServer(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadMCPServer(w, r)
	if !ok {
		return
	}
	s.respondAfterMCPSync(w, r, srv.ID, http.StatusOK)
}

// adminTestMCPServer connects to a configuration without saving it and
// reports the protocol version and tools it found.
func (s *Server) adminTestMCPServer(w http.ResponseWriter, r *http.Request) {
	var in mcpServerInput
	if !decodeJSON(w, r, &in) {
		return
	}
	srv := store.MCPServer{AuthType: mcp.AuthNone, ToolMode: store.MCPModeAll, TimeoutMS: mcpDefaultTimeout, Enabled: true}
	var old mcp.Secret
	creating := true
	if in.ServerID != "" {
		stored, err := s.mcp.Repo.GetServer(r.Context(), in.ServerID)
		if err != nil {
			writeError(w, http.StatusNotFound, "MCP server not found")
			return
		}
		if old, err = mcp.OpenSecret(s.mcp.Sealer, stored); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "open mcp secret"))
			return
		}
		srv, creating = stored, false
		srv.ProtocolVersion = ""
	}
	if in.Name == nil && creating {
		name := "test"
		in.Name = &name
	}
	sec, msg := s.applyMCPInput(&srv, old, in, creating)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mcpProbeTimeout)
	defer cancel()
	start := time.Now()
	res, err := s.mcp.Syncer.Probe(ctx, srv, sec)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "latency_ms": latency})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "protocol_version": res.ProtocolVersion, "tools": res.Tools, "latency_ms": latency,
	})
}

func (s *Server) adminSetMCPTools(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadMCPServer(w, r)
	if !ok {
		return
	}
	var body struct {
		ToolMode string   `json:"tool_mode"`
		Enabled  []string `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ToolMode != store.MCPModeAll && body.ToolMode != store.MCPModeSelected {
		writeError(w, http.StatusBadRequest, "tool_mode must be all or selected")
		return
	}
	if body.ToolMode == store.MCPModeSelected && len(body.Enabled) == 0 {
		writeError(w, http.StatusBadRequest, "select at least one tool, or expose every tool")
		return
	}
	if err := s.mcp.Repo.SetToolSelection(r.Context(), srv.ID, body.ToolMode, body.Enabled); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrInvalid.Error()+": "))
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "set mcp tools"))
		return
	}
	s.mcp.Changed(r.Context())
	s.respondMCPServer(w, r, srv.ID, http.StatusOK, "")
}

// respondAfterMCPSync syncs a server's catalog and returns the server. A
// failed sync still returns the saved server, with sync_error set.
func (s *Server) respondAfterMCPSync(w http.ResponseWriter, r *http.Request, id string, status int) {
	srv, err := s.mcp.Repo.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "get mcp server"))
		return
	}
	syncErr := ""
	if srv.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), mcpProbeTimeout)
		_, err := s.mcp.Syncer.SyncServer(ctx, id)
		cancel()
		if err != nil {
			syncErr = err.Error()
		}
	}
	s.respondMCPServer(w, r, id, status, syncErr)
}

func (s *Server) respondMCPServer(w http.ResponseWriter, r *http.Request, id string, status int, syncErr string) {
	srv, err := s.mcp.Repo.GetServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "get mcp server"))
		return
	}
	tools, err := s.mcp.Repo.ListTools(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list mcp tools"))
		return
	}
	view := s.mcpServerView(srv, tools, true)
	if syncErr == "" {
		writeJSON(w, status, view)
		return
	}
	writeJSON(w, status, struct {
		mcpServerJSON
		SyncError string `json:"sync_error"`
	}{view, syncErr})
}

func (s *Server) loadMCPServer(w http.ResponseWriter, r *http.Request) (store.MCPServer, bool) {
	srv, err := s.mcp.Repo.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "MCP server not found")
		} else {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "get mcp server"))
		}
		return store.MCPServer{}, false
	}
	if srv.TenantID != s.mcp.Tenant() {
		writeError(w, http.StatusNotFound, "MCP server not found")
		return store.MCPServer{}, false
	}
	return srv, true
}

func (s *Server) mcpServerView(srv store.MCPServer, tools []store.MCPServerTool, withTools bool) mcpServerJSON {
	sec, err := mcp.OpenSecret(s.mcp.Sealer, srv)
	if err != nil {
		s.log.Warn("mcp: open secret for view", "server", srv.Slug, "err", err)
	}
	names := make([]string, 0, len(sec.StaticHeaders))
	for k := range sec.StaticHeaders {
		names = append(names, k)
	}
	v := mcpServerJSON{
		ID: srv.ID, Slug: srv.Slug, Name: srv.Name, Description: srv.Description, PresetID: srv.PresetID,
		URL: srv.URL, AuthType: srv.AuthType, AuthHeader: srv.AuthHeader, Username: sec.Username,
		HasSecret: sec.Secret != "", StaticHeaderNames: names, ForwardHeaders: srv.ForwardHeaders,
		ToolMode: srv.ToolMode, TimeoutMS: srv.TimeoutMS, Enabled: srv.Enabled, ProtocolVersion: srv.ProtocolVersion,
		HealthStatus: srv.HealthStatus, HealthError: srv.HealthError, HealthCheckedAt: srv.HealthCheckedAt,
		ToolsSyncedAt: srv.ToolsSyncedAt, CreatedAt: srv.CreatedAt, UpdatedAt: srv.UpdatedAt,
	}
	if v.ForwardHeaders == nil {
		v.ForwardHeaders = []string{}
	}
	for _, t := range tools {
		v.ToolCount++
		if t.Enabled && t.InvalidReason == "" {
			v.ExposedToolCount++
		}
		if t.ChangedAt != nil {
			v.ChangedToolCount++
		}
		if t.InvalidReason != "" {
			v.InvalidToolCount++
		}
		if withTools {
			title, desc, schema := mcpToolText(t.Definition)
			v.Tools = append(v.Tools, mcpToolJSON{
				Name: t.Name, Title: title, Description: desc, InputSchema: schema, Enabled: t.Enabled, InvalidReason: t.InvalidReason,
				ChangedAt: t.ChangedAt, ExposedName: srv.Slug + mcp.ToolNameSeparator + t.Name,
			})
		}
	}
	if withTools && v.Tools == nil {
		v.Tools = []mcpToolJSON{}
	}
	return v
}

func mcpToolText(def string) (title, desc string, schema json.RawMessage) {
	var t struct {
		Title       string          `json:"title"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	_ = json.Unmarshal([]byte(def), &t)
	return t.Title, t.Description, t.InputSchema
}

// applyMCPInput validates the input and copies it onto srv. It returns the
// secret blob to seal, or a message for a 400 response. creating requires
// the fields a new server needs.
func (s *Server) applyMCPInput(srv *store.MCPServer, sec mcp.Secret, in mcpServerInput, creating bool) (mcp.Secret, string) {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > mcpMaxNameLen {
			return sec, "name is required and must be at most 80 characters"
		}
		srv.Name = name
	} else if creating {
		return sec, "name is required"
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(d) > mcpMaxDescLen {
			return sec, "description must be at most 500 characters"
		}
		srv.Description = d
	}
	if in.PresetID != nil {
		srv.PresetID = strings.TrimSpace(*in.PresetID)
	}
	if in.URL != nil {
		srv.URL = strings.TrimSpace(*in.URL)
	} else if creating {
		return sec, "url is required"
	}
	if msg := validateMCPURL(srv.URL); msg != "" {
		return sec, msg
	}
	if in.AuthType != nil {
		srv.AuthType = *in.AuthType
	}
	if !mcp.ValidAuthType(srv.AuthType) {
		return sec, "auth_type must be none, bearer, header or basic"
	}
	if in.AuthHeader != nil {
		srv.AuthHeader = strings.TrimSpace(*in.AuthHeader)
	}
	if srv.AuthType == mcp.AuthHeader {
		if !mcp.IsToken(srv.AuthHeader) {
			return sec, "auth_header must be a valid header name"
		}
		if mcp.IsReservedHeader(srv.AuthHeader) && !strings.EqualFold(srv.AuthHeader, "x-api-key") {
			return sec, "auth_header \"" + srv.AuthHeader + "\" cannot be used; pick bearer for Authorization"
		}
	} else {
		srv.AuthHeader = ""
	}
	if in.Secret != nil {
		if len(*in.Secret) > mcpMaxSecretLen || strings.ContainsAny(*in.Secret, "\r\n\x00") {
			return sec, "secret must be a single line of at most 8192 characters"
		}
		sec.Secret = *in.Secret
	}
	if in.Username != nil {
		if strings.ContainsAny(*in.Username, "\r\n\x00:") || len(*in.Username) > 256 {
			return sec, "username must be one line without \":\""
		}
		sec.Username = strings.TrimSpace(*in.Username)
	}
	if srv.AuthType == mcp.AuthNone {
		sec.Secret, sec.Username = "", ""
	}
	if srv.AuthType != mcp.AuthBasic {
		sec.Username = ""
	} else if sec.Username == "" {
		return sec, "username is required for basic authentication"
	}
	if in.StaticHeaders != nil {
		hs := *in.StaticHeaders
		if len(hs) > mcpMaxHeaders {
			return sec, "at most 20 static headers"
		}
		clean := make(map[string]string, len(hs))
		for k, v := range hs {
			if err := mcp.ValidateStaticHeader(k, v); err != nil {
				return sec, err.Error()
			}
			if len(v) > mcpMaxHeaderValue {
				return sec, "header \"" + k + "\" value is too long"
			}
			if srv.AuthType == mcp.AuthHeader && strings.EqualFold(k, srv.AuthHeader) {
				return sec, "header \"" + k + "\" is already the auth header"
			}
			clean[http.CanonicalHeaderKey(k)] = v
		}
		sec.StaticHeaders = clean
	}
	if in.ForwardHeaders != nil {
		fh := *in.ForwardHeaders
		if len(fh) > mcpMaxHeaders {
			return sec, "at most 20 forwarded headers"
		}
		seen := map[string]bool{}
		out := make([]string, 0, len(fh))
		for _, h := range fh {
			h = strings.TrimSpace(h)
			if !mcp.IsToken(h) || mcp.IsReservedHeader(h) {
				return sec, "header \"" + h + "\" cannot be forwarded"
			}
			c := http.CanonicalHeaderKey(h)
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
		srv.ForwardHeaders = out
	}
	if in.TimeoutMS != nil {
		if *in.TimeoutMS < mcpMinTimeoutMS || *in.TimeoutMS > mcpMaxTimeoutMS {
			return sec, "timeout_ms must be between 1000 and 600000"
		}
		srv.TimeoutMS = *in.TimeoutMS
	}
	if in.Enabled != nil {
		srv.Enabled = *in.Enabled
	}
	return sec, s.checkMCPPreset(srv, sec)
}

// checkMCPPreset enforces the preset rules: a referenced preset must exist,
// and with catalog_only every server must match its preset's URL pattern and
// auth types.
func (s *Server) checkMCPPreset(srv *store.MCPServer, sec mcp.Secret) string {
	if srv.PresetID == "" {
		if s.mcp.Catalog.CatalogOnly() {
			return "this gateway only allows servers from the catalog; pick a preset"
		}
		return ""
	}
	p, ok := s.mcp.Catalog.Get(srv.PresetID)
	if !ok {
		return "unknown preset \"" + srv.PresetID + "\""
	}
	if !p.SecretOptional && srv.AuthType != mcp.AuthNone && sec.Secret == "" {
		return p.Name + " needs a " + strings.ToLower(defaultStr(p.SecretLabel, "secret"))
	}
	if !s.mcp.Catalog.CatalogOnly() {
		return ""
	}
	if !p.MatchesURL(srv.URL) {
		return "the URL does not match the " + p.Name + " preset"
	}
	if !p.AllowsAuth(srv.AuthType) {
		return "the " + p.Name + " preset does not allow auth type " + srv.AuthType
	}
	if srv.AuthType == mcp.AuthHeader && !strings.EqualFold(srv.AuthHeader, p.AuthHeader) {
		return "the " + p.Name + " preset sends the key in " + p.AuthHeader
	}
	return ""
}

func validateMCPURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "url must be an http or https URL"
	}
	if u.User != nil {
		return "put credentials in the secret field, not in the URL"
	}
	if len(raw) > 2048 {
		return "url is too long"
	}
	if err := httputil.ValidateBaseURL(raw); err != nil {
		return "url is not allowed: " + err.Error()
	}
	return ""
}

// adminCallMCPTool runs one tool from the dashboard tester. Access rules and
// limits do not apply; the call is logged with the "dashboard" key.
func (s *Server) adminCallMCPTool(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadMCPServer(w, r)
	if !ok {
		return
	}
	var body struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Tool == "" {
		writeError(w, http.StatusBadRequest, "tool is required")
		return
	}
	if args := bytes.TrimSpace(body.Arguments); len(args) > 0 && args[0] != '{' {
		writeError(w, http.StatusBadRequest, "arguments must be a JSON object")
		return
	}
	res, err := s.mcp.Invoke(r.Context(), srv.ID, body.Tool, body.Arguments)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// adminMCPServerAccess lists the keys that can use a server.
func (s *Server) adminMCPServerAccess(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadMCPServer(w, r)
	if !ok {
		return
	}
	grants, err := s.mcp.Repo.ListKeysForServer(r.Context(), s.mcp.Tenant(), srv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list mcp keys"))
		return
	}
	keys, err := s.identity.List(r.Context(), adminTenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list keys"))
		return
	}
	type row struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Display  string   `json:"display"`
		Disabled bool     `json:"disabled"`
		AllTools bool     `json:"all_tools"`
		Tools    []string `json:"tools"`
	}
	out := []row{}
	for _, k := range keys {
		tools, ok := grants[k.ID]
		if !ok || k.Kind != "" {
			continue
		}
		out = append(out, row{ID: k.ID, Name: k.Name, Display: k.Display, Disabled: k.Disabled, AllTools: tools == nil, Tools: tools})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

type mcpKeyAccessJSON struct {
	Mode    string `json:"mode"`
	Servers []struct {
		ServerID string   `json:"server_id"`
		ToolMode string   `json:"tool_mode"`
		Tools    []string `json:"tools"`
	} `json:"servers"`
}

func (s *Server) adminGetKeyMCPAccess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !s.mcpOperatorKey(w, r, id) {
		return
	}
	a, err := s.mcp.Repo.GetKeyAccess(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "get mcp access"))
		return
	}
	var out mcpKeyAccessJSON
	out.Mode = a.Mode
	out.Servers = make([]struct {
		ServerID string   `json:"server_id"`
		ToolMode string   `json:"tool_mode"`
		Tools    []string `json:"tools"`
	}, 0, len(a.Grants))
	for _, g := range a.Grants {
		tools := g.Tools
		if tools == nil {
			tools = []string{}
		}
		out.Servers = append(out.Servers, struct {
			ServerID string   `json:"server_id"`
			ToolMode string   `json:"tool_mode"`
			Tools    []string `json:"tools"`
		}{g.ServerID, g.ToolMode, tools})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminSetKeyMCPAccess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !s.mcpOperatorKey(w, r, id) {
		return
	}
	var body mcpKeyAccessJSON
	if !decodeJSON(w, r, &body) {
		return
	}
	a := store.MCPKeyAccess{Mode: body.Mode}
	switch body.Mode {
	case store.MCPModeNone, store.MCPModeAll:
		if len(body.Servers) > 0 {
			writeError(w, http.StatusBadRequest, "servers are only allowed with mode selected")
			return
		}
	case store.MCPModeSelected:
		if len(body.Servers) == 0 {
			writeError(w, http.StatusBadRequest, "pick at least one server, or choose every server or no access")
			return
		}
		servers, err := s.mcp.Repo.ListServers(r.Context(), s.mcp.Tenant())
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list mcp servers"))
			return
		}
		known := map[string]bool{}
		for _, srv := range servers {
			known[srv.ID] = true
		}
		seen := map[string]bool{}
		for _, g := range body.Servers {
			if !known[g.ServerID] {
				writeError(w, http.StatusBadRequest, "unknown server "+strconv.Quote(g.ServerID))
				return
			}
			if seen[g.ServerID] {
				writeError(w, http.StatusBadRequest, "server "+strconv.Quote(g.ServerID)+" is listed twice")
				return
			}
			seen[g.ServerID] = true
			switch g.ToolMode {
			case store.MCPModeAll:
			case store.MCPModeSelected:
				if len(g.Tools) == 0 {
					writeError(w, http.StatusBadRequest, "pick at least one tool for each server limited to selected tools")
					return
				}
				if len(g.Tools) > 1000 {
					writeError(w, http.StatusBadRequest, "too many tools")
					return
				}
			default:
				writeError(w, http.StatusBadRequest, "tool_mode must be all or selected")
				return
			}
			a.Grants = append(a.Grants, store.MCPKeyGrant{ServerID: g.ServerID, ToolMode: g.ToolMode, Tools: dedupeStrings(g.Tools)})
		}
	default:
		writeError(w, http.StatusBadRequest, "mode must be none, all or selected")
		return
	}
	if err := s.mcp.Repo.SetKeyAccess(r.Context(), id, a); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "set mcp access"))
		return
	}
	s.mcp.InvalidateAccess(id)
	s.adminGetKeyMCPAccess(w, r)
}

// mcpOperatorKey writes 404 unless id is an operator key.
func (s *Server) mcpOperatorKey(w http.ResponseWriter, r *http.Request, id string) bool {
	k, err := s.identity.Get(r.Context(), id)
	if err != nil || k.Kind != "" || k.TenantID != adminTenant {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "get key"))
			return false
		}
		writeError(w, http.StatusNotFound, "key not found")
		return false
	}
	return true
}

func (s *Server) adminListMCPLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	logs, next, err := s.mcp.Repo.ListCallLogs(r.Context(), store.MCPLogFilter{
		TenantID: s.mcp.Tenant(), ServerID: q.Get("server_id"), APIKeyID: q.Get("key_id"),
		Status: q.Get("status"), Tool: strings.TrimSpace(q.Get("tool")), Cursor: q.Get("cursor"), Limit: limit,
	})
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "list mcp logs"))
		return
	}
	if logs == nil {
		logs = []store.MCPCallLog{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "next_cursor": next})
}

var mcpStatsPeriods = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

func (s *Server) adminMCPStats(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	d, ok := mcpStatsPeriods[period]
	if !ok {
		writeError(w, http.StatusBadRequest, "period must be 1h, 24h, 7d or 30d")
		return
	}
	st, err := s.mcp.Repo.CallStats(r.Context(), s.mcp.Tenant(), time.Now().Add(-d))
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "mcp stats"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"period": period, "stats": st})
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
