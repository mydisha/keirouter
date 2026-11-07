package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// insertLegacyAPIKey stores a key as a release without the keyed lookup index
// did, which is what a database carried over from such a release holds.
func insertLegacyAPIKey(t *testing.T, s *Server, id, plaintext string) {
	t.Helper()
	require.NoError(t, s.identity.Keys().Create(context.Background(), store.APIKey{
		ID: id, TenantID: adminTenant, Name: id, KeyHash: "$argon2id$legacy",
		LookupHash: crypto.LookupHash(plaintext), Display: "kr_…", CreatedAt: time.Now(),
	}))
}

func TestCLIKeyStatus(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	ctx := context.Background()
	s := h.gateway

	status, name := s.cliKeyStatus(ctx, h.apiKey)
	require.Equal(t, "valid", status)
	require.Equal(t, "test-key", name)

	status, _ = s.cliKeyStatus(ctx, "")
	require.Equal(t, "missing", status)
	status, _ = s.cliKeyStatus(ctx, "kr_unknown")
	require.Equal(t, "invalid", status)

	insertLegacyAPIKey(t, s, "legacy", "kr_legacy-key")
	status, name = s.cliKeyStatus(ctx, "kr_legacy-key")
	require.Equal(t, "valid", status, "a key not indexed yet is still recognized")
	require.Equal(t, "legacy", name)

	issued, err := s.identity.Create(ctx, adminTenant, "", "off")
	require.NoError(t, err)
	require.NoError(t, s.identity.SetDisabled(ctx, issued.Record.ID, true))
	status, name = s.cliKeyStatus(ctx, issued.Plaintext)
	require.Equal(t, "disabled", status)
	require.Equal(t, "off", name)
}

// Keys imported from another router keep their plaintext (often short), and
// must authenticate through the keyed index straight after import.
func TestImportN9routerAPIKeysAuthenticate(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	ctx := context.Background()
	s := h.gateway

	raw, err := json.Marshal([]map[string]any{
		{"id": "one", "key": "sk-imported-123", "name": "laptop"},
		{"id": "two", "key": "sk-imported-456", "isActive": false},
	})
	require.NoError(t, err)
	var res foreignImportResult
	s.importN9routerAPIKeys(ctx, map[string]json.RawMessage{"apiKeys": raw}, &res)
	require.Empty(t, res.Errors)
	require.Equal(t, 2, res.APIKeys)

	rec, err := s.identity.Authenticate(ctx, "sk-imported-123")
	require.NoError(t, err)
	require.Equal(t, n9IDPrefix+"one", rec.ID)
	require.Equal(t, s.identity.KeyLookup().Of("sk-imported-123"), rec.LookupHMAC)

	_, err = s.identity.Authenticate(ctx, "sk-imported-456")
	require.Error(t, err, "an inactive imported key stays disabled")

	resp := h.post(t, "/v1/chat/completions", `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`, "sk-imported-123")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestUsageKeyLookupMatchesIndexedAndLegacyRows(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	ctx := context.Background()
	s := h.gateway

	insertLegacyAPIKey(t, s, "legacy", "kr_legacy-key")
	resolve := s.usageKeyLookup(ctx)

	id, ok := resolve(h.apiKey)
	require.True(t, ok)
	require.NotEmpty(t, id)

	id, ok = resolve("kr_legacy-key")
	require.True(t, ok)
	require.Equal(t, "legacy", id)

	_, ok = resolve("kr_unknown")
	require.False(t, ok)
	_, ok = resolve("")
	require.False(t, ok)
}
