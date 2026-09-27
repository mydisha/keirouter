package identity

import (
	"strings"
	"testing"

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
