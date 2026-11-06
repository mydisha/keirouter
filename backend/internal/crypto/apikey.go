package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

// KeyPrefix is the human-visible prefix on every issued API key. It lets users
// and secret scanners recognize a KeiRouter key at a glance.
const KeyPrefix = "kr_"

// secretBytes is the entropy of the random portion of an API key.
const secretBytes = 24

// argon2 parameters tuned for interactive verification on commodity hardware.
// These are encoded into each hash so they can evolve without breaking old hashes.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash is returned when a stored hash cannot be parsed.
var ErrInvalidHash = errors.New("crypto: invalid argon2 hash")

// GeneratedKey is the result of minting a new API key. The Plaintext is shown
// to the user exactly once and never persisted; only Hash and Lookup are stored.
type GeneratedKey struct {
	// Plaintext is the full key string the caller uses (e.g. "kr_AbC123...").
	Plaintext string
	// Hash is the argon2id verifier stored in the database. Authentication no
	// longer reads it; it is still written so a release that predates the
	// keyed lookup index can verify keys minted by this one after a rollback.
	Hash string
	// Lookup is the legacy SHA-256 index (LookupHash). It is still written for
	// the same rollback reason; the keyed index comes from KeyLookup.
	Lookup string
	// Display is a masked form safe to show in listings (e.g. "kr_AbC1…7xQ2").
	Display string
}

// GenerateAPIKey mints a new API key, returning the plaintext plus its stored
// verifier. The plaintext is unrecoverable afterward.
func GenerateAPIKey() (GeneratedKey, error) {
	raw := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return GeneratedKey{}, fmt.Errorf("generate key entropy: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	plaintext := KeyPrefix + secret

	hash, err := HashAPIKey(plaintext)
	if err != nil {
		return GeneratedKey{}, err
	}

	return GeneratedKey{
		Plaintext: plaintext,
		Hash:      hash,
		Lookup:    LookupHash(plaintext),
		Display:   maskKey(plaintext),
	}, nil
}

// HashAPIKey produces an argon2id PHC-style hash of the key.
func HashAPIKey(plaintext string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	digest := argon2.IDKey([]byte(plaintext), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// PHC string format: $argon2id$v=19$m=...,t=...,p=...$salt$hash
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

// VerifyAPIKey reports whether plaintext matches the stored argon2id hash,
// using a constant-time comparison.
func VerifyAPIKey(plaintext, encodedHash string) (bool, error) {
	mem, time, threads, salt, want, err := parseArgon2Hash(encodedHash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plaintext), salt, time, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// LookupHash returns the legacy lookup index for a key: hex SHA-256 of the
// plaintext, with no secret. Rows written before the keyed index existed carry
// only this value; KeyLookup.FromLegacy converts it without the plaintext.
func LookupHash(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ErrInvalidLookupHash is returned when a stored legacy lookup hash is not
// 32 bytes of hex.
var ErrInvalidLookupHash = errors.New("crypto: invalid legacy lookup hash")

// KeyLookup computes the keyed lookup index stored for every API key:
// HMAC-SHA256 over the key's SHA-256 digest, under a key derived from the
// master key. Keys are 192 random bits, so a match on this index is as
// conclusive as an argon2 verification; the HMAC key means a copy of the
// database alone can neither test guesses (for imported keys with little
// entropy) nor mint a row that authenticates.
//
// The HMAC input is the SHA-256 digest rather than the plaintext so that a
// legacy row, which stores that digest, converts without the plaintext.
//
// Of runs on every request, so the HMAC pads are prepared once and each call
// hashes on the stack (RFC 2104 with a key shorter than the block size)
// instead of allocating a crypto/hmac state.
type KeyLookup struct {
	key  []byte
	ipad [sha256.BlockSize]byte
	opad [sha256.BlockSize]byte
	kid  string
}

// NewKeyLookup derives the lookup key from the credential master key.
func NewKeyLookup(masterKey []byte) *KeyLookup {
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("keirouter/api-key-lookup/v1"))
	key := mac.Sum(nil)

	l := &KeyLookup{key: key}
	for i := range sha256.BlockSize {
		var b byte
		if i < len(key) {
			b = key[i]
		}
		l.ipad[i] = b ^ 0x36
		l.opad[i] = b ^ 0x5c
	}

	kidMAC := hmac.New(sha256.New, key)
	kidMAC.Write([]byte("keirouter/api-key-lookup/kid"))
	l.kid = hex.EncodeToString(kidMAC.Sum(nil)[:8])
	return l
}

// Of returns the keyed lookup index for a presented plaintext key.
func (l *KeyLookup) Of(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return l.of(sum[:])
}

// FromLegacy converts a stored legacy lookup hash (LookupHash) into the keyed
// index, so existing rows gain it without anyone presenting the plaintext.
func (l *KeyLookup) FromLegacy(legacy string) (string, error) {
	digest, err := hex.DecodeString(legacy)
	if err != nil || len(digest) != sha256.Size {
		return "", ErrInvalidLookupHash
	}
	return l.of(digest), nil
}

// KID identifies the lookup key without revealing it. Each row records the
// KID it was indexed under, so a changed master key is detectable.
func (l *KeyLookup) KID() string { return l.kid }

// of is HMAC-SHA256(l.key, digest) for a 32-byte digest.
func (l *KeyLookup) of(digest []byte) string {
	var buf [sha256.BlockSize + sha256.Size]byte
	copy(buf[:], l.ipad[:])
	copy(buf[sha256.BlockSize:], digest)
	inner := sha256.Sum256(buf[:])
	copy(buf[:], l.opad[:])
	copy(buf[sha256.BlockSize:], inner[:])
	outer := sha256.Sum256(buf[:])
	var out [2 * sha256.Size]byte
	hex.Encode(out[:], outer[:])
	return string(out[:])
}

// maskKey renders a key for display, revealing only a short head and tail.
func maskKey(plaintext string) string {
	body := strings.TrimPrefix(plaintext, KeyPrefix)
	if len(body) <= 8 {
		return KeyPrefix + "…"
	}
	return fmt.Sprintf("%s%s…%s", KeyPrefix, body[:4], body[len(body)-4:])
}

func parseArgon2Hash(encoded string) (mem, time uint32, threads uint8, salt, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: salt: %v", ErrInvalidHash, err)
	}
	if hash, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: digest: %v", ErrInvalidHash, err)
	}
	return mem, time, threads, salt, hash, nil
}