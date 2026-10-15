package connectors

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Oracle Cloud API request signing. OCI authenticates API-key callers with
// the HTTP Signatures scheme (draft-cavage-http-signatures-08) using
// RSA-SHA256: the caller signs a canonical string built from selected request
// headers and sends the signature in the Authorization header together with
// a keyId of "<tenancy OCID>/<user OCID>/<key fingerprint>".
//
// Requests without a body sign date, (request-target) and host. Requests with
// a body additionally sign content-length, content-type and x-content-sha256
// (the base64 SHA-256 of the body), which binds the payload to the signature.

// ociSignedHeadersNoBody / ociSignedHeadersBody list the signed headers in the
// order the Oracle SDKs use. The order is declared in the Authorization
// header, so the server rebuilds the same signing string.
var (
	ociSignedHeadersNoBody = []string{"date", "(request-target)", "host"}
	ociSignedHeadersBody   = []string{"date", "(request-target)", "host", "content-length", "content-type", "x-content-sha256"}
)

// ociSigner holds the parsed credential material for one OCI API key.
type ociSigner struct {
	keyID string
	key   *rsa.PrivateKey
}

// ociSigningString builds the canonical string to sign. headers must carry a
// value for every name except "(request-target)", which is target.
func ociSigningString(names []string, target string, headers map[string]string) string {
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(name)
		b.WriteString(": ")
		if name == "(request-target)" {
			b.WriteString(target)
		} else {
			b.WriteString(headers[name])
		}
	}
	return b.String()
}

// ociSignRequest signs one request and returns the headers the caller must
// send with it (Authorization, Date and, for requests with a body, the
// content headers). Host is not returned: the HTTP client derives it from the
// URL, which is exactly the value that was signed. The function is pure apart
// from the RSA operation, which is deterministic for PKCS#1 v1.5.
func ociSignRequest(method, rawURL string, body []byte, s ociSigner, now time.Time) (map[string]string, error) {
	if s.key == nil {
		return nil, errors.New("oci: signing key is not loaded")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("oci: parse request url: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("oci: request url has no host")
	}

	signed := map[string]string{
		"date": now.UTC().Format(http.TimeFormat),
		"host": u.Host,
	}
	names := ociSignedHeadersNoBody
	if ociMethodHasBody(method) {
		sum := sha256.Sum256(body)
		signed["content-length"] = strconv.Itoa(len(body))
		signed["content-type"] = "application/json"
		signed["x-content-sha256"] = base64.StdEncoding.EncodeToString(sum[:])
		names = ociSignedHeadersBody
	}

	// RequestURI is the escaped path plus query exactly as the client sends
	// it on the request line, which is what the server signs against.
	target := strings.ToLower(method) + " " + u.RequestURI()
	digest := sha256.Sum256([]byte(ociSigningString(names, target, signed)))
	sig, err := rsa.SignPKCS1v15(nil, s.key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("oci: sign request: %w", err)
	}

	out := make(map[string]string, len(signed)+1)
	for k, v := range signed {
		if k != "host" {
			out[k] = v
		}
	}
	out["authorization"] = fmt.Sprintf(`Signature version="1",keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		s.keyID, strings.Join(names, " "), base64.StdEncoding.EncodeToString(sig))
	return out, nil
}

func ociMethodHasBody(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// parseOCIPrivateKey decodes an RSA private key in PKCS#1 ("RSA PRIVATE KEY")
// or PKCS#8 ("PRIVATE KEY") PEM form. Keys pasted through single-line inputs
// often carry literal "\n" sequences; those are restored first. Encrypted keys
// are rejected with an actionable message: the account form has no passphrase
// input and the standard library cannot decrypt PKCS#8 encrypted keys.
func parseOCIPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	s := strings.TrimSpace(pemText)
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, errors.New("the API private key is not a PEM block (expected -----BEGIN PRIVATE KEY-----)")
	}
	if block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
		return nil, errors.New("the API private key is passphrase-protected; export an unencrypted copy " +
			"(openssl rsa -in key.pem -out key_plain.pem) and paste that instead")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("the API private key could not be parsed (PKCS#1): %w", err)
		}
		return k, nil
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("the API private key could not be parsed (PKCS#8): %w", err)
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("the API private key is not an RSA key; OCI API signing keys must be RSA")
		}
		return rk, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block %q for the API private key", block.Type)
	}
}

// ociKeyCacheMax bounds the parsed-key cache. Keys are keyed by a hash of the
// fingerprint and PEM text, so the bound only matters for deployments that
// rotate many distinct keys.
const ociKeyCacheMax = 256

// ociKeyCache memoizes parsed private keys so a request does not re-parse
// the PEM (an RSA key parse costs far more than the signature itself).
var ociKeyCache = struct {
	sync.Mutex
	keys map[string]*rsa.PrivateKey
}{keys: map[string]*rsa.PrivateKey{}}

// ociSignerFor resolves the signer for an account's credentials, parsing the
// key once per fingerprint+key pair. Missing fields and unusable keys are
// reported as auth errors without echoing any secret material.
func ociSignerFor(provider string, creds core.Credentials) (ociSigner, error) {
	tenancy := extraValue(creds, "oci_tenancy")
	user := extraValue(creds, "oci_user")
	fingerprint := extraValue(creds, "oci_fingerprint")
	keyPEM := extraValue(creds, "oci_key")
	if keyPEM == "" {
		// Accept the key in the generic secret slot as well.
		keyPEM = strings.TrimSpace(creds.APIKey)
	}
	var missing []string
	for _, f := range []struct{ name, v string }{
		{"tenancy OCID", tenancy}, {"user OCID", user}, {"key fingerprint", fingerprint}, {"API private key", keyPEM},
	} {
		if f.v == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return ociSigner{}, &core.ProviderError{Kind: core.ErrAuth, Scope: core.FailureScopeAccount, Provider: provider,
			Message: "missing OCI credential fields: " + strings.Join(missing, ", ")}
	}

	cacheKey := tokenCacheKey("oci-key", fingerprint, keyPEM)
	ociKeyCache.Lock()
	key, ok := ociKeyCache.keys[cacheKey]
	ociKeyCache.Unlock()
	if !ok {
		parsed, err := parseOCIPrivateKey(keyPEM)
		if err != nil {
			return ociSigner{}, &core.ProviderError{Kind: core.ErrAuth, Scope: core.FailureScopeAccount, Provider: provider,
				Message: err.Error()}
		}
		ociKeyCache.Lock()
		if len(ociKeyCache.keys) >= ociKeyCacheMax {
			ociKeyCache.keys = map[string]*rsa.PrivateKey{}
		}
		ociKeyCache.keys[cacheKey] = parsed
		ociKeyCache.Unlock()
		key = parsed
	}
	return ociSigner{keyID: tenancy + "/" + user + "/" + fingerprint, key: key}, nil
}
