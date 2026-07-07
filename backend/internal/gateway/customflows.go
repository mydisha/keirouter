package gateway

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/oauth"
)

// mountCustomFlows registers connect endpoints for providers whose flow does
// not fit the generic authorize/exchange or device-code/poll handlers:
// KiloCode (custom device-auth), Qoder (PKCE device-token poll), CodeBuddy
// (browser-poll), and Cursor (import token). Each is mounted under a dedicated
// static prefix so it never collides with the /oauth/{provider} param routes.
func (s *Server) mountCustomFlows(r chi.Router) {
	r.Post("/kilocode/device-start", s.kilocodeDeviceStart)
	r.Post("/kilocode/device-poll", s.kilocodeDevicePoll)

	r.Post("/qoder/device-start", s.qoderDeviceStart)
	r.Post("/qoder/device-poll", s.qoderDevicePoll)

	r.Post("/codebuddy/auth-start", s.codebuddyAuthStart)
	r.Post("/codebuddy/auth-poll", s.codebuddyAuthPoll)

	r.Post("/kimchi/auth-start", s.kimchiAuthStart)
	r.Post("/kimchi/auth-poll", s.kimchiAuthPoll)
	// NOTE: GET /kimchi/callback is mounted at root level (outside session
	// middleware) because it receives an external browser redirect from
	// Kimchi's auth page that carries no dashboard session cookie.
	r.Post("/kimchi/callback-submit", s.kimchiCallbackSubmit)

	r.Post("/cursor/import", s.cursorImport)

	r.Post("/commandcode/import", s.commandcodeImport)
}

// kilocodeDeviceStart begins a KiloCode device-auth request and returns the user
// code + verification URL the dashboard displays while polling.
func (s *Server) kilocodeDeviceStart(w http.ResponseWriter, r *http.Request) {
	dc, err := oauth.KilocodeStartDeviceAuth(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.oauthSessions.Put(dc.DeviceCode, &oauth.Session{
		Provider:   "kilocode",
		Flow:       oauth.FlowDeviceCode,
		DeviceCode: dc.DeviceCode,
		Interval:   dc.Interval,
	})
	writeJSON(w, http.StatusOK, deviceCodeResponse(dc))
}

// kilocodeDevicePoll performs one poll of the KiloCode device-auth status.
func (s *Server) kilocodeDevicePoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
		Label      string `json:"label"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.DeviceCode == "" {
		writeError(w, http.StatusBadRequest, "device_code is required")
		return
	}
	sess, ok := s.oauthSessions.Get(body.DeviceCode)
	if !ok || sess.Provider != "kilocode" {
		writeError(w, http.StatusBadRequest, "session not found or expired; restart the flow")
		return
	}

	result := oauth.KilocodePollToken(r.Context(), body.DeviceCode)
	s.completeCustomPoll(w, r, "kilocode", body.DeviceCode, body.Label, result)
}

// qoderDeviceStart generates the local PKCE/nonce state and returns the browser
// verification URL. The nonce doubles as the poll key.
func (s *Server) qoderDeviceStart(w http.ResponseWriter, r *http.Request) {
	flow, err := oauth.QoderInitiateDeviceFlow()
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "qoder flow init failed"))
		return
	}
	s.oauthSessions.Put(flow.Nonce, &oauth.Session{
		Provider:   "qoder",
		Flow:       oauth.FlowDeviceCode,
		DeviceCode: flow.Nonce,
		Verifier:   flow.Verifier,
		Interval:   2,
		Extra:      map[string]string{"machine_id": flow.MachineID},
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               flow.Nonce,
		"user_code":                 "",
		"verification_uri":          "https://qoder.com/device/selectAccounts",
		"verification_uri_complete": flow.VerificationURIComplete,
		"expires_in":                300,
		"interval":                  2,
	})
}

// qoderDevicePoll polls the Qoder device-token endpoint using the stored nonce,
// verifier, and machine id.
func (s *Server) qoderDevicePoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
		Label      string `json:"label"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.DeviceCode == "" {
		writeError(w, http.StatusBadRequest, "device_code is required")
		return
	}
	sess, ok := s.oauthSessions.Get(body.DeviceCode)
	if !ok || sess.Provider != "qoder" {
		writeError(w, http.StatusBadRequest, "session not found or expired; restart the flow")
		return
	}

	machineID := ""
	if sess.Extra != nil {
		machineID = sess.Extra["machine_id"]
	}
	result := oauth.QoderPollToken(r.Context(), body.DeviceCode, sess.Verifier, machineID)
	s.completeCustomPoll(w, r, "qoder", body.DeviceCode, body.Label, result)
}

// codebuddyAuthStart requests a CodeBuddy login state + browser auth URL.
func (s *Server) codebuddyAuthStart(w http.ResponseWriter, r *http.Request) {
	dc, err := oauth.CodebuddyStartAuth(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.oauthSessions.Put(dc.DeviceCode, &oauth.Session{
		Provider:   "codebuddy",
		Flow:       oauth.FlowDeviceCode,
		DeviceCode: dc.DeviceCode,
		Interval:   dc.Interval,
	})
	writeJSON(w, http.StatusOK, deviceCodeResponse(dc))
}

// codebuddyAuthPoll polls the CodeBuddy token endpoint with the login state.
func (s *Server) codebuddyAuthPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
		Label      string `json:"label"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.DeviceCode == "" {
		writeError(w, http.StatusBadRequest, "device_code is required")
		return
	}
	sess, ok := s.oauthSessions.Get(body.DeviceCode)
	if !ok || sess.Provider != "codebuddy" {
		writeError(w, http.StatusBadRequest, "session not found or expired; restart the flow")
		return
	}

	result := oauth.CodebuddyPollToken(r.Context(), body.DeviceCode)
	s.completeCustomPoll(w, r, "codebuddy", body.DeviceCode, body.Label, result)
}

// kimchiAuthStart generates a state token and returns the browser auth URL.
func (s *Server) kimchiAuthStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CallbackBase string `json:"callback_base"`
	}
	_ = decodeJSON(w, r, &body)
	callbackBase := body.CallbackBase
	if callbackBase == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		callbackBase = scheme + "://" + r.Host
	}
	dc, err := oauth.KimchiStartAuth(callbackBase)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.oauthSessions.Put(dc.DeviceCode, &oauth.Session{
		Provider:   "kimchi",
		Flow:       oauth.FlowDeviceCode,
		DeviceCode: dc.DeviceCode,
		Interval:   dc.Interval,
	})
	writeJSON(w, http.StatusOK, deviceCodeResponse(dc))
}

// kimchiAuthPoll checks whether the browser callback has delivered a token.
func (s *Server) kimchiAuthPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
		Label      string `json:"label"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.DeviceCode == "" {
		writeError(w, http.StatusBadRequest, "device_code is required")
		return
	}
	sess, ok := s.oauthSessions.Get(body.DeviceCode)
	if !ok || sess.Provider != "kimchi" {
		writeError(w, http.StatusBadRequest, "session not found or expired; restart the flow")
		return
	}
	result := oauth.KimchiPollToken(body.DeviceCode)
	s.completeCustomPoll(w, r, "kimchi", body.DeviceCode, body.Label, result)
}

// kimchiCallback receives the browser token callback.
func (s *Server) kimchiCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	token := r.URL.Query().Get("token")
	if state == "" {
		writeError(w, http.StatusBadRequest, "state is required")
		return
	}
	if err := oauth.KimchiCallback(r.Context(), state, token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Kimchi Connected</title></head><body><p style="font-family:sans-serif;text-align:center;margin-top:40vh">Kimchi connected. You can close this tab.</p><script>try{window.opener&&window.opener.postMessage({type:"kimchi-callback",status:"success",state:"` + state + `"},"*")}catch(e){}setTimeout(function(){window.close()},500)</script></body></html>`))
}


// kimchiCallbackSubmit processes a manually submitted callback URL.
// Used when the browser redirect didn't reach the callback endpoint or
// the popup didn't auto-close.
func (s *Server) kimchiCallbackSubmit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State string `json:"state"`
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.State == "" || body.Token == "" {
		writeError(w, http.StatusBadRequest, "state and token are required")
		return
	}
	if err := oauth.KimchiCallback(r.Context(), body.State, body.Token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}


// cursorImport validates and stores a token pasted from the Cursor IDE.
func (s *Server) cursorImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Label string `json:"label"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	tokens, err := oauth.CursorImportToken(r.Context(), body.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	label := defaultStr(body.Label, "Cursor (imported)")
	id, perr := s.persistOAuthAccount(r, "cursor", label, tokens)
	if perr != nil {
		writeError(w, http.StatusInternalServerError, perr.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "provider": "cursor"})
}

// commandcodeImport validates and stores a token pasted from the Command Code
// CLI (~/.commandcode/auth.json) or generated at commandcode.ai/studio.
func (s *Server) commandcodeImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Label string `json:"label"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	tokens, err := oauth.CommandCodeImportToken(r.Context(), body.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	label := defaultStr(body.Label, "Command Code (CLI)")
	id, perr := s.persistOAuthAccount(r, "commandcode", label, tokens)
	if perr != nil {
		writeError(w, http.StatusInternalServerError, perr.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "provider": "commandcode"})
}

// completeCustomPoll maps a custom-flow PollResult to the standard poll
// response shape, persisting the account on success.
func (s *Server) completeCustomPoll(w http.ResponseWriter, r *http.Request, provider, sessionKey, label string, result oauth.PollResult) {
	if result.Err != nil {
		s.oauthSessions.Delete(sessionKey)
		writeError(w, http.StatusBadGateway, result.Err.Error())
		return
	}
	if result.Pending {
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending", "slow_down": result.SlowDown})
		return
	}

	s.oauthSessions.Delete(sessionKey)
	id, perr := s.persistOAuthAccount(r, provider, label, result.Tokens)
	if perr != nil {
		writeError(w, http.StatusInternalServerError, perr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "complete", "id": id, "provider": provider})
}

// deviceCodeResponse renders a DeviceCode into the JSON shape the dashboard
// device-code modal consumes.
func deviceCodeResponse(dc *oauth.DeviceCode) map[string]any {
	return map[string]any{
		"device_code":               dc.DeviceCode,
		"user_code":                 dc.UserCode,
		"verification_uri":          dc.VerificationURI,
		"verification_uri_complete": dc.VerificationURIComplete,
		"expires_in":                dc.ExpiresIn,
		"interval":                  dc.Interval,
	}
}