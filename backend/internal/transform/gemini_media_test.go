package transform

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestGemini_PDFIsADocumentNotAnImage(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"summarize"},{"inlineData":{"mimeType":"application/pdf","data":"JVBERi0="}}]}]}`
	req, err := GeminiCodec{}.ParseRequest([]byte(body))
	require.NoError(t, err)
	require.Equal(t, core.PartDocument, req.Messages[0].Content[1].Type)

	req.Model = "claude-sonnet-4-5"
	out, err := AnthropicCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(out), `"type":"document"`)
	require.NotContains(t, string(out), `"type":"image"`)

	back, err := GeminiCodec{}.RenderRequest(req)
	require.NoError(t, err)
	require.Contains(t, string(back), `"mimeType":"application/pdf"`)
}
