package connectors

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestCloudflareBaseURLResolvesAccountID(t *testing.T) {
	c := NewOpenAICompatible("cloudflare-ai", "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1")
	want := "https://api.cloudflare.com/client/v4/accounts/abc123/ai/v1"

	require.Equal(t, want, c.baseURL(core.Credentials{Extra: map[string]string{"accountId": "abc123"}}))
	// Accounts imported from 9router stored the id under account_id.
	require.Equal(t, want, c.baseURL(core.Credentials{Extra: map[string]string{"account_id": "abc123"}}))
}
