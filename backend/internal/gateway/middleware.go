package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// ctxKey is an unexported context key type to avoid collisions.
type ctxKey int

const apiKeyCtxKey ctxKey = iota

// authedKey returns the authenticated API key record from the request context.
func authedKey(ctx context.Context) (store.APIKey, bool) {
	k, ok := ctx.Value(apiKeyCtxKey).(store.APIKey)
	return k, ok
}

// authMiddleware authenticates the inbound API key from the Authorization
// header (Bearer) or the x-api-key header, attaching the key record to context.
// It rejects unauthenticated requests with 401.
//
// It also opens the request's console trace and guarantees the request ends
// with exactly one summary line on the dashboard console, even when a handler
// returns early without logging (the status and error body are captured).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr := newReqTrace(s.consoleLog, r)
		tr.base.Client = detectClient(r)
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		tap := &errorTap{ww: ww}
		ww.Tee(tap)
		r = r.WithContext(pipeline.WithObserver(r.Context(), tr))

		returned := false
		defer func() {
			if !returned {
				// Panicking: record it and let Recoverer answer the client.
				tr.reject(http.StatusInternalServerError, "Crashed · handler panicked",
					"", "KeiRouter hit an unexpected error while handling this request. The server log has the stack trace.")
				return
			}
			tr.settle(ww.Status(), tap.buf)
		}()
		s.authenticate(ww, r, tr, next)
		returned = true
	})
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request, tr *reqTrace, next http.Handler) {
	token := extractToken(r)
	if token == "" {
		tr.reject(http.StatusUnauthorized, "Rejected · missing API key",
			fmt.Sprintf("IP: %s", extractIP(r)),
			"The request had no Authorization: Bearer or x-api-key header. Configure the client with a KeiRouter API key.")
		writeRequestError(w, r, http.StatusUnauthorized, "missing API key")
		return
	}

	// The key is checked before the per-IP failure limit so a valid key always
	// gets through: clients behind one proxy, or several local tools, share an
	// IP, and one of them sending a stale key must not lock the others out.
	key, err := s.identity.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			if s.authFailureBlocked(w, r) {
				tr.reject(http.StatusTooManyRequests, "Rejected · too many failed logins from this IP",
					fmt.Sprintf("Key: %s\nIP: %s", maskToken(token), extractIP(r)),
					"This IP sent too many invalid API keys; its invalid keys are refused for now (valid keys still work). Check for a client using a stale key.")
				return
			}
			s.recordAuthFailure(r)
			tr.reject(http.StatusUnauthorized, "Rejected · invalid API key",
				fmt.Sprintf("Key: %s\nIP: %s", maskToken(token), extractIP(r)),
				"The key is unknown, revoked or expired. Compare the masked key with the Keys page.")
			writeRequestError(w, r, http.StatusUnauthorized, "invalid API key")
			return
		}
		s.log.Error("auth lookup failed", "err", err)
		tr.reject(http.StatusInternalServerError, "Authentication lookup failed", err.Error(),
			"KeiRouter could not check the key against its database. Check database health.")
		writeRequestError(w, r, http.StatusInternalServerError, "authentication error")
		return
	}
	tr.base.Key = key.Name

	ctx := context.WithValue(r.Context(), apiKeyCtxKey, key)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// dialectForPath names the client dialect an API path speaks, so errors raised
// before a handler runs (auth, limits, body size) use the envelope the
// client's SDK can parse.
func dialectForPath(path string) core.Dialect {
	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		return core.DialectAnthropic
	case strings.HasPrefix(path, "/v1/responses"):
		return core.DialectOpenAIResponses
	case strings.HasPrefix(path, "/v1beta/"):
		return core.DialectGemini
	default:
		return core.DialectOpenAI
	}
}

func writeRequestError(w http.ResponseWriter, r *http.Request, status int, message string) {
	writeDialectError(w, dialectForPath(r.URL.Path), status, message)
}

// echoRequestID returns the request id to the client so a failed call can be
// matched with the gateway's logs and console trace.
func echoRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := chimiddleware.GetReqID(r.Context()); id != "" {
			w.Header().Set(chimiddleware.RequestIDHeader, id)
		}
		next.ServeHTTP(w, r)
	})
}

// extractToken pulls the API key from standard header locations.
func extractToken(r *http.Request) string {
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
			return strings.TrimSpace(auth[7:])
		}
		return auth
	}
	if k := r.Header.Get("x-api-key"); k != "" {
		return strings.TrimSpace(k)
	}
	// Google's SDKs (Gemini CLI, @google/genai) send the key in x-goog-api-key
	// or as ?key=; only the Gemini routes accept those.
	if strings.HasPrefix(r.URL.Path, "/v1beta/") {
		if k := r.Header.Get("x-goog-api-key"); k != "" {
			return strings.TrimSpace(k)
		}
		return strings.TrimSpace(r.URL.Query().Get("key"))
	}
	return ""
}

// loopbackOnly rejects non-loopback clients when bind-loopback-only is set. This
// guards the dashboard/admin surface in local single-user mode.
func (s *Server) loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Security.BindLoopbackOnly {
			next.ServeHTTP(w, r)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			writeError(w, http.StatusForbidden, "dashboard is restricted to loopback access")
			return
		}
		next.ServeHTTP(w, r)
	})
}
