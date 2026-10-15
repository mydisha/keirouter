// Package vault turns encrypted account records into usable credentials and
// back. It is the only component that handles plaintext provider secrets, and
// it holds them only transiently: secrets are sealed before they touch the
// store and opened just-in-time for an upstream call.
package vault

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// Vault seals and opens account secrets using the envelope sealer.
type Vault struct {
	sealer *crypto.Sealer
}

// New builds a Vault from a sealer.
func New(sealer *crypto.Sealer) *Vault {
	return &Vault{sealer: sealer}
}

// Sealer exposes the underlying envelope sealer for portable export/import,
// where sealed blobs must be re-keyed between the master key and a passphrase.
func (v *Vault) Sealer() *crypto.Sealer { return v.sealer }

// NewSecret describes a plaintext credential to seal into an account record.
type NewSecret struct {
	// APIKey is set for api_key accounts.
	APIKey string
	// AccessToken / RefreshToken / ExpiresAt are set for oauth accounts.
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	// Metadata holds non-secret provider config (base_url, region, ...).
	Metadata map[string]string
	// SecretFields are provider-specific secrets beyond the API key (an AWS
	// secret access key, an OAuth client secret, a PEM private key). They are
	// sealed together with APIKey and surface in Credentials.Extra on Open.
	SecretFields map[string]string
}

// secretFieldsPrefix marks a sealed API-key blob that also carries secret
// provider fields as JSON. Plain keys never start with it.
const secretFieldsPrefix = "kr-fields-v1:"

// packSecret combines the API key with secret fields into one sealable blob.
func packSecret(apiKey string, fields map[string]string) (string, error) {
	clean := make(map[string]string, len(fields))
	for k, v := range fields {
		if v != "" {
			clean[k] = v
		}
	}
	if len(clean) == 0 {
		return apiKey, nil
	}
	clean[apiKeyField] = apiKey
	raw, err := json.Marshal(clean)
	if err != nil {
		return "", err
	}
	return secretFieldsPrefix + string(raw), nil
}

// apiKeyField holds the API key inside a packed secret blob.
const apiKeyField = "api_key"

// unpackSecret splits a sealed blob back into the API key and secret fields.
func unpackSecret(blob string) (string, map[string]string, error) {
	if !strings.HasPrefix(blob, secretFieldsPrefix) {
		return blob, nil, nil
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(blob[len(secretFieldsPrefix):]), &fields); err != nil {
		return "", nil, fmt.Errorf("vault: parse secret fields: %w", err)
	}
	key := fields[apiKeyField]
	delete(fields, apiKeyField)
	return key, fields, nil
}

// Seal encrypts the secret material of an account in place, replacing the
// plaintext NewSecret fields with sealed blobs on the store.Account.
func (v *Vault) Seal(acc *store.Account, secret NewSecret) error {
	packed, err := packSecret(secret.APIKey, secret.SecretFields)
	if err != nil {
		return fmt.Errorf("vault: pack secret fields: %w", err)
	}
	if packed != "" {
		sealed, err := v.sealer.SealString(packed)
		if err != nil {
			return fmt.Errorf("vault: seal api key: %w", err)
		}
		acc.SecretWrappedDEK = sealed.WrappedDEK
		acc.SecretCiphertext = sealed.Ciphertext
	}
	if secret.AccessToken != "" {
		sealed, err := v.sealer.SealString(secret.AccessToken)
		if err != nil {
			return fmt.Errorf("vault: seal access token: %w", err)
		}
		acc.TokenWrappedDEK = sealed.WrappedDEK
		acc.TokenCiphertext = sealed.Ciphertext
	}
	if secret.RefreshToken != "" {
		sealed, err := v.sealer.SealString(secret.RefreshToken)
		if err != nil {
			return fmt.Errorf("vault: seal refresh token: %w", err)
		}
		acc.RefreshWrappedDEK = sealed.WrappedDEK
		acc.RefreshCiphertext = sealed.Ciphertext
	}
	acc.TokenExpiresAt = secret.ExpiresAt

	if secret.Metadata != nil {
		raw, err := json.Marshal(secret.Metadata)
		if err != nil {
			return fmt.Errorf("vault: marshal metadata: %w", err)
		}
		acc.Metadata = string(raw)
	}
	if acc.Metadata == "" {
		acc.Metadata = "{}"
	}
	return nil
}

// Open decrypts an account's secrets into live core.Credentials for one call.
// The returned credentials must not be persisted by the caller.
func (v *Vault) Open(acc store.Account) (core.Credentials, error) {
	creds := core.Credentials{AccountID: acc.ID, Headers: map[string]string{}}

	meta := map[string]string{}
	if acc.Metadata != "" {
		if err := json.Unmarshal([]byte(acc.Metadata), &meta); err != nil {
			return core.Credentials{}, fmt.Errorf("vault: parse account metadata: %w", err)
		}
	}
	creds.BaseURL = meta["base_url"]
	delete(meta, "base_url")
	creds.Extra = meta

	if acc.SecretCiphertext != "" {
		blob, err := v.sealer.OpenString(crypto.Sealed{WrappedDEK: acc.SecretWrappedDEK, Ciphertext: acc.SecretCiphertext})
		if err != nil {
			return core.Credentials{}, fmt.Errorf("vault: open api key: %w", err)
		}
		key, fields, err := unpackSecret(blob)
		if err != nil {
			return core.Credentials{}, err
		}
		creds.APIKey = key
		for k, val := range fields {
			creds.Extra[k] = val
		}
	}
	if acc.TokenCiphertext != "" {
		tok, err := v.sealer.OpenString(crypto.Sealed{WrappedDEK: acc.TokenWrappedDEK, Ciphertext: acc.TokenCiphertext})
		if err != nil {
			return core.Credentials{}, fmt.Errorf("vault: open access token: %w", err)
		}
		creds.AccessToken = tok
	}
	return creds, nil
}

// OpenRefreshToken decrypts only the refresh token, used by the token-refresh
// service without exposing the rest of the credential set.
func (v *Vault) OpenRefreshToken(acc store.Account) (string, error) {
	if acc.RefreshCiphertext == "" {
		return "", fmt.Errorf("vault: account %s has no refresh token", acc.ID)
	}
	tok, err := v.sealer.OpenString(crypto.Sealed{WrappedDEK: acc.RefreshWrappedDEK, Ciphertext: acc.RefreshCiphertext})
	if err != nil {
		return "", fmt.Errorf("vault: open refresh token: %w", err)
	}
	return tok, nil
}
