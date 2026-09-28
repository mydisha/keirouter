package gateway

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
)

// This file implements the gateway edge for the non-chat capabilities. Each
// handler parses an OpenAI-style request, resolves the routing target from the
// model string, runs the matching pipeline method, and renders the response.

// mediaOptions builds pipeline.MediaOptions from a resolved request: it maps
// the model string to fallback targets and attaches tenant/scope metadata.
func (s *Server) mediaOptions(r *http.Request, model string) (pipeline.MediaOptions, error) {
	key, _ := authedKey(r.Context())
	tenantID := tenantOf(key)
	resolved, err := resolveTargets(r.Context(), s.chains, s.aliases, s.latencyReader(), tenantID, model)
	if err != nil {
		return pipeline.MediaOptions{}, err
	}
	if len(resolved.Targets) > 0 {
		filtered, ferr := s.filterAllowedTargets(r.Context(), key.ID, model, resolved.PlanOpts.ChainID != "", resolved.Targets)
		if ferr != nil {
			return pipeline.MediaOptions{}, ferr
		}
		if len(filtered) == 0 {
			return pipeline.MediaOptions{}, accessDeniedError{model: model}
		}
		resolved.Targets = filtered
	}
	effectiveLimits, err := s.effectiveLimits(r.Context(), key)
	if err != nil {
		return pipeline.MediaOptions{}, err
	}
	return pipeline.MediaOptions{
		Targets:   resolved.Targets,
		TenantID:  tenantID,
		ProjectID: key.ProjectID,
		APIKeyID:  key.ID,
		Limits:    effectiveLimits,
	}, nil
}

// readJSON reads and decodes a JSON request body into v.
func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, ok := s.readRequestBody(w, r)
	if !ok {
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

// writeMediaError maps a pipeline/provider error (or bad-model error) to HTTP.
func (s *Server) writeMediaError(w http.ResponseWriter, err error) {
	if denied, ok := err.(accessDeniedError); ok {
		writeError(w, http.StatusForbidden, denied.Error())
		return
	}
	var bad badModelError
	if asBadModel(err, &bad) {
		writeError(w, http.StatusBadRequest, bad.Error())
		return
	}
	s.writeProviderError(w, err)
}

// ---- Embeddings -------------------------------------------------------------

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	var body struct {
		Model      string          `json:"model"`
		Input      json.RawMessage `json:"input"`
		Dimensions int             `json:"dimensions"`
	}
	if !s.readJSON(w, r, &body) {
		return
	}
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	inputs, err := decodeEmbeddingInput(body.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	opts, err := s.mediaOptions(r, body.Model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	model := modelTail(opts.Targets)
	resp, provider, perr := s.pipeline.Embeddings(r.Context(), &core.EmbeddingRequest{
		Model: model, Input: inputs, Dimensions: body.Dimensions,
	}, opts)
	if perr != nil {
		s.logRequest(key.Name, provider, model, 0, 0, 0, false, perr)
		s.writeMediaError(w, perr)
		return
	}
	s.logRequest(key.Name, provider, model, resp.Usage.TotalTokens, 0, 0, false, nil)

	data := make([]map[string]any, 0, len(resp.Vectors))
	for i, v := range resp.Vectors {
		data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": v})
	}
	w.Header().Set("X-KeiRouter-Provider", provider)
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "model": resp.Model, "data": data,
		"usage": map[string]any{
			"prompt_tokens": resp.Usage.PromptTokens, "total_tokens": resp.Usage.TotalTokens,
		},
	})
}

// decodeEmbeddingInput accepts a string or an array of strings.
func decodeEmbeddingInput(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errBadModel("input is required")
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, errBadModel("input must be a string or array of strings")
}

// ---- Image generation -------------------------------------------------------

func (s *Server) handleImageGeneration(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	var req core.ImageRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	if req.Model == "" || req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "model and prompt are required")
		return
	}
	opts, err := s.mediaOptions(r, req.Model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	req.Model = modelTail(opts.Targets)
	resp, provider, perr := s.pipeline.GenerateImage(r.Context(), &req, opts)
	if perr != nil {
		s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, perr)
		s.writeMediaError(w, perr)
		return
	}
	s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, nil)
	w.Header().Set("X-KeiRouter-Provider", provider)
	writeJSON(w, http.StatusOK, map[string]any{"created": resp.Created, "data": resp.Data})
}

// ---- Speech-to-text ---------------------------------------------------------

func (s *Server) handleAudioTranscription(w http.ResponseWriter, r *http.Request) {
	// Accept multipart/form-data (OpenAI standard) with a file + model field.
	if err := r.ParseMultipartForm(s.requestBodyLimit()); err != nil {
		writeError(w, http.StatusBadRequest, "expected multipart/form-data: "+err.Error())
		return
	}
	model := r.FormValue("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(io.LimitReader(file, s.requestBodyLimit()))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read audio file")
		return
	}

	opts, err := s.mediaOptions(r, model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	req := &core.TranscriptionRequest{
		Model:          modelTail(opts.Targets),
		Audio:          audio,
		Filename:       header.Filename,
		ContentType:    header.Header.Get("Content-Type"),
		Language:       r.FormValue("language"),
		Prompt:         r.FormValue("prompt"),
		ResponseFormat: r.FormValue("response_format"),
	}
	resp, provider, perr := s.pipeline.Transcribe(r.Context(), req, opts)
	if perr != nil {
		s.writeMediaError(w, perr)
		return
	}
	w.Header().Set("X-KeiRouter-Provider", provider)
	if req.ResponseFormat == "text" {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, resp.Text)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": resp.Text, "language": resp.Language, "duration": resp.Duration})
}

// ---- Text-to-speech ---------------------------------------------------------

func (s *Server) handleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	var req core.SpeechRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	if req.Model == "" || req.Input == "" {
		writeError(w, http.StatusBadRequest, "model and input are required")
		return
	}
	opts, err := s.mediaOptions(r, req.Model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	req.Model = modelTail(opts.Targets)
	resp, provider, perr := s.pipeline.Synthesize(r.Context(), &req, opts)
	if perr != nil {
		s.writeMediaError(w, perr)
		return
	}
	w.Header().Set("Content-Type", resp.ContentType)
	w.Header().Set("X-KeiRouter-Provider", provider)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp.Audio)
}

// ---- Web search -------------------------------------------------------------

func (s *Server) handleWebSearch(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	var req core.SearchRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	// Default the search provider/model when the client omits it: pick the
	// first configured search-capable target via the model string.
	model := req.Model
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required (provider/model, e.g. tavily/tavily-search)")
		return
	}
	opts, err := s.mediaOptions(r, model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	req.Model = modelTail(opts.Targets)
	resp, provider, perr := s.pipeline.Search(r.Context(), &req, opts)
	if perr != nil {
		s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, perr)
		s.writeMediaError(w, perr)
		return
	}
	s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, nil)
	w.Header().Set("X-KeiRouter-Provider", provider)
	writeJSON(w, http.StatusOK, map[string]any{"query": resp.Query, "results": resp.Results})
}

// ---- Web fetch --------------------------------------------------------------

func (s *Server) handleWebFetch(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	var req core.FetchRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	// SSRF Protection: Validate URL before passing to upstream services
	if err := httputil.ValidateOutboundURL(req.URL); err != nil {
		s.log.Warn("blocked suspicious web fetch URL", "url", req.URL, "error", err)
		writeError(w, http.StatusBadRequest, "invalid url: URL blocked by security policy")
		return
	}

	model := req.Model
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required (provider/model, e.g. firecrawl/firecrawl-scrape)")
		return
	}
	opts, err := s.mediaOptions(r, model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	req.Model = modelTail(opts.Targets)
	resp, provider, perr := s.pipeline.Fetch(r.Context(), &req, opts)
	if perr != nil {
		s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, perr)
		s.writeMediaError(w, perr)
		return
	}
	s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, nil)
	w.Header().Set("X-KeiRouter-Provider", provider)
	writeJSON(w, http.StatusOK, map[string]any{
		"url": resp.URL, "title": resp.Title, "content": resp.Content, "format": resp.Format,
	})
}

// ---- Video generation -------------------------------------------------------

// handleVideoGeneration submits an asynchronous video-generation job. Unknown
// JSON fields are retained so provider-specific generation controls pass through.
func (s *Server) handleVideoGeneration(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
			writeError(w, http.StatusUnsupportedMediaType, "video generation requires a JSON request body")
			return
		}
	}
	raw, ok := s.readRequestBody(w, r)
	if !ok {
		return
	}
	var head struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if head.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	opts, err := s.mediaOptions(r, head.Model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	req := &core.VideoRequest{
		Model:       modelTail(opts.Targets),
		Prompt:      head.Prompt,
		Body:        raw,
		ContentType: r.Header.Get("Content-Type"),
	}
	resp, provider, perr := s.pipeline.GenerateVideo(r.Context(), req, opts)
	if perr != nil {
		s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, perr)
		s.writeMediaError(w, perr)
		return
	}
	s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, nil)
	w.Header().Set("X-KeiRouter-Provider", provider)
	w.Header().Set("X-KeiRouter-Account", resp.AccountID)
	writeVideoResponse(w, resp)
}

// handleVideoPoll checks the status of an in-flight video job. The job id is in
// the path; the model (provider/model) is supplied as a query parameter so the
// poll routes to the same provider that created the job.
func (s *Server) handleVideoPoll(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "video id is required")
		return
	}
	model := r.URL.Query().Get("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model query parameter is required (provider/model)")
		return
	}
	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		accountID = r.Header.Get("X-KeiRouter-Account")
	}
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "account_id is required to poll the credential that submitted the video job")
		return
	}
	opts, err := s.mediaOptions(r, model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	opts.AccountID = accountID
	req := &core.VideoStatusRequest{Model: modelTail(opts.Targets), RequestID: id}
	resp, provider, perr := s.pipeline.PollVideo(r.Context(), req, opts)
	if perr != nil {
		s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, perr)
		s.writeMediaError(w, perr)
		return
	}
	s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, nil)
	w.Header().Set("X-KeiRouter-Provider", provider)
	w.Header().Set("X-KeiRouter-Account", resp.AccountID)
	writeVideoResponse(w, resp)
}

// writeVideoResponse renders a VideoResponse. When the upstream body was
// captured verbatim it is passed through untouched; otherwise a canonical
// summary is emitted.
func writeVideoResponse(w http.ResponseWriter, resp *core.VideoResponse) {
	if len(resp.Raw) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp.Raw)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"request_id": resp.RequestID, "status": resp.Status,
		"url": resp.URL, "urls": resp.URLs,
	})
}

// ---- Image understanding (image-to-text) ------------------------------------

func (s *Server) handleImageUnderstanding(w http.ResponseWriter, r *http.Request) {
	key, _ := authedKey(r.Context())
	var req core.ImageUnderstandingRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	hasImage := false
	for _, image := range req.Images {
		if strings.TrimSpace(image) != "" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		writeError(w, http.StatusBadRequest, "at least one image is required")
		return
	}
	opts, err := s.mediaOptions(r, req.Model)
	if err != nil {
		s.writeMediaError(w, err)
		return
	}
	req.Model = modelTail(opts.Targets)
	resp, provider, perr := s.pipeline.UnderstandImage(r.Context(), &req, opts)
	if perr != nil {
		s.logRequest(key.Name, provider, req.Model, 0, 0, 0, false, perr)
		s.writeMediaError(w, perr)
		return
	}
	s.logRequest(key.Name, provider, req.Model, resp.Usage.TotalTokens, 0, 0, false, nil)
	w.Header().Set("X-KeiRouter-Provider", provider)
	writeJSON(w, http.StatusOK, map[string]any{
		"model": resp.Model, "text": resp.Text,
		"usage": map[string]any{
			"prompt_tokens": resp.Usage.PromptTokens, "completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens": resp.Usage.TotalTokens,
		},
	})
}

// ---- helpers ----------------------------------------------------------------

// modelTail returns the model id of the first resolved target, used to pass the
// concrete model down to the pipeline. The pipeline overrides it per-attempt,
// but media requests usually resolve to a single provider/model target.
func modelTail(targets []dispatch.Target) string {
	if len(targets) > 0 {
		return targets[0].Model
	}
	return ""
}

// asBadModel reports whether err is a badModelError and assigns it to target.
func asBadModel(err error, target *badModelError) bool {
	if bm, ok := err.(badModelError); ok {
		*target = bm
		return true
	}
	return false
}

type accessDeniedError struct{ model string }

func (e accessDeniedError) Error() string {
	return "access denied: this API key is not permitted to use model " + e.model
}
