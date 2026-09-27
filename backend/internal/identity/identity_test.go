package identity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/crypto"
)

func TestGenerateProducesVerifiableKey(t *testing.T) {
	// Generate does not touch the store, so a nil repo is fine here.
	s := New(nil)

	issued, err := s.Generate("tenant-1", "project-1", "my key")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if issued.Plaintext == "" {
		t.Fatal("Plaintext is empty")
	}
	if !strings.HasPrefix(issued.Plaintext, crypto.DefaultKeyPrefix) {
		t.Fatalf("Plaintext %q missing prefix %q", issued.Plaintext, crypto.DefaultKeyPrefix)
	}

	rec := issued.Record
	if rec.ID == "" {
		t.Fatal("Record.ID is empty")
	}
	if rec.TenantID != "tenant-1" || rec.ProjectID != "project-1" || rec.Name != "my key" {
		t.Fatalf("record fields wrong: %+v", rec)
	}
	if rec.KeyHash == "" || rec.KeyHash == issued.Plaintext {
		t.Fatalf("KeyHash not hashed: %q", rec.KeyHash)
	}

	// The stored lookup index must match the deterministic hash of the plaintext.
	if rec.LookupHash != crypto.LookupHash(issued.Plaintext) {
		t.Fatal("LookupHash does not match plaintext lookup")
	}

	// The stored argon2 verifier must accept the plaintext.
	ok, err := crypto.VerifyAPIKey(issued.Plaintext, rec.KeyHash)
	if err != nil {
		t.Fatalf("VerifyAPIKey: %v", err)
	}
	if !ok {
		t.Fatal("generated plaintext does not verify against stored hash")
	}
}

func TestGenerateUniqueKeys(t *testing.T) {
	s := New(nil)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		issued, err := s.Generate("t", "p", "k")
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if seen[issued.Plaintext] {
			t.Fatal("Generate produced a duplicate plaintext key")
		}
		if seen[issued.Record.ID] {
			t.Fatal("Generate produced a duplicate record ID")
		}
		seen[issued.Plaintext] = true
		seen[issued.Record.ID] = true
	}
}

func TestNormalizePrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", crypto.DefaultKeyPrefix, true},
		{"_", crypto.DefaultKeyPrefix, true},
		{"tkr", "tkr_", true},
		{"tkr_", "tkr_", true},
		{"TKR_", "tkr_", true},
		{"  tkr  ", "tkr_", true},
		{"a", "a_", true},
		{"abc123", "abc123_", true},
		{"abcdefghijklmnop", "abcdefghijklmnop_", true}, // 16 chars: allowed
		{"tkr!", "", false},
		{"tk r", "", false},
		{"abcdefghijklmnopq", "", false}, // 17 chars: rejected
	}
	for _, c := range cases {
		got, ok := NormalizePrefix(c.in)
		require.Equal(t, c.ok, ok, "input %q", c.in)
		if c.ok {
			require.Equal(t, c.want, got, "input %q", c.in)
		}
	}
}

func TestSetKeyPrefixAppliesToNewKeys(t *testing.T) {
	s := New(nil)
	p, ok := NormalizePrefix("tkr")
	require.True(t, ok)
	s.SetKeyPrefix(p)

	issued, err := s.Generate("t", "p", "k")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(issued.Plaintext, "tkr_"))
	require.True(t, strings.HasPrefix(issued.Record.Display, "tkr_"))
}

func TestChangingPrefixKeepsOldKeysValid(t *testing.T) {
	s := New(nil)

	old, err := s.Generate("t", "p", "k")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(old.Plaintext, crypto.DefaultKeyPrefix))

	p, ok := NormalizePrefix("tkr")
	require.True(t, ok)
	s.SetKeyPrefix(p)

	// Requirement: a key issued before the prefix change must still authenticate.
	ok, err = crypto.VerifyAPIKey(old.Plaintext, old.Record.KeyHash)
	require.NoError(t, err)
	require.True(t, ok, "existing key must keep authenticating after prefix change")
	require.Equal(t, crypto.LookupHash(old.Plaintext), old.Record.LookupHash)

	// Newly issued keys use the new prefix.
	fresh, err := s.Generate("t", "p", "k")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(fresh.Plaintext, "tkr_"))
}
