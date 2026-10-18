package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
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
		writeError(w, http.StatusUnauthorized, "missing API key")
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
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		s.log.Error("auth lookup failed", "err", err)
		tr.reject(http.StatusInternalServerError, "Authentication lookup failed", err.Error(),
			"KeiRouter could not check the key against its database. Check database health.")
		writeError(w, http.StatusInternalServerError, "authentication error")
		return
	}
	tr.base.Key = key.Name

	// Bansos anti-resale notice for endpoints with no text content
	// (embeddings, images, audio, video). The key-id gate is exact-match so a
	// regular user key can never receive this header.
	if notice := s.bansosNoticeText(r.Context(), key.ID); notice != "" {
		w.Header().Set("X-Bansos-Notice", notice)
	}

	ctx := context.WithValue(r.Context(), apiKeyCtxKey, key)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// extractToken pulls the API key from standard header locations.
func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(auth)
	}
	if k := r.Header.Get("x-api-key"); k != "" {
		return strings.TrimSpace(k)
	}
	return ""
}

// loopbackOnly rejects non-loopback clients when bind-loopback-only is set. This
// guards the dashboard/admin surface in local single-user mode.
// securityHeaders sets headers every response gets: nosniff stops a browser
// from treating a JSON or text body as HTML, and the referrer policy keeps
// full dashboard URLs (which may carry ids) off other sites.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

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
