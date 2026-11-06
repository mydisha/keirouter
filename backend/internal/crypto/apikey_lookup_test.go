package crypto

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testMasterKey(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

func TestKeyLookup_LegacyConversionMatchesPlaintext(t *testing.T) {
	l := NewKeyLookup(testMasterKey(1))
	gen, err := GenerateAPIKey()
	require.NoError(t, err)

	for _, plaintext := range []string{
		gen.Plaintext,
		"sk-short",
		"kunci-rahasia-ünïcødé-🔑",
		"",
		strings.Repeat("x", 4096),
	} {
		fromLegacy, err := l.FromLegacy(LookupHash(plaintext))
		require.NoError(t, err)
		require.Equal(t, l.Of(plaintext), fromLegacy,
			"a row converted from its stored sha-256 must match the index computed from the plaintext (%q)", plaintext)
		require.Len(t, l.Of(plaintext), 64)
	}
}

func TestKeyLookup_DependsOnMasterKey(t *testing.T) {
	a := NewKeyLookup(testMasterKey(1))
	a2 := NewKeyLookup(testMasterKey(1))
	b := NewKeyLookup(testMasterKey(2))
	const plaintext = "kr_example"

	require.Equal(t, a.Of(plaintext), a2.Of(plaintext), "same master key is deterministic")
	require.Equal(t, a.KID(), a2.KID())
	require.NotEqual(t, a.Of(plaintext), b.Of(plaintext), "another master key yields another index")
	require.NotEqual(t, a.KID(), b.KID())
	require.Len(t, a.KID(), 16)

	require.NotEqual(t, LookupHash(plaintext), a.Of(plaintext), "keyed index must differ from the legacy sha-256")
	require.NotEqual(t, a.Of(plaintext), a.Of(plaintext+"x"))
}

// The stack-based HMAC must equal crypto/hmac exactly, or stored indexes
// would stop matching if the implementation ever changed.
func TestKeyLookup_MatchesStandardHMAC(t *testing.T) {
	for _, mk := range [][]byte{testMasterKey(1), testMasterKey(0xff), make([]byte, KeySize)} {
		l := NewKeyLookup(mk)
		for _, plaintext := range []string{"", "kr_x", "kunci-ünïcødé", strings.Repeat("y", 1000)} {
			sum := sha256.Sum256([]byte(plaintext))
			mac := hmac.New(sha256.New, l.key)
			mac.Write(sum[:])
			require.Equal(t, hex.EncodeToString(mac.Sum(nil)), l.Of(plaintext))
		}
	}
}

func TestKeyLookup_OfDoesNotAllocateBeyondTheResult(t *testing.T) {
	l := NewKeyLookup(testMasterKey(1))
	allocs := testing.AllocsPerRun(100, func() { _ = l.Of("kr_some-plaintext-key") })
	require.LessOrEqual(t, allocs, 1.0, "only the hex string should allocate")
}

func TestKeyLookup_FromLegacyRejectsMalformed(t *testing.T) {
	l := NewKeyLookup(testMasterKey(1))
	for _, bad := range []string{
		"",
		"zz",
		strings.Repeat("a", 63),
		strings.Repeat("a", 66),
		strings.Repeat("g", 64),
	} {
		_, err := l.FromLegacy(bad)
		require.ErrorIs(t, err, ErrInvalidLookupHash, "input %q", bad)
	}
}
