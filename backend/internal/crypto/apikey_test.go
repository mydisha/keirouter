package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateAPIKey_ShapeAndVerify(t *testing.T) {
	k, err := GenerateAPIKey("")
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(k.Plaintext, DefaultKeyPrefix), "plaintext must carry prefix")
	require.NotEmpty(t, k.Hash)
	require.Len(t, k.Lookup, 64, "sha-256 hex lookup is 64 chars")
	require.Contains(t, k.Display, "…", "display form is masked")
	require.NotContains(t, k.Display, k.Plaintext[len(DefaultKeyPrefix)+4:len(k.Plaintext)-4], "middle must be hidden")

	ok, err := VerifyAPIKey(k.Plaintext, k.Hash)
	require.NoError(t, err)
	require.True(t, ok, "correct key must verify")
}

func TestVerifyAPIKey_WrongKeyFails(t *testing.T) {
	k, err := GenerateAPIKey("")
	require.NoError(t, err)
	ok, err := VerifyAPIKey(k.Plaintext+"x", k.Hash)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		k, err := GenerateAPIKey("")
		require.NoError(t, err)
		_, dup := seen[k.Plaintext]
		require.False(t, dup, "keys must be unique")
		seen[k.Plaintext] = struct{}{}
	}
}

func TestGenerateAPIKey_CustomPrefix(t *testing.T) {
	k, err := GenerateAPIKey("tkr_")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(k.Plaintext, "tkr_"), "plaintext must carry custom prefix")
	require.True(t, strings.HasPrefix(k.Display, "tkr_"), "display must carry custom prefix")
	require.False(t, strings.HasPrefix(k.Display, DefaultKeyPrefix), "display must not use the default prefix")

	ok, err := VerifyAPIKey(k.Plaintext, k.Hash)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestGenerateAPIKey_EmptyPrefixUsesDefault(t *testing.T) {
	k, err := GenerateAPIKey("")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(k.Plaintext, DefaultKeyPrefix))
}

func TestLookupHash_Deterministic(t *testing.T) {
	require.Equal(t, LookupHash("kr_sample"), LookupHash("kr_sample"))
	require.NotEqual(t, LookupHash("kr_a"), LookupHash("kr_b"))
}

func TestVerifyAPIKey_MalformedHash(t *testing.T) {
	cases := []string{
		"",
		"not-a-hash",
		"$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", // wrong variant
		"$argon2id$v=18$m=1,t=1,p=1$c2FsdA$aGFzaA", // wrong version
		"$argon2id$v=19$bad$c2FsdA$aGFzaA",
	}
	for _, h := range cases {
		_, err := VerifyAPIKey("kr_whatever", h)
		require.ErrorIs(t, err, ErrInvalidHash, "hash %q must be rejected", h)
	}
}