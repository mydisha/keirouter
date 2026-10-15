package connectors

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// authHeaders returns the request headers for one upstream call, resolving
// credentials that must be exchanged first (Entra ID service principals,
// Databricks OAuth M2M, Snowflake key-pair JWTs). Providers with a static
// bearer key take the plain headers path.
func (c *OpenAICompatible) authHeaders(ctx context.Context, creds core.Credentials) (map[string]string, error) {
	switch c.id {
	case "azure", "azure-ai":
		if extraValue(creds, "azure_auth") == "entra" {
			tok, err := entraToken(ctx, c.id, creds, "https://cognitiveservices.azure.com/.default")
			if err != nil {
				return nil, err
			}
			return mergeHeaders(map[string]string{"Authorization": bearer(tok)}, creds.Headers), nil
		}
		if c.id == "azure-ai" {
			return mergeHeaders(map[string]string{"api-key": creds.APIKey}, creds.Headers), nil
		}
	case "databricks":
		if extraValue(creds, "databricks_auth") == "oauth" {
			tok, err := databricksToken(ctx, c.id, creds)
			if err != nil {
				return nil, err
			}
			return mergeHeaders(map[string]string{"Authorization": bearer(tok)}, creds.Headers), nil
		}
	case "snowflake":
		return snowflakeHeaders(c.id, creds)
	case "sarvam":
		h := c.headers(creds)
		h["api-subscription-key"] = creds.APIKey
		return h, nil
	}
	return c.headers(creds), nil
}

// endpointURL joins a resource path onto the account base URL, adding the
// query parameters some deployments require on every call.
func (c *OpenAICompatible) endpointURL(creds core.Credentials, path string) string {
	u := joinURL(c.baseURL(creds), path)
	if c.id == "azure-ai" {
		u += "?api-version=" + url.QueryEscape(extraOr(creds, "azure_ai_api_version", "2024-05-01-preview"))
	}
	return u
}

// entraAuthority is the Microsoft identity platform host (a var for tests).
var entraAuthority = "https://login.microsoftonline.com"

// entraToken mints a Microsoft Entra ID access token for a service principal
// (client credentials grant) and caches it until shortly before expiry.
func entraToken(ctx context.Context, provider string, creds core.Credentials, scope string) (string, error) {
	tenant := extraValue(creds, "azure_tenant_id")
	clientID := extraValue(creds, "azure_client_id")
	secret := extraValue(creds, "azure_client_secret")
	if tenant == "" || clientID == "" || secret == "" {
		return "", &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
			Message: "Entra ID tenant ID, client ID and client secret are required"}
	}
	key := tokenCacheKey("entra", tenant, clientID, secret, scope)
	return sharedTokenCache.get(ctx, key, func(ctx context.Context) (exchangedToken, error) {
		endpoint := entraAuthority + "/" + url.PathEscape(tenant) + "/oauth2/v2.0/token"
		return fetchFormToken(ctx, provider, endpoint, url.Values{
			"grant_type":    {"client_credentials"},
			"client_id":     {clientID},
			"client_secret": {secret},
			"scope":         {scope},
		}, nil)
	})
}

// databricksToken exchanges a service principal's OAuth secret for a
// workspace-level access token.
func databricksToken(ctx context.Context, provider string, creds core.Credentials) (string, error) {
	host := strings.TrimRight(extraValue(creds, "databricks_host"), "/")
	clientID := extraValue(creds, "databricks_client_id")
	secret := extraValue(creds, "databricks_client_secret")
	if host == "" || clientID == "" || secret == "" {
		return "", &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
			Message: "Databricks workspace URL, client ID and client secret are required"}
	}
	key := tokenCacheKey("databricks", host, clientID, secret)
	return sharedTokenCache.get(ctx, key, func(ctx context.Context) (exchangedToken, error) {
		basic := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + secret))
		return fetchFormToken(ctx, provider, host+"/oidc/v1/token", url.Values{
			"grant_type": {"client_credentials"},
			"scope":      {"all-apis"},
		}, map[string]string{"Authorization": "Basic " + basic})
	})
}

// snowflakeAccountHost normalizes an account identifier for use in the
// account URL: lower case, underscores replaced by hyphens.
func snowflakeAccountHost(account string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(account)), "_", "-")
}

// snowflakeHeaders authenticates with a programmatic access token or a
// key-pair JWT signed with the user's RSA key.
func snowflakeHeaders(provider string, creds core.Credentials) (map[string]string, error) {
	h := map[string]string{"Accept": "application/json"}
	if extraValue(creds, "snowflake_auth") == "keypair" {
		jwt, err := snowflakeJWT(provider, creds, time.Now())
		if err != nil {
			return nil, err
		}
		h["Authorization"] = bearer(jwt)
		h["X-Snowflake-Authorization-Token-Type"] = "KEYPAIR_JWT"
		return mergeHeaders(h, creds.Headers), nil
	}
	h["Authorization"] = bearer(creds.APIKey)
	h["X-Snowflake-Authorization-Token-Type"] = "PROGRAMMATIC_ACCESS_TOKEN"
	return mergeHeaders(h, creds.Headers), nil
}

// snowflakeJWT builds the key-pair authentication JWT: issuer
// ACCOUNT.USER.SHA256:<public key fingerprint>, subject ACCOUNT.USER, valid
// for one hour. The account part drops any region/cloud suffix.
func snowflakeJWT(provider string, creds core.Credentials, now time.Time) (string, error) {
	account := strings.ToUpper(strings.TrimSpace(extraValue(creds, "snowflake_account")))
	if i := strings.IndexByte(account, '.'); i > 0 {
		account = account[:i]
	}
	user := strings.ToUpper(extraValue(creds, "snowflake_user"))
	pemKey := extraValue(creds, "snowflake_private_key")
	if account == "" || user == "" || pemKey == "" {
		return "", &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
			Message: "Snowflake account, user and private key are required for key-pair auth"}
	}
	key, err := parseRSAPrivateKey(pemKey)
	if err != nil {
		return "", &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
			Message: "invalid Snowflake private key: " + strings.TrimPrefix(err.Error(), "vertex: ")}
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}
	fp := sha256.Sum256(pub)
	qualified := account + "." + user
	claims, _ := json.Marshal(map[string]any{
		"iss": qualified + ".SHA256:" + base64.StdEncoding.EncodeToString(fp[:]),
		"sub": qualified,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	})
	header := b64url([]byte(`{"alg":"RS256","typ":"JWT"}`))
	signingInput := header + "." + b64url(claims)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign snowflake jwt: %w", err)
	}
	return signingInput + "." + b64url(sig), nil
}

// databricksModelSource lists a workspace's chat serving endpoints.
type databricksModelSource struct{ conn *OpenAICompatible }

func (s *databricksModelSource) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	host := strings.TrimRight(extraValue(creds, "databricks_host"), "/")
	if host == "" {
		return nil, fmt.Errorf("databricks workspace URL is not set")
	}
	headers, err := s.conn.authHeaders(ctx, creds)
	if err != nil {
		return nil, err
	}
	raw, err := doJSONMethod(ctx, http.MethodGet, s.conn.id, "models", host+"/api/2.0/serving-endpoints", nil, headers)
	if err != nil {
		return nil, err
	}
	var out struct {
		Endpoints []struct {
			Name  string `json:"name"`
			Task  string `json:"task"`
			State struct {
				Ready string `json:"ready"`
			} `json:"state"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode serving endpoints: %w", err)
	}
	models := make([]ModelSpec, 0, len(out.Endpoints))
	for _, e := range out.Endpoints {
		switch e.Task {
		case "llm/v1/chat", "":
			models = append(models, m(e.Name, e.Name))
		case "llm/v1/embeddings":
			models = append(models, ModelSpec{ID: e.Name, Name: e.Name, Kind: core.ServiceEmbedding})
		}
	}
	return models, nil
}
