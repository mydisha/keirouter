package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/clitools"
	"github.com/mydisha/keirouter/backend/internal/crypto"
)

// cliToolSettingsView is the connection stored in a tool's config, as shown to
// the dashboard. The plaintext key never leaves the server: only a masked hint
// and whether it authenticates against this gateway.
type cliToolSettingsView struct {
	BaseURL    string `json:"base_url"`
	Model      string `json:"model,omitempty"`
	APIKeyHint string `json:"api_key_hint,omitempty"`
	// KeyStatus is "valid", "invalid" (unknown to this gateway), "disabled"
	// or "missing" (no key in the config).
	KeyStatus string `json:"key_status"`
	KeyName   string `json:"key_name,omitempty"`
	// EndpointMatch reports whether BaseURL addresses this gateway.
	EndpointMatch bool `json:"endpoint_match"`
}

// cliToolView merges a tool's live status with its manual-setup snippet. The
// shallower Settings field shadows clitools.Status.Settings in JSON.
type cliToolView struct {
	clitools.Status
	Dialect      string               `json:"dialect"`
	Instructions string               `json:"instructions"`
	Snippet      string               `json:"snippet"`
	Settings     *cliToolSettingsView `json:"settings,omitempty"`
}

// handleCLITools returns the status and config snippets for all supported CLI
// tools. The frontend uses the snippet fields for copy-to-clipboard and the
// status/settings fields for the connection badges and form prefill.
func (s *Server) handleCLITools(w http.ResponseWriter, r *http.Request) {
	baseURL := s.publicBaseURL(r)
	model := r.URL.Query().Get("model")
	statuses := s.cliTools.DetectAll(r.Context(), s.cliToolHome)
	snippets := snippetsByID(baseURL, model)

	tools := make([]cliToolView, 0, len(statuses))
	for _, st := range statuses {
		tools = append(tools, s.cliToolView(r, st, snippets[st.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base_url": baseURL,
		"model":    model,
		"tools":    tools,
	})
}

// handleCLITool returns one tool's status — used to refresh the detail page
// without re-probing every tool.
func (s *Server) handleCLITool(w http.ResponseWriter, r *http.Request) {
	tool := s.cliToolOr404(w, r)
	if tool == nil {
		return
	}
	st := s.cliTools.Detect(r.Context(), tool, s.cliToolHome)
	writeJSON(w, http.StatusOK, s.cliToolView(r, st, snippetsByID(s.publicBaseURL(r), "")[st.ID]))
}

// handleCLIToolConfigure writes KeiRouter config into a specific tool.
func (s *Server) handleCLIToolConfigure(w http.ResponseWriter, r *http.Request) {
	tool := s.cliToolOr404(w, r)
	if tool == nil {
		return
	}

	var body struct {
		BaseURL string `json:"base_url"`
		// APIKey may be omitted to keep the key already stored in the tool.
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
		// Models is accepted for older clients; the first entry is the
		// default model.
		Models []string `json:"models"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	baseURL, err := validateGatewayURL(body.BaseURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" && len(body.Models) > 0 {
		model = strings.TrimSpace(body.Models[0])
	}
	if model == "" && tool.ModelRequired() {
		writeError(w, http.StatusBadRequest, tool.Name()+" needs a default model")
		return
	}

	apiKey := strings.TrimSpace(body.APIKey)
	if apiKey == "" {
		current, err := tool.Read(s.cliToolHome)
		if err != nil {
			writeCLIToolError(w, err)
			return
		}
		if current == nil || current.APIKey == "" {
			writeError(w, http.StatusBadRequest, "api_key is required: "+tool.Name()+" has no stored key to keep")
			return
		}
		apiKey = current.APIKey
	}
	// Refuse keys this gateway would reject — writing one leaves the tool
	// failing every request with 401.
	switch status, _ := s.cliKeyStatus(r.Context(), apiKey); status {
	case "valid":
	case "disabled":
		writeError(w, http.StatusBadRequest, "that API key is disabled; enable it or use another key")
		return
	default:
		writeError(w, http.StatusBadRequest, "that API key isn't valid for this gateway; create a key or paste the full key shown when it was created")
		return
	}

	st, err := s.cliTools.Configure(r.Context(), tool, s.cliToolHome, clitools.Settings{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	})
	if err != nil {
		writeCLIToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"tool": s.cliToolView(r, st, snippetsByID(s.publicBaseURL(r), "")[st.ID]),
	})
}

// handleCLIToolRemove strips KeiRouter config from a specific tool.
func (s *Server) handleCLIToolRemove(w http.ResponseWriter, r *http.Request) {
	tool := s.cliToolOr404(w, r)
	if tool == nil {
		return
	}
	st, err := s.cliTools.Remove(r.Context(), tool, s.cliToolHome)
	if err != nil {
		writeCLIToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"tool": s.cliToolView(r, st, snippetsByID(s.publicBaseURL(r), "")[st.ID]),
	})
}

func (s *Server) cliToolOr404(w http.ResponseWriter, r *http.Request) clitools.Tool {
	toolID := chi.URLParam(r, "toolId")
	tool := s.cliTools.Get(toolID)
	if tool == nil {
		writeError(w, http.StatusNotFound, "unknown tool: "+toolID)
	}
	return tool
}

// writeCLIToolError maps config errors to statuses: an unparseable config is
// the user's to fix (422); anything else is a server-side write failure.
func writeCLIToolError(w http.ResponseWriter, err error) {
	var pe *clitools.ParseError
	if errors.As(err, &pe) {
		writeError(w, http.StatusUnprocessableEntity, pe.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// cliToolView builds the dashboard view of one tool.
func (s *Server) cliToolView(r *http.Request, st clitools.Status, sn cliToolSnippet) cliToolView {
	v := cliToolView{
		Status:       st,
		Dialect:      sn.Dialect,
		Instructions: sn.Instructions,
		Snippet:      sn.Snippet,
	}
	if st.Settings == nil {
		return v
	}
	view := &cliToolSettingsView{
		BaseURL:       st.Settings.BaseURL,
		Model:         st.Settings.Model,
		APIKeyHint:    maskSecret(st.Settings.APIKey),
		EndpointMatch: s.isGatewayURL(r, st.Settings.BaseURL),
	}
	view.KeyStatus, view.KeyName = s.cliKeyStatus(r.Context(), st.Settings.APIKey)
	v.Settings = view
	return v
}

// cliKeyStatus classifies a plaintext key against the key store using the
// lookup hash alone — no argon2 verification and no last-used update, so
// rendering the page neither costs a hash nor skews key activity.
func (s *Server) cliKeyStatus(ctx context.Context, plaintext string) (status, name string) {
	if plaintext == "" {
		return "missing", ""
	}
	if s.identity == nil {
		return "invalid", ""
	}
	rec, err := s.identity.Keys().FindByLookup(ctx, crypto.LookupHash(plaintext))
	if err != nil {
		return "invalid", ""
	}
	if rec.Disabled {
		return "disabled", rec.Name
	}
	return "valid", rec.Name
}

// isGatewayURL reports whether u addresses this gateway: the request's public
// origin or the configured listen address. Loopback spellings (localhost,
// 127.0.0.1, ::1, 0.0.0.0) are treated as one host.
func (s *Server) isGatewayURL(r *http.Request, u string) bool {
	target, ok := endpointKey(u)
	if !ok {
		return false
	}
	candidates := []string{s.publicBaseURL(r), "http://" + s.cfg.Addr()}
	for _, c := range candidates {
		if k, ok := endpointKey(c); ok && k == target {
			return true
		}
	}
	return false
}

// endpointKey normalizes a URL to "scheme://host:port/path" for comparison.
func endpointKey(raw string) (string, bool) {
	u, err := url.Parse(clitools.RootURL(raw))
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	host = strings.ToLower(host)
	if host == "localhost" || host == "0.0.0.0" || host == "::" {
		host = "loopback"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		host = "loopback"
	}
	return fmt.Sprintf("%s://%s:%s%s", scheme, host, port, strings.TrimRight(u.Path, "/")), true
}

// validateGatewayURL checks a user-supplied endpoint and returns its root form.
func validateGatewayURL(raw string) (string, error) {
	root := clitools.RootURL(raw)
	if root == "" {
		return "", errors.New("base_url is required")
	}
	u, err := url.Parse(root)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("base_url must be an http(s) URL such as http://localhost:20180")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("base_url must not contain credentials, a query or a fragment")
	}
	return root, nil
}

// maskSecret renders a key as "kr_AbC1…7xQ2" — enough to recognize it,
// useless to anyone reading over a shoulder.
func maskSecret(key string) string {
	if key == "" {
		return ""
	}
	prefix := ""
	body := key
	// API key prefixes are operator-configurable ("kr_", "token_", ...), so
	// keep whatever short "<word>_" prefix the key carries.
	if i := strings.IndexByte(key, '_'); i > 0 && i <= 12 {
		prefix, body = key[:i+1], key[i+1:]
	}
	if len(body) <= 8 {
		return prefix + "…"
	}
	return prefix + body[:4] + "…" + body[len(body)-4:]
}

// publicBaseURL derives the externally usable base URL from the request. It
// honors a forwarded host/proto when present (reverse proxy), else falls back
// to the configured listen address.
func (s *Server) publicBaseURL(r *http.Request) string {
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	if host == "" {
		host = s.cfg.Addr()
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

// mountCLITools registers the CLI tool auto-config endpoints.
func (s *Server) mountCLITools(r chi.Router) {
	r.Get("/cli-tools", s.handleCLITools)
	r.Get("/cli-tools/{toolId}", s.handleCLITool)
	r.Post("/cli-tools/{toolId}/configure", s.handleCLIToolConfigure)
	r.Post("/cli-tools/{toolId}/remove", s.handleCLIToolRemove)
}
