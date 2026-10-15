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
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func chatReq(model string) *core.ChatRequest {
	return &core.ChatRequest{Model: model, Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
}

const okChatBody = `{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

func TestEnterpriseAuth_AzureAIUsesAPIKeyHeaderAndVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/chat/completions" || r.URL.Query().Get("api-version") != "2024-05-01-preview" {
			t.Errorf("unexpected url %s", r.URL)
		}
		if r.Header.Get("api-key") != "k1" || r.Header.Get("Authorization") != "" {
			t.Errorf("auth headers: api-key=%q authorization=%q", r.Header.Get("api-key"), r.Header.Get("Authorization"))
		}
		io.WriteString(w, okChatBody)
	}))
	defer srv.Close()
	c := NewOpenAICompatible("azure-ai", "")
	resp, err := c.Chat(context.Background(), chatReq("DeepSeek-R1"), core.Credentials{APIKey: "k1", BaseURL: srv.URL + "/models"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != core.FinishStop {
		t.Fatalf("finish = %v", resp.FinishReason)
	}
}

func TestEnterpriseAuth_EntraTokenCachedAndUsed(t *testing.T) {
	var tokenCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			tokenCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "sec" ||
				r.Form.Get("scope") != "https://cognitiveservices.azure.com/.default" || !strings.Contains(r.URL.Path, "/tenant-1/") {
				t.Errorf("bad token request %s %v", r.URL.Path, r.Form)
			}
			io.WriteString(w, `{"access_token":"entra-tok","expires_in":3600,"token_type":"Bearer"}`)
		default:
			if r.Header.Get("Authorization") != "Bearer entra-tok" || r.Header.Get("api-key") != "" {
				t.Errorf("chat auth = %q / %q", r.Header.Get("Authorization"), r.Header.Get("api-key"))
			}
			io.WriteString(w, okChatBody)
		}
	}))
	defer srv.Close()
	old := entraAuthority
	entraAuthority = srv.URL
	defer func() { entraAuthority = old }()

	creds := core.Credentials{BaseURL: srv.URL + "/models", Extra: map[string]string{
		"azure_auth": "entra", "azure_tenant_id": "tenant-1", "azure_client_id": "cid", "azure_client_secret": "sec",
	}}
	c := NewOpenAICompatible("azure-ai", "")
	for i := 0; i < 3; i++ {
		if _, err := c.Chat(context.Background(), chatReq("m"), creds); err != nil {
			t.Fatal(err)
		}
	}
	if n := tokenCalls.Load(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1", n)
	}
}

func TestEnterpriseAuth_EntraBadSecretIsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided."}`)
	}))
	defer srv.Close()
	old := entraAuthority
	entraAuthority = srv.URL
	defer func() { entraAuthority = old }()
	_, err := entraToken(context.Background(), "azure", core.Credentials{Extra: map[string]string{
		"azure_tenant_id": "t", "azure_client_id": "c", "azure_client_secret": "wrong",
	}}, "scope")
	if pe := core.AsProviderError(err); pe == nil || pe.Kind != core.ErrAuth {
		t.Fatalf("want auth error, got %v", err)
	}
}

func TestEnterpriseAuth_DatabricksOAuthAndPAT(t *testing.T) {
	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oidc/v1/token":
			user, pass, ok := r.BasicAuth()
			_ = r.ParseForm()
			if !ok || user != "sp-id" || pass != "sp-secret" || r.Form.Get("scope") != "all-apis" {
				t.Errorf("bad oauth request: %v %v %v", user, pass, r.Form)
			}
			io.WriteString(w, `{"access_token":"dbx-oauth","expires_in":3600}`)
		case "/serving-endpoints/chat/completions":
			gotAuth.Store(r.Header.Get("Authorization"))
			io.WriteString(w, okChatBody)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewOpenAICompatible("databricks", "{databricks_host}/serving-endpoints")
	oauth := core.Credentials{Extra: map[string]string{"databricks_host": srv.URL, "databricks_auth": "oauth",
		"databricks_client_id": "sp-id", "databricks_client_secret": "sp-secret"}}
	if _, err := c.Chat(context.Background(), chatReq("databricks-claude-sonnet-4-5"), oauth); err != nil {
		t.Fatal(err)
	}
	if gotAuth.Load() != "Bearer dbx-oauth" {
		t.Fatalf("oauth auth = %v", gotAuth.Load())
	}
	pat := core.Credentials{APIKey: "dapi123", Extra: map[string]string{"databricks_host": srv.URL}}
	if _, err := c.Chat(context.Background(), chatReq("m"), pat); err != nil {
		t.Fatal(err)
	}
	if gotAuth.Load() != "Bearer dapi123" {
		t.Fatalf("pat auth = %v", gotAuth.Load())
	}
}

func TestEnterpriseAuth_SnowflakeURLAndPAT(t *testing.T) {
	c := NewOpenAICompatible("snowflake", "https://{snowflake_account}.snowflakecomputing.com/api/v2/cortex/v1")
	creds := core.Credentials{APIKey: "pat", Extra: map[string]string{"snowflake_account": "MyOrg_Acct"}}
	if got := c.chatCompletionsURL(creds, "m"); got != "https://myorg-acct.snowflakecomputing.com/api/v2/cortex/v1/chat/completions" {
		t.Fatalf("url = %s", got)
	}
	h, err := c.authHeaders(context.Background(), creds)
	if err != nil {
		t.Fatal(err)
	}
	if h["Authorization"] != "Bearer pat" || h["X-Snowflake-Authorization-Token-Type"] != "PROGRAMMATIC_ACCESS_TOKEN" {
		t.Fatalf("headers = %v", h)
	}
}

func TestEnterpriseAuth_SnowflakeKeyPairJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	creds := core.Credentials{Extra: map[string]string{"snowflake_auth": "keypair", "snowflake_account": "xy12345.us-east-2.aws",
		"snowflake_user": "svc_user", "snowflake_private_key": pemKey}}
	now := time.Unix(1_700_000_000, 0)
	jwt, err := snowflakeJWT("snowflake", creds, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %s", jwt)
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(payload, &claims)
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	fp := sha256.Sum256(pub)
	if claims["sub"] != "XY12345.SVC_USER" || claims["iss"] != "XY12345.SVC_USER.SHA256:"+base64.StdEncoding.EncodeToString(fp[:]) {
		t.Fatalf("claims = %v", claims)
	}
	if claims["exp"].(float64)-claims["iat"].(float64) != 3600 {
		t.Fatalf("lifetime = %v", claims)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
}

func TestEnterpriseAuth_SarvamSubscriptionHeader(t *testing.T) {
	h, err := NewOpenAICompatible("sarvam", "").authHeaders(context.Background(), core.Credentials{APIKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	if h["api-subscription-key"] != "sk" || h["Authorization"] != "Bearer sk" {
		t.Fatalf("headers = %v", h)
	}
}
