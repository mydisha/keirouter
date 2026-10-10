package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"
)

func TestEstimateTokensFromChars(t *testing.T) {
	cases := []struct{ chars, want int }{{0, 0}, {-5, 0}, {1, 1}, {4, 1}, {5, 2}, {100, 25}}
	for _, tc := range cases {
		if got := EstimateTokensFromChars(tc.chars); got != tc.want {
			t.Errorf("EstimateTokensFromChars(%d) = %d, want %d", tc.chars, got, tc.want)
		}
	}
}

// Reference counts come from the o200k_base vocabulary (the GPT-4o/GPT-5
// tokenizer). Individual short samples can deviate noticeably; the estimator
// is calibrated so a realistic corpus lands within ±15% and errs high.
func TestEstimateTextTokensTracksBPE(t *testing.T) {
	samples := []struct {
		text string
		ref  int
	}{
		{"The quick brown fox jumps over the lazy dog.", 10},
		{"International configuration management is complicated.", 6},
		{"func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {\n\tkey, _ := authedKey(r.Context())\n}", 30},
		{`{"file_path":"/home/user/project/main.go","limit":200,"offset":1500}`, 20},
		{"这是一个用于测试的中文句子。", 9},
		{"Ini adalah kalimat bahasa Indonesia untuk pengujian.", 11},
		{"        indented line\n\n\n", 5},
	}
	var sumGot, sumRef int
	for _, s := range samples {
		got := EstimateTextTokens(s.text)
		sumGot += got
		sumRef += s.ref
		lo, hi := s.ref*6/10, s.ref*17/10+1
		if got < lo || got > hi {
			t.Errorf("EstimateTextTokens(%q) = %d, reference %d (want within [%d,%d])", s.text, got, s.ref, lo, hi)
		}
	}
	if sumGot < sumRef*85/100 || sumGot > sumRef*120/100 {
		t.Errorf("aggregate estimate %d vs reference %d drifts more than expected", sumGot, sumRef)
	}
	if EstimateTextTokens("") != 0 {
		t.Error("empty text must be free")
	}
}

func pngBase64(w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestEstimateImageTokensUsesDimensions(t *testing.T) {
	// 1024x1024 → shortest side scaled to 768 → 2x2 tiles → 85 + 4*170.
	got := EstimateImageTokens(&MediaPayload{MIMEType: "image/png", Data: pngBase64(1024, 1024)})
	if got != 765 {
		t.Fatalf("1024x1024 = %d tokens, want 765", got)
	}
	// 200x200 fits one tile.
	if got := EstimateImageTokens(&MediaPayload{MIMEType: "image/png", Data: pngBase64(200, 200)}); got != 255 {
		t.Fatalf("200x200 = %d tokens, want 255", got)
	}
	// Remote URL: default.
	if got := EstimateImageTokens(&MediaPayload{URL: "https://example.com/a.png"}); got != defaultImageTokens {
		t.Fatalf("url image = %d, want default %d", got, defaultImageTokens)
	}
	// A data: URL prefix is tolerated.
	if got := EstimateImageTokens(&MediaPayload{Data: "data:image/png;base64," + pngBase64(200, 200)}); got != 255 {
		t.Fatalf("data-url image = %d, want 255", got)
	}
}

func TestEstimatePromptTokensCountsEverything(t *testing.T) {
	if got := EstimatePromptTokens(nil); got != 0 {
		t.Errorf("nil request = %d, want 0", got)
	}
	textOnly := &ChatRequest{
		System:   "You are helpful.",
		Messages: []Message{{Role: RoleUser, Content: []ContentPart{{Type: PartText, Text: "hello there"}}}},
	}
	base := EstimatePromptTokens(textOnly)
	if base < 10 || base > 20 {
		t.Fatalf("text-only estimate = %d, expected framing + ~6 tokens", base)
	}

	withImage := *textOnly
	withImage.Messages = append(withImage.Messages, Message{Role: RoleUser, Content: []ContentPart{
		{Type: PartImage, Media: &MediaPayload{MIMEType: "image/png", Data: pngBase64(1024, 1024)}},
	}})
	if got := EstimatePromptTokens(&withImage); got != base+perMessageOverhead+765 {
		t.Fatalf("image estimate = %d, want %d", got, base+perMessageOverhead+765)
	}

	withTools := *textOnly
	withTools.Tools = []Tool{{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}}
	if got := EstimatePromptTokens(&withTools); got <= base+perToolOverhead {
		t.Fatalf("tool definitions must add tokens: %d vs %d", got, base)
	}
}
