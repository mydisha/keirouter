package gateway

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// handleGeminiGenerate serves Gemini's native endpoints:
//
//	POST /v1beta/models/{model}:generateContent
//	POST /v1beta/models/{model}:streamGenerateContent
//
// The model and action live in the URL path, and the model may itself contain
// a slash (provider/model). Everything after parsing runs through serveChat, so
// key model restrictions and rate limits apply exactly as on the other edges.
func (s *Server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request) {
	model, action, ok := strings.Cut(chi.URLParam(r, "*"), ":")
	if !ok || model == "" {
		writeDialectError(w, core.DialectGemini, http.StatusBadRequest, "expected /v1beta/models/{model}:generateContent")
		return
	}
	switch action {
	case "generateContent", "streamGenerateContent":
	case "countTokens":
		s.handleGeminiCountTokens(w, r)
		return
	default:
		writeDialectError(w, core.DialectGemini, http.StatusNotFound, "unsupported action: "+action)
		return
	}
	stream := action == "streamGenerateContent"
	s.serveChat(w, r, core.DialectGemini, func(req *core.ChatRequest) {
		req.Model = model
		req.Stream = stream
	})
}

// handleGeminiCountTokens answers countTokens locally, like Anthropic's
// count_tokens: running a generation to count tokens would cost money.
func (s *Server) handleGeminiCountTokens(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readRequestBody(w, r)
	if !ok {
		return
	}
	// The body is {contents} or {generateContentRequest: {...}}.
	var wrapped struct {
		Request json.RawMessage `json:"generateContentRequest"`
	}
	if json.Unmarshal(body, &wrapped) == nil && len(wrapped.Request) > 0 {
		body = wrapped.Request
	}
	req, err := transform.GeminiCodec{}.ParseRequest(body)
	if err != nil {
		writeDialectError(w, core.DialectGemini, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"totalTokens": estimateInputTokens(req)})
}
