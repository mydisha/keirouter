package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Kimchi browser-callback auth flow. The token arrives directly on the
// callback query string — no authorization-code exchange, no PKCE.

const (
	kimchiWebAppURL     = "https://app.kimchi.dev"
	kimchiValidationURL = "https://api.cast.ai/v1/llm/openai/supported-providers"
	kimchiUserInfoURL   = "https://app.kimchi.dev/api/v1/me"
	kimchiUserAgent     = "kimchi/0.1.50"
)

// kimchiSession holds the resolved token from the browser callback.
type kimchiSession struct {
	done   bool
	tokens *Tokens
	err    error
}

// kimchiSessions maps state to session, populated by the callback handler and
// consumed by the poll endpoint.
var (
	kimchiSessionsMu sync.Mutex
	kimchiSessions   = map[string]*kimchiSession{}
)

// KimchiStartAuth generates a state token and builds the browser auth URL.
// The state doubles as the poll key.
func KimchiStartAuth(callbackBase string) (*DeviceCode, error) {
	state := uuid.NewString()
	callbackURL := strings.TrimRight(callbackBase, "/") + "/api/kimchi/callback"
	params := url.Values{"callback": {callbackURL}, "state": {state}}
	authURL := kimchiWebAppURL + "/cli-auth?" + params.Encode()

	kimchiSessionsMu.Lock()
	kimchiSessions[state] = &kimchiSession{}
	kimchiSessionsMu.Unlock()

	return &DeviceCode{
		DeviceCode:              state,
		VerificationURI:         authURL,
		VerificationURIComplete: authURL,
		ExpiresIn:               300,
		Interval:                2,
	}, nil
}

// KimchiCallback processes the token received from the browser redirect.
func KimchiCallback(ctx context.Context, state, token string) error {
	kimchiSessionsMu.Lock()
	sess, ok := kimchiSessions[state]
	kimchiSessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("kimchi: unknown or expired state")
	}

	if token == "" {
		sess.err = fmt.Errorf("kimchi: no token returned")
		sess.done = true
		return sess.err
	}

	if err := kimchiValidateToken(ctx, token); err != nil {
		sess.err = err
		sess.done = true
		return err
	}

	tokens := &Tokens{AccessToken: token, ExpiresIn: 86400}
	kimchiFetchProfile(ctx, tokens)
	sess.tokens = tokens
	sess.done = true
	return nil
}

// KimchiPollToken checks whether the browser callback has resolved.
func KimchiPollToken(state string) PollResult {
	kimchiSessionsMu.Lock()
	sess, ok := kimchiSessions[state]
	kimchiSessionsMu.Unlock()
	if !ok {
		return PollResult{Err: fmt.Errorf("kimchi: session not found or expired; restart the flow")}
	}
	if !sess.done {
		return PollResult{Pending: true}
	}
	if sess.err != nil {
		deleteKimchiSession(state)
		return PollResult{Err: sess.err}
	}
	deleteKimchiSession(state)
	return PollResult{Done: true, Tokens: sess.tokens}
}

func deleteKimchiSession(state string) {
	kimchiSessionsMu.Lock()
	delete(kimchiSessions, state)
	kimchiSessionsMu.Unlock()
}

func kimchiValidateToken(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kimchiValidationURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("kimchi: token invalid or expired")
	case http.StatusForbidden:
		return fmt.Errorf("kimchi: token lacks required scope")
	}
	return nil
}

func kimchiFetchProfile(ctx context.Context, t *Tokens) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kimchiUserInfoURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", kimchiUserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return
	}
	var profile struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return
	}
	t.Email = strings.TrimSpace(profile.Email)
	t.DisplayName = strings.TrimSpace(profile.Name)
	if t.DisplayName == "" {
		t.DisplayName = strings.TrimSpace(profile.Username)
	}
}