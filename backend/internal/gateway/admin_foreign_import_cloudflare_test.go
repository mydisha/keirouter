package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPSDToMetadataKeepsCloudflareAccountIDCamelCase(t *testing.T) {
	meta := psdToMetadata("cloudflare-ai", map[string]any{"accountId": "abc123"})
	// The connector's base URL template is .../accounts/{accountId}/ai/v1.
	require.Equal(t, map[string]string{"accountId": "abc123"}, meta)
}
