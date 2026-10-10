package core

import (
	"bytes"
	"encoding/base64"
	"image"
	"strings"
	"unicode"

	// Register the decoders DecodeConfig needs to read image headers.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Token estimation.
//
// Providers normally report usage; these estimates are used when they do not
// (some OpenAI-compatible servers, truncated streams) and for pre-dispatch
// budgeting (TPM limits). The old "4 characters per token" rule undercounts
// code and JSON by ~25% and ignores images entirely, so the estimator below
// mimics a BPE pre-tokenizer instead: it splits text into words, numbers,
// symbols and whitespace runs the way tiktoken does and charges each run the
// way the OpenAI/Anthropic vocabularies tend to. It is still an estimate
// (expect roughly ±15%), never a replacement for provider counts.

const (
	// perMessageOverhead is the framing cost of one chat message (role, separators).
	perMessageOverhead = 4
	// replyPrimingOverhead is the assistant turn priming every request pays.
	replyPrimingOverhead = 3
	// perToolOverhead is the framing cost of one tool definition.
	perToolOverhead = 8

	// imageBaseTokens / imageTileTokens are OpenAI's high-detail image cost
	// (85 + 170 per 512px tile after resizing). Anthropic's (w*h)/750 and
	// Gemini's 258-per-768px-tile land in the same range for typical sizes.
	imageBaseTokens = 85
	imageTileTokens = 170
	imageTileSize   = 512
	imageMaxEdge    = 2048
	imageShortEdge  = 768
	// defaultImageTokens is charged when the dimensions cannot be read
	// (remote URL, unsupported format): a 1024x1024 high-detail image.
	defaultImageTokens = 765
	// documentBytesPerToken approximates PDF/document attachments by size.
	documentBytesPerToken = 8
)

// EstimateTokensFromChars approximates a token count from a character count.
// Kept for callers that only know a length; EstimateTextTokens is more
// accurate when the text is available. Returns 0 for non-positive input.
func EstimateTokensFromChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

// EstimateTextTokens approximates the token count of text with a BPE-like
// pre-tokenization: words, numbers (3-digit groups), symbols, CJK characters
// and whitespace runs are charged separately.
func EstimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	tokens := 0
	// run bookkeeping
	kind := runNone
	runLen := 0
	flush := func() {
		if runLen == 0 {
			return
		}
		switch kind {
		case runWord:
			// Common words are one token; long or rare words split roughly
			// every 8 characters (calibrated against o200k_base).
			tokens += 1 + (runLen-1)/8
		case runDigits:
			// Numbers tokenize in chunks of up to three digits.
			tokens += (runLen + 2) / 3
		case runCJK:
			// o200k spends about 0.7 tokens per CJK character.
			tokens += (runLen*7 + 9) / 10
		case runOtherLetters:
			// Cyrillic, Arabic, Thai, ...: about 2–3 characters per token.
			tokens += (runLen*2 + 4) / 5
		case runSymbols:
			// Adjacent symbols merge aggressively ('":"', '","', "://", "()").
			tokens += (runLen + 2) / 3
		case runSpaces:
			// A single space attaches to the following word for free;
			// indentation runs cost about one token per eight columns.
			if runLen > 1 {
				tokens += 1 + (runLen-2)/8
			}
		case runNewlines:
			// "\n" and "\n\n" are single tokens.
			tokens += (runLen + 1) / 2
		}
		runLen = 0
	}
	for _, r := range s {
		k := classifyRune(r)
		if k != kind {
			flush()
			kind = k
		}
		runLen++
	}
	flush()
	return tokens
}

type runKind uint8

const (
	runNone runKind = iota
	runWord
	runDigits
	runCJK
	runOtherLetters
	runSymbols
	runSpaces
	runNewlines
)

func classifyRune(r rune) runKind {
	switch {
	case r == '\n' || r == '\r':
		return runNewlines
	case r == ' ' || r == '\t':
		return runSpaces
	case r >= '0' && r <= '9':
		return runDigits
	case r < 0x80:
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return runWord
		}
		return runSymbols
	case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
		return runCJK
	case unicode.IsLetter(r):
		return runOtherLetters
	case unicode.IsDigit(r):
		return runDigits
	case unicode.IsSpace(r):
		return runSpaces
	default:
		return runSymbols
	}
}

// EstimateImageTokens approximates the prompt cost of an image attachment
// using OpenAI's high-detail tile formula on the decoded dimensions. Remote
// URLs and undecodable payloads get the default for a 1024x1024 image.
func EstimateImageTokens(m *MediaPayload) int {
	if m == nil {
		return 0
	}
	w, h, ok := imageDimensions(m)
	if !ok {
		return defaultImageTokens
	}
	// Fit within 2048x2048, then scale the shortest side to 768.
	if w > imageMaxEdge || h > imageMaxEdge {
		scale := float64(imageMaxEdge) / float64(max(w, h))
		w, h = int(float64(w)*scale), int(float64(h)*scale)
	}
	if short := min(w, h); short > imageShortEdge {
		scale := float64(imageShortEdge) / float64(short)
		w, h = int(float64(w)*scale), int(float64(h)*scale)
	}
	tiles := ((w + imageTileSize - 1) / imageTileSize) * ((h + imageTileSize - 1) / imageTileSize)
	if tiles < 1 {
		tiles = 1
	}
	return imageBaseTokens + imageTileTokens*tiles
}

// imageDimensions reads the width/height from a base64 payload's header
// without decoding the pixels.
func imageDimensions(m *MediaPayload) (int, int, bool) {
	if m.Data == "" {
		return 0, 0, false
	}
	data := m.Data
	if i := strings.Index(data, ";base64,"); i >= 0 {
		data = data[i+len(";base64,"):]
	}
	// Headers live in the first few hundred bytes; decode a bounded prefix.
	const prefix = 4096
	if len(data) > prefix {
		data = data[:prefix-prefix%4]
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(data, "="))
		if err != nil {
			return 0, 0, false
		}
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// EstimateDocumentTokens approximates a document attachment (PDF) by size.
func EstimateDocumentTokens(m *MediaPayload) int {
	if m == nil || m.Data == "" {
		return 0
	}
	decoded := len(m.Data) * 3 / 4
	return decoded / documentBytesPerToken
}

// EstimatePromptTokens approximates the prompt token count for a request over
// system text, message content (text, tool calls, tool results, images and
// documents) and tool definitions, including per-message framing overhead.
func EstimatePromptTokens(req *ChatRequest) int {
	if req == nil || (req.System == "" && len(req.Messages) == 0 && len(req.Tools) == 0) {
		return 0
	}
	tokens := replyPrimingOverhead
	if req.System != "" {
		tokens += perMessageOverhead + EstimateTextTokens(req.System)
	}
	for _, m := range req.Messages {
		tokens += perMessageOverhead
		for _, part := range m.Content {
			tokens += estimatePartTokens(part)
		}
	}
	for _, t := range req.Tools {
		tokens += perToolOverhead + EstimateTextTokens(t.Name) + EstimateTextTokens(t.Description) +
			EstimateTextTokens(string(t.Parameters))
	}
	return tokens
}

func estimatePartTokens(part ContentPart) int {
	tokens := 0
	switch part.Type {
	case PartImage:
		return EstimateImageTokens(part.Media)
	case PartDocument:
		return EstimateDocumentTokens(part.Media)
	}
	tokens += EstimateTextTokens(part.Text)
	if part.ToolCall != nil {
		tokens += EstimateTextTokens(part.ToolCall.Name) + EstimateTextTokens(string(part.ToolCall.Arguments))
	}
	if part.ToolResult != nil {
		tokens += EstimateTextTokens(part.ToolResult.Content)
		for _, p := range part.ToolResult.Parts {
			tokens += estimatePartTokens(p)
		}
	}
	return tokens
}
