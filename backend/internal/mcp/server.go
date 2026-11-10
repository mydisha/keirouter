package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// Upstream auth types.
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthHeader = "header"
	AuthBasic  = "basic"
)

// Secret is the sealed part of a server's configuration.
type Secret struct {
	Secret        string            `json:"secret,omitempty"`
	Username      string            `json:"username,omitempty"`
	StaticHeaders map[string]string `json:"static_headers,omitempty"`
}

// SealSecret encrypts a secret blob into the server's sealed columns.
func SealSecret(sealer *crypto.Sealer, s *store.MCPServer, sec Secret) error {
	if sec.Secret == "" && sec.Username == "" && len(sec.StaticHeaders) == 0 {
		s.SecretWrappedDEK, s.SecretCipher = "", ""
		return nil
	}
	raw, err := json.Marshal(sec)
	if err != nil {
		return err
	}
	sealed, err := sealer.Seal(raw)
	if err != nil {
		return fmt.Errorf("seal mcp secret: %w", err)
	}
	s.SecretWrappedDEK, s.SecretCipher = sealed.WrappedDEK, sealed.Ciphertext
	return nil
}

// OpenSecret decrypts a server's sealed columns. An unsealed server yields an
// empty Secret.
func OpenSecret(sealer *crypto.Sealer, s store.MCPServer) (Secret, error) {
	if s.SecretCipher == "" {
		return Secret{}, nil
	}
	raw, err := sealer.Open(crypto.Sealed{WrappedDEK: s.SecretWrappedDEK, Ciphertext: s.SecretCipher})
	if err != nil {
		return Secret{}, fmt.Errorf("open mcp secret: %w", err)
	}
	var sec Secret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return Secret{}, fmt.Errorf("open mcp secret: %w", err)
	}
	return sec, nil
}

// TargetFor builds the request target for a server: URL, auth header and
// static headers. An empty secret sends no auth header, which lets servers
// with optional keys run keyless.
func TargetFor(s store.MCPServer, sec Secret) Target {
	h := http.Header{}
	for k, v := range sec.StaticHeaders {
		h.Set(k, v)
	}
	switch s.AuthType {
	case AuthBearer:
		if sec.Secret != "" {
			h.Set("Authorization", "Bearer "+sec.Secret)
		}
	case AuthHeader:
		if sec.Secret != "" && s.AuthHeader != "" {
			h.Set(s.AuthHeader, sec.Secret)
		}
	case AuthBasic:
		if sec.Username != "" || sec.Secret != "" {
			h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(sec.Username+":"+sec.Secret)))
		}
	}
	return Target{Key: s.ID, URL: s.URL, Headers: h, Version: s.ProtocolVersion}
}
