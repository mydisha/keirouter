package gateway

import (
	"fmt"
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
	scope := chi.URLParam(r, "server")
	route := "/mcp"
	if scope != "" {
		route = "/mcp/{server}"
	}
	traceID, spanID := audit.ParseTraceparent(r.Header.Get("traceparent"))
	tr := s.traceOf(r)
	out := s.mcp.Serve(w, r, mcp.Caller{
		KeyID:     key.ID,
		RequestID: chimiddleware.GetReqID(r.Context()),
		Client:    detectClient(r),
		TraceID:   traceID,
		SpanID:    spanID,
		ClientIP:  extractIP(r),
		UserAgent: r.UserAgent(),
		Route:     route,
	}, scope)
	tr.mcpDone(out)
}

// mcpDone writes the console's summary line for one /mcp request: which
// tool was called, how it ended, and why when it failed. Tool failures
// travel inside HTTP 200 responses, so the line is built from the outcome,
// not from the status code.
func (t *reqTrace) mcpDone(o mcp.Outcome) {
	if !t.claim() {
		return
	}
	e := t.base
	e.Final = true
	e.LatencyMs = t.elapsedMs()
	e.Status = o.HTTPStatus
	if o.Server != "" {
		e.Provider = "mcp/" + o.Server
	}
	dur := humanDuration(e.LatencyMs)
	method := o.Method
	if method == "" {
		method = "request"
	}

	var level, msg, hint string
	switch {
	case method == "tools/call" && o.CallStatus != "":
		e.Model = o.Tool
		level, hint = mcpCallLevel(o.CallStatus), mcpCallHint(o.CallStatus)
		msg = fmt.Sprintf("MCP · %s · %s · %s", o.Tool, strings.ReplaceAll(o.CallStatus, "_", " "), dur)
		if o.CallStatus != mcp.StatusSuccess && o.Error != "" {
			msg += " · " + o.Error
		}
	case o.HTTPStatus >= 400:
		level, hint = "WARN", statusHint(o.HTTPStatus)
		msg = fmt.Sprintf("MCP · %s rejected · %s", method, orDash(o.Error))
	case method == "tools/call":
		e.Model = o.Tool
		level = "WARN"
		msg = fmt.Sprintf("MCP · %s · %s", orDash(o.Tool), orDash(o.Error))
		hint = "The tool does not exist, is hidden on its server, or this key may not use it."
	case method == "tools/list":
		level = "DEBUG"
		msg = fmt.Sprintf("MCP · tools/list · %d tool%s · %s", o.ToolCount, plural(o.ToolCount), dur)
	default:
		level = "DEBUG"
		msg = fmt.Sprintf("MCP · %s · %s", method, dur)
	}
	e.Level, e.Message, e.Hint = level, msg, hint

	era := "legacy"
	if o.Modern {
		era = "modern"
	}
	if o.Version != "" {
		era += " " + o.Version
	}
	var d strings.Builder
	writeField(&d, "Method", method)
	writeField(&d, "Tool", o.Tool)
	writeField(&d, "Server", o.Server)
	writeField(&d, "Result", o.CallStatus)
	writeField(&d, "Error", o.Error)
	writeField(&d, "Protocol", era)
	writeField(&d, "Key", e.Key)
	writeField(&d, "Client", e.Client)
	if o.UpstreamMS > 0 {
		writeField(&d, "Upstream", fmt.Sprintf("%dms", o.UpstreamMS))
	}
	writeField(&d, "Duration", fmt.Sprintf("%dms", e.LatencyMs))
	e.Detail = d.String()
	t.buf.Add(e)
}

func mcpCallLevel(status string) string {
	switch status {
	case mcp.StatusSuccess:
		return "INFO"
	case mcp.StatusUpstreamError, mcp.StatusTimeout:
		return "ERROR"
	default:
		return "WARN"
	}
}

func mcpCallHint(status string) string {
	switch status {
	case mcp.StatusToolError:
		return "The tool ran and reported an error. The model receives the message and can retry with other arguments."
	case mcp.StatusDenied:
		return "This key may not use the tool. Change the key's MCP access, or the tools the server exposes."
	case mcp.StatusRateLimited:
		return "The key hit its MCP limit (mcp.default_rpm or mcp.default_concurrency). It is counted apart from the key's LLM limits."
	case mcp.StatusUpstreamError:
		return "The MCP server failed, rejected KeiRouter's credentials, or is paused after repeated failures. Check its status on the MCP servers page."
	case mcp.StatusTimeout:
		return "The MCP server did not answer within its timeout. Raise the server's timeout or check the server."
	}
	return ""
}

// isMCPPath reports whether a path belongs to the public MCP endpoint, so
// errors raised before the handler use a JSON-RPC envelope.
func isMCPPath(p string) bool { return p == "/mcp" || strings.HasPrefix(p, "/mcp/") }
