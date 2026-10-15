package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// tokenRefreshSkew renews a cached token this long before it expires so a
// request never leaves with a token that dies in flight.
const tokenRefreshSkew = 2 * time.Minute

// tokenCacheMax bounds the number of cached exchanged tokens. Entries are
// keyed by a hash of the credential material, so the bound only matters for
// deployments that rotate thousands of distinct credentials.
const tokenCacheMax = 4096

// exchangedToken is a short-lived bearer obtained from a credential exchange
// (IAM API key, OAuth client credentials, signed JWT assertion, ...).
type exchangedToken struct {
	Value     string
	ExpiresAt time.Time
}

// tokenCache memoizes exchanged tokens per credential and collapses
// concurrent exchanges for the same credential into one upstream call.
type tokenCache struct {
	mu     sync.Mutex
	tokens map[string]exchangedToken
	group  singleflight.Group
}

var sharedTokenCache = &tokenCache{tokens: map[string]exchangedToken{}}

// tokenCacheKey derives a cache key from a namespace and the credential parts
// that identify the token. Secrets are hashed, never stored as keys.
func tokenCacheKey(namespace string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return namespace + ":" + hex.EncodeToString(h.Sum(nil)[:16])
}

// get returns a cached token for key or runs fetch to mint one. A token whose
// expiry is unknown (zero) is cached for defaultTTL.
func (c *tokenCache) get(ctx context.Context, key string, fetch func(ctx context.Context) (exchangedToken, error)) (string, error) {
	now := time.Now()
	c.mu.Lock()
	if t, ok := c.tokens[key]; ok && now.Add(tokenRefreshSkew).Before(t.ExpiresAt) {
		c.mu.Unlock()
		return t.Value, nil
	}
	c.mu.Unlock()

	v, err, _ := c.group.Do(key, func() (any, error) {
		t, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		if t.Value == "" {
			return nil, fmt.Errorf("token exchange returned an empty token")
		}
		if t.ExpiresAt.IsZero() {
			t.ExpiresAt = time.Now().Add(30 * time.Minute)
		}
		c.mu.Lock()
		if len(c.tokens) >= tokenCacheMax {
			c.evictLocked(time.Now())
		}
		c.tokens[key] = t
		c.mu.Unlock()
		return t.Value, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// invalidate drops a cached token, e.g. after the upstream rejected it.
func (c *tokenCache) invalidate(key string) {
	c.mu.Lock()
	delete(c.tokens, key)
	c.mu.Unlock()
}

// evictLocked removes expired entries, then an arbitrary half if the cache is
// still full. Caller holds c.mu.
func (c *tokenCache) evictLocked(now time.Time) {
	for k, t := range c.tokens {
		if !now.Before(t.ExpiresAt) {
			delete(c.tokens, k)
		}
	}
	if len(c.tokens) < tokenCacheMax {
		return
	}
	n := 0
	for k := range c.tokens {
		delete(c.tokens, k)
		if n++; n >= tokenCacheMax/2 {
			return
		}
	}
}

// oauthTokenResponse is the RFC 6749 token response shape shared by most
// client-credentials endpoints.
type oauthTokenResponse struct {
	AccessToken string      `json:"access_token"`
	ExpiresIn   json.Number `json:"expires_in"`
	Expiration  json.Number `json:"expiration"` // IBM IAM: absolute unix seconds
	ExpiresAt   json.Number `json:"expires_at"` // GigaChat: absolute unix millis
	Token       string      `json:"token"`      // Cloud Pak for Data
	Error       string      `json:"error"`
	ErrorDesc   string      `json:"error_description"`
}

// expiry resolves the absolute expiry from whichever field the endpoint set.
func (r oauthTokenResponse) expiry(now time.Time) time.Time {
	if n, err := r.ExpiresIn.Int64(); err == nil && n > 0 {
		return now.Add(time.Duration(n) * time.Second)
	}
	if n, err := r.Expiration.Int64(); err == nil && n > 0 {
		return time.Unix(n, 0)
	}
	if n, err := r.ExpiresAt.Int64(); err == nil && n > 0 {
		if n > 1e12 { // milliseconds
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

// fetchFormToken posts a form to a token endpoint and decodes the standard
// token response. Errors keep the provider attribution so a bad client secret
// surfaces as an auth failure on the account.
func fetchFormToken(ctx context.Context, provider, endpoint string, form url.Values, headers map[string]string) (exchangedToken, error) {
	body, err := doFormPOST(ctx, provider, "token", endpoint, form, headers)
	if err != nil {
		return exchangedToken{}, asAuthError(provider, err)
	}
	return parseTokenResponse(provider, body)
}

func parseTokenResponse(provider string, body []byte) (exchangedToken, error) {
	var tr oauthTokenResponse
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&tr); err != nil {
		return exchangedToken{}, &core.ProviderError{Kind: core.ErrAuth, Provider: provider,
			Message: "token endpoint returned an unreadable response: " + err.Error(), Cause: err}
	}
	tok := tr.AccessToken
	if tok == "" {
		tok = tr.Token
	}
	if tok == "" {
		msg := tr.ErrorDesc
		if msg == "" {
			msg = tr.Error
		}
		if msg == "" {
			msg = "token endpoint returned no access token"
		}
		return exchangedToken{}, &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: 401, Message: msg}
	}
	return exchangedToken{Value: tok, ExpiresAt: tr.expiry(time.Now())}, nil
}

// asAuthError reclassifies a 4xx from a token endpoint as an auth failure:
// the credential, not the request, is what the upstream rejected.
func asAuthError(provider string, err error) error {
	pe := core.AsProviderError(err)
	if pe == nil || pe.StatusCode < 400 || pe.StatusCode >= 500 || pe.StatusCode == 429 {
		return err
	}
	cp := *pe
	cp.Kind = core.ErrAuth
	cp.Provider = provider
	return &cp
}
