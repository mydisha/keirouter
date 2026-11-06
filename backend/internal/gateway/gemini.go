package gateway

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/core"
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
		writeError(w, http.StatusBadRequest, "expected /v1beta/models/{model}:generateContent")
		return
	}
	stream := strings.HasPrefix(action, "stream")
	s.serveChat(w, r, core.DialectGemini, func(req *core.ChatRequest) {
		req.Model = model
		req.Stream = stream
	})
}
