package vault

import (
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	key, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	sealer, err := crypto.NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return New(sealer)
}

func TestSealOpenAPIKey(t *testing.T) {
	v := newTestVault(t)

	var acc store.Account
	acc.ID = "acc-1"
	secret := NewSecret{
		APIKey:   "sk-super-secret-123",
		Metadata: map[string]string{"base_url": "https://api.example.com", "region": "us"},
	}
	if err := v.Seal(&acc, secret); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Ciphertext must not contain the plaintext.
	if acc.SecretCiphertext == "" || acc.SecretCiphertext == secret.APIKey {
		t.Fatalf("secret not sealed: %q", acc.SecretCiphertext)
	}

	creds, err := v.Open(acc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if creds.APIKey != secret.APIKey {
		t.Fatalf("APIKey = %q, want %q", creds.APIKey, secret.APIKey)
	}
	if creds.AccountID != "acc-1" {
		t.Fatalf("AccountID = %q, want acc-1", creds.AccountID)
	}
	if creds.BaseURL != "https://api.example.com" {
		t.Fatalf("BaseURL = %q, want https://api.example.com", creds.BaseURL)
	}
	// base_url is lifted out of Extra.
	if _, ok := creds.Extra["base_url"]; ok {
		t.Fatal("base_url should be removed from Extra")
	}
	if creds.Extra["region"] != "us" {
		t.Fatalf("Extra[region] = %q, want us", creds.Extra["region"])
	}
}

func TestSealOpenTokens(t *testing.T) {
	v := newTestVault(t)

	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	var acc store.Account
	acc.ID = "acc-2"
	secret := NewSecret{
		AccessToken:  "access-tok",
		RefreshToken: "refresh-tok",
		ExpiresAt:    &exp,
	}
	if err := v.Seal(&acc, secret); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if acc.TokenExpiresAt == nil || !acc.TokenExpiresAt.Equal(exp) {
		t.Fatalf("TokenExpiresAt = %v, want %v", acc.TokenExpiresAt, exp)
	}

	creds, err := v.Open(acc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if creds.AccessToken != "access-tok" {
		t.Fatalf("AccessToken = %q, want access-tok", creds.AccessToken)
	}

	refresh, err := v.OpenRefreshToken(acc)
	if err != nil {
		t.Fatalf("OpenRefreshToken: %v", err)
	}
	if refresh != "refresh-tok" {
		t.Fatalf("refresh = %q, want refresh-tok", refresh)
	}
}

func TestSealDefaultsMetadata(t *testing.T) {
	v := newTestVault(t)
	var acc store.Account
	if err := v.Seal(&acc, NewSecret{APIKey: "k"}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Empty metadata is normalized to "{}" so Open can parse it.
	if acc.Metadata != "{}" {
		t.Fatalf("Metadata = %q, want {}", acc.Metadata)
	}
	if _, err := v.Open(acc); err != nil {
		t.Fatalf("Open with default metadata: %v", err)
	}
}

func TestOpenRefreshTokenMissing(t *testing.T) {
	v := newTestVault(t)
	var acc store.Account
	acc.ID = "acc-3"
	if _, err := v.OpenRefreshToken(acc); err == nil {
		t.Fatal("expected error opening missing refresh token")
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	// Seal with one vault, try to open with a vault holding a different key.
	v1 := newTestVault(t)
	v2 := newTestVault(t)

	var acc store.Account
	if err := v1.Seal(&acc, NewSecret{APIKey: "secret"}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := v2.Open(acc); err == nil {
		t.Fatal("Open with wrong master key should fail")
	}
}

func TestOpenInvalidMetadata(t *testing.T) {
	v := newTestVault(t)
	acc := store.Account{ID: "acc-4", Metadata: "{not-json"}
	if _, err := v.Open(acc); err == nil {
		t.Fatal("expected error for invalid metadata JSON")
	}
}

func TestSealerAccessor(t *testing.T) {
	v := newTestVault(t)
	if v.Sealer() == nil {
		t.Fatal("Sealer() returned nil")
	}
}

func TestSealOpenSecretFields(t *testing.T) {
	v := newTestVault(t)
	var acc store.Account
	secret := NewSecret{
		APIKey:       "",
		Metadata:     map[string]string{"aws_region": "eu-west-1", "aws_access_key_id": "AKID"},
		SecretFields: map[string]string{"aws_secret_access_key": "s3cr3t", "aws_session_token": ""},
	}
	if err := v.Seal(&acc, secret); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if acc.SecretCiphertext == "" {
		t.Fatal("secret fields were not sealed")
	}
	creds, err := v.Open(acc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if creds.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", creds.APIKey)
	}
	if creds.Extra["aws_secret_access_key"] != "s3cr3t" || creds.Extra["aws_region"] != "eu-west-1" {
		t.Fatalf("Extra = %v", creds.Extra)
	}
	if _, ok := creds.Extra["aws_session_token"]; ok {
		t.Fatal("empty secret fields must not be stored")
	}
}

func TestSealSecretFieldsKeepsPlainKeyFormat(t *testing.T) {
	v := newTestVault(t)
	var acc store.Account
	if err := v.Seal(&acc, NewSecret{APIKey: "sk-1", SecretFields: map[string]string{"x": ""}}); err != nil {
		t.Fatal(err)
	}
	blob, err := v.Sealer().OpenString(crypto.Sealed{WrappedDEK: acc.SecretWrappedDEK, Ciphertext: acc.SecretCiphertext})
	if err != nil {
		t.Fatal(err)
	}
	if blob != "sk-1" {
		t.Fatalf("plain keys must stay unwrapped, got %q", blob)
	}
}
