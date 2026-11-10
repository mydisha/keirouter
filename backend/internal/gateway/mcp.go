package gateway

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/mydisha/keirouter/backend/internal/audit"
	"github.com/mydisha/keirouter/backend/internal/mcp"
)

// mountMCP registers the public MCP endpoint. Streamable HTTP uses POST
// only; GET and DELETE (the legacy stream and session teardown) get 405.
func (s *Server) mountMCP(r chi.Router, limitInFlight func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(s.mcpGuard)
		r.Use(limitInFlight)
		r.Use(s.authMiddleware)
		r.Post("/mcp", s.handleMCP)
		r.Post("/mcp/{server}", s.handleMCP)
	})
	notAllowed := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", http.MethodPost)
		mcp.WriteHTTPError(w, http.StatusMethodNotAllowed, "this MCP endpoint accepts POST only")
	}
	for _, p := range []string{"/mcp", "/mcp/{server}"} {
		r.Get(p, notAllowed)
		r.Delete(p, notAllowed)
	}
}

// mcpGuard rejects requests before authentication when the MCP gateway is
// off or the Origin header names a site that may not call it. The spec asks
// servers to check Origin to stop DNS rebinding from a browser.
func (s *Server) mcpGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.mcp == nil {
			mcp.WriteHTTPError(w, http.StatusServiceUnavailable, "the MCP gateway is not running")
			return
		}
		if !s.mcpOriginAllowed(r) {
			mcp.WriteHTTPError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mcpOriginAllowed accepts requests without Origin (non-browser clients),
// same-origin requests, and origins listed in server.cors_origins.
func (s *Server) mcpOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, o := range s.cfg.Server.CORSOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	key, ok := authedKey(r.Context())
	if !ok {
		mcp.WriteHTTPError(w, http.StatusUnauthorized, "missing API key")
		return
	}
	traceID, _ := audit.ParseTraceparent(r.Header.Get("traceparent"))
	s.mcp.Serve(w, r, mcp.Caller{
		KeyID:     key.ID,
		RequestID: chimiddleware.GetReqID(r.Context()),
		Client:    detectClient(r),
		TraceID:   traceID,
	}, chi.URLParam(r, "server"))
}

// isMCPPath reports whether a path belongs to the public MCP endpoint, so
// errors raised before the handler use a JSON-RPC envelope.
func isMCPPath(p string) bool { return p == "/mcp" || strings.HasPrefix(p, "/mcp/") }
