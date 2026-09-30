package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

const (
	portalSessionCookie = "kr_portal_session"
	portalStateCookie   = "kr_portal_state"
	portalStateTTL      = 5 * time.Minute
)

// portalSubject prefixes the Google subject in the portal session token so it
// can never collide with the dashboard "dashboard" subject.
func portalSubject(googleSub string) string { return "portal:" + googleSub }

// portalGoogleSub strips the "portal:" audience prefix to recover the raw
// Google subject stored in portal_users.google_sub.
func portalGoogleSub(sessionSub string) string {
	return strings.TrimPrefix(sessionSub, "portal:")
}

// portalSSOConfigured reports whether Google sign-in is available.
func (s *Server) portalSSOConfigured() bool { return s.portalSSO != nil }

// handlePortalLoginStart sets a CSRF state cookie and redirects to Google.
func (s *Server) handlePortalLoginStart(w http.ResponseWriter, r *http.Request) {
	if !s.portalSSOConfigured() {
		writeError(w, http.StatusServiceUnavailable, "portal sso not configured")
		return
	}
	state, err := randomState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     portalStateCookie,
		Value:    state,
		Path:     "/",
		MaxAge:   int(portalStateTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure(r),
	})
	svc := s.portalSSO.WithRedirect(s.portalRedirectURL(r))
	http.Redirect(w, r, svc.AuthURL(state), http.StatusFound)
}

// handlePortalLoginCallback validates state, exchanges the code, and issues a
// portal session cookie.
func (s *Server) handlePortalLoginCallback(w http.ResponseWriter, r *http.Request) {
	if !s.portalSSOConfigured() {
		writeError(w, http.StatusServiceUnavailable, "portal sso not configured")
		return
	}
	c, err := r.Cookie(portalStateCookie)
	gotState := r.URL.Query().Get("state")
	if err != nil || c.Value == "" || gotState == "" || c.Value != gotState {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	// One-shot state cookie.
	http.SetCookie(w, &http.Cookie{Name: portalStateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}
	svc := s.portalSSO.WithRedirect(s.portalRedirectURL(r))
	id, err := svc.Exchange(r.Context(), code)
	if err != nil {
		s.log.Warn("portal: google exchange failed", "err", err)
		writeError(w, http.StatusUnauthorized, "google sign-in failed")
		return
	}
	tok, err := s.auth.IssuePortalSession(portalSubject(id.Sub), id.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session")
		return
	}
	s.setPortalCookie(w, r, tok)
	http.Redirect(w, r, "/portal", http.StatusFound)
}

// handlePortalStatus reports the portal session's authentication and claim state.
func (s *Server) handlePortalStatus(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(portalSessionCookie)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	sub, ok := s.auth.SessionSubject(c.Value)
	if !ok || !strings.HasPrefix(sub, "portal:") {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	out := map[string]any{"authenticated": true, "claimed": false}
	if email, ok := s.auth.SessionEmail(c.Value); ok {
		out["email"] = email
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err == nil {
		out["claimed"] = true
		out["key_id"] = u.KeyID
	} else if !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePortalClaim binds the authenticated Google user to an API key. The
// user must present the full key, which is verified with argon2 by identity.
func (s *Server) handlePortalClaim(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "api_key is required")
		return
	}
	key, err := s.identity.Authenticate(r.Context(), strings.TrimSpace(body.APIKey))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	email, _ := s.auth.SessionEmail(portalSessionToken(r))
	u := store.PortalUser{
		GoogleSub: portalGoogleSub(sub),
		Email:     email,
		KeyID:     key.ID,
	}
	if err := s.db.PortalUsers().Upsert(r.Context(), u); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "this api key is already claimed by another account")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save claim")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key_id": key.ID})
}

// handlePortalUsage returns usage for the caller's claimed key.
func (s *Server) handlePortalUsage(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "no api key claimed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load portal user")
		return
	}
	key, err := s.identity.Keys().Get(r.Context(), u.KeyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "claimed key no longer exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load key")
		return
	}
	payload, err := s.buildKeyUsageMap(r.Context(), key, parseUsageDays(r))
	if err != nil {
		s.log.Error("portal usage: build failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to build usage")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// handlePortalLogout clears the portal session cookie.
func (s *Server) handlePortalLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     portalSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// portalSessionMiddleware requires a valid portal session.
func (s *Server) portalSessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.portalSubject(r); !ok {
			writeError(w, http.StatusUnauthorized, "portal session required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// portalSubject returns the Google subject of the current portal session.
func (s *Server) portalSubject(r *http.Request) (string, bool) {
	tok := portalSessionToken(r)
	if tok == "" {
		return "", false
	}
	sub, ok := s.auth.SessionSubject(tok)
	if !ok || !strings.HasPrefix(sub, "portal:") {
		return "", false
	}
	return sub, true
}

func portalSessionToken(r *http.Request) string {
	c, err := r.Cookie(portalSessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) setPortalCookie(w http.ResponseWriter, r *http.Request, token string) {
	ttl := s.cfg.PortalSSO.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	http.SetCookie(w, &http.Cookie{
		Name:     portalSessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure(r),
	})
}

// portalRedirectURL returns the configured redirect URL, or derives one from
// the request's public base URL so it works behind a reverse proxy without
// extra configuration.
func (s *Server) portalRedirectURL(r *http.Request) string {
	if u := s.cfg.PortalSSO.RedirectURL; u != "" {
		return u
	}
	return s.publicBaseURL(r) + "/portal/auth/google/callback"
}

// parseUsageDays reads the days query param, defaulting to 30.
func parseUsageDays(r *http.Request) int {
	switch r.URL.Query().Get("days") {
	case "7":
		return 7
	case "14":
		return 14
	case "90":
		return 90
	default:
		return 30
	}
}

// randomState returns a URL-safe random CSRF state.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
