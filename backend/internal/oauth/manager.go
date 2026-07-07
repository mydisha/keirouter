package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

// refreshSkew refreshes a token this long before its actual expiry, so an
// in-flight request never races a just-expired token.
const refreshSkew = 60 * time.Second

// errNoRefreshConfig signals that a provider has no refresh configuration. It
// is handled differently by EnsureFresh (fall back to the stale token) and
// ForceRefresh (hard error), so it is propagated rather than handled inside the
// shared refresh core.
var errNoRefreshConfig = errors.New("oauth: no refresh config")

// TokenManager refreshes expiring OAuth access tokens just-in-time. It is
// consulted by the dispatcher before opening an account's credentials.
type TokenManager struct {
	vault    *vault.Vault
	accounts *store.AccountRepo

	// refreshGroup collapses concurrent refreshes of the same account into a
	// single upstream call. Coding agents fire many requests in quick
	// succession; without this, several goroutines would each call the token
	// endpoint with the same refresh token. Providers that rotate refresh
	// tokens (Kiro/AWS SSO OIDC, Cline, ...) accept the first call and reject
	// the rest with refresh_token_reused / invalid_grant, which would wrongly
	// flag the account as needing reconnect even though the refresh succeeded.
	refreshGroup singleflight.Group

	// refresh performs the token exchange + persistence for one account. It
	// defaults to refreshAndPersist; tests override it to observe how many
	// times the work actually runs under concurrent callers.
	refresh func(context.Context, store.Account) (store.Account, error)
}

// NewTokenManager builds a TokenManager.
func NewTokenManager(v *vault.Vault, accounts *store.AccountRepo) *TokenManager {
	m := &TokenManager{vault: v, accounts: accounts}
	m.refresh = m.refreshAndPersist
	return m
}

// EnsureFresh returns an account whose OAuth access token is valid, refreshing
// it in place (and persisting the new tokens) when it is expired or about to
// expire. Non-OAuth accounts and accounts without an expiry are returned
// unchanged. A refresh failure is returned so the dispatcher can skip the
// account and fall back.
func (m *TokenManager) EnsureFresh(ctx context.Context, acc store.Account) (store.Account, error) {
	if m == nil || m.vault == nil || m.accounts == nil {
		return acc, nil
	}
	if acc.AuthKind != store.AuthOAuth || acc.TokenExpiresAt == nil {
		return acc, nil
	}
	if time.Until(*acc.TokenExpiresAt) > refreshSkew {
		return acc, nil // still valid
	}

	out, err := m.dedupedRefresh(ctx, acc)
	if errors.Is(err, errNoRefreshConfig) {
		// No refresh config; let the dispatcher try the (possibly stale) token.
		return acc, nil
	}
	return out, err
}

// ForceRefresh unconditionally refreshes an OAuth account's access token,
// bypassing the local expiry check. Used when the upstream API rejects the
// current token even though it hasn't reached its local expiry (tokens can be
// invalidated server-side before expiry).
func (m *TokenManager) ForceRefresh(ctx context.Context, acc store.Account) (store.Account, error) {
	if m == nil || m.vault == nil || m.accounts == nil {
		return acc, fmt.Errorf("oauth: token manager not configured")
	}
	if acc.AuthKind != store.AuthOAuth {
		return acc, fmt.Errorf("oauth: account %s is not OAuth", acc.ID)
	}

	out, err := m.dedupedRefresh(ctx, acc)
	if errors.Is(err, errNoRefreshConfig) {
		return acc, fmt.Errorf("oauth: no refresh config for provider %s", acc.Provider)
	}
	return out, err
}

// dedupedRefresh collapses concurrent refreshes of the same account into one
// upstream exchange via singleflight, then returns the shared result to every
// caller. This prevents a burst of requests on an expiring account from each
// spending the (often single-use) refresh token.
func (m *TokenManager) dedupedRefresh(ctx context.Context, acc store.Account) (store.Account, error) {
	work := m.refresh
	if work == nil {
		work = m.refreshAndPersist
	}
	v, err, _ := m.refreshGroup.Do(acc.ID, func() (any, error) {
		return work(ctx, acc)
	})
	out, ok := v.(store.Account)
	if !ok {
		out = acc
	}
	return out, err
}

// refreshAndPersist performs the actual token exchange for an account, seals
// the new tokens into the record, and persists them. It is the shared core of
// EnsureFresh and ForceRefresh and runs under singleflight so only one
// goroutine per account executes it at a time.
func (m *TokenManager) refreshAndPersist(ctx context.Context, acc store.Account) (store.Account, error) {
	refreshToken, err := m.vault.OpenRefreshToken(acc)
	if err != nil {
		return acc, fmt.Errorf("oauth: no refresh token for account %s: %w", acc.ID, err)
	}

	var tokens *Tokens
	if acc.Provider == "kiro" {
		// Kiro refreshes through AWS SSO OIDC (Builder ID / IDC, using the stored
		// client credentials) or the Kiro desktop social auth service (imported).
		tokens, err = refreshKiro(ctx, acc, refreshToken)
	} else if acc.Provider == "codebuddy" {
		tokens, err = refreshCodebuddy(ctx, refreshToken)
	} else {
		cfg, ok := ConfigFor(acc.Provider)
		if !ok {
			return acc, errNoRefreshConfig
		}
		tokens, err = cfg.Refresh(ctx, refreshToken)
	}
	if err != nil {
		// Permanent refresh failures (token_revoked, invalid_grant, etc.) mean
		// the refresh token itself is dead. Mark the account so the dashboard
		// shows a "Reconnect Required" badge and the dispatcher skips it.
		if IsPermanentRefresh(err) {
			if setErr := m.accounts.SetNeedsReconnect(ctx, acc.ID, true); setErr != nil {
				return acc, fmt.Errorf("oauth: mark reconnect: %w (original: %w)", setErr, err)
			}
		}
		return acc, fmt.Errorf("oauth: refresh failed for account %s: %w", acc.ID, err)
	}

	var expiresAt *time.Time
	if tokens.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		expiresAt = &t
	}

	// Seal the new tokens into the account. Passing nil Metadata preserves the
	// existing provider metadata.
	if err := m.vault.Seal(&acc, vault.NewSecret{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    expiresAt,
	}); err != nil {
		return acc, fmt.Errorf("oauth: seal refreshed token: %w", err)
	}
	acc.TokenExpiresAt = expiresAt

	if err := m.accounts.UpdateTokens(ctx, acc); err != nil {
		return acc, fmt.Errorf("oauth: persist refreshed token: %w", err)
	}
	return acc, nil
}

// refreshCodebuddy renews a CodeBuddy account token via the Tencent plugin
// auth refresh endpoint. The refresh token is carried in the X-Refresh-Token
// header (not a form body), matching the official CodeBuddy CLI. The body is
// an empty JSON object.
func refreshCodebuddy(ctx context.Context, refreshToken string) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://copilot.tencent.com/v2/plugin/auth/token/refresh", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("codebuddy: build refresh request: %w", err)
	}
	for k, v := range codebuddyStateHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Refresh-Token", refreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codebuddy: refresh request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("codebuddy: refresh failed (%d): %s", resp.StatusCode, truncate(raw, 300))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("codebuddy: parse refresh response: %w", err)
	}
	if parsed.Code != 0 || parsed.Data.AccessToken == "" {
		msg := parsed.Msg
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("codebuddy: refresh error: %s", msg)
	}
	newRefresh := parsed.Data.RefreshToken
	if newRefresh == "" {
		newRefresh = refreshToken
	}
	return &Tokens{
		AccessToken:  parsed.Data.AccessToken,
		RefreshToken: newRefresh,
		ExpiresIn:    86400,
	}, nil
}

// refreshKiro renews a Kiro account's token. Builder ID / IDC accounts carry the
// SSO OIDC client credentials in their metadata; imported accounts refresh
// through the Kiro desktop social auth service.
func refreshKiro(ctx context.Context, acc store.Account, refreshToken string) (*Tokens, error) {
	meta := map[string]string{}
	if acc.Metadata != "" {
		_ = json.Unmarshal([]byte(acc.Metadata), &meta)
	}
	clientID := meta["kiro_client_id"]
	clientSecret := meta["kiro_client_secret"]
	if clientID != "" && clientSecret != "" {
		client := &KiroClient{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Region:       meta["kiro_region"],
			StartURL:     meta["kiro_start_url"],
		}
		return client.Refresh(ctx, refreshToken)
	}
	// Imported token: refresh via the social auth service.
	return KiroSocialRefresh(ctx, refreshToken)
}
