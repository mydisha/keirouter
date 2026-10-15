package gateway

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// adminKeyUsage serves GET /api/keys/{id}/usage: the same usage payload the
// SSO portal shows a key's owner, for the admin key detail page. It replaces
// the public /v1/portal/keys/{id}/usage lookup, which this fork does not
// expose (portal usage requires a Google session bound to the key).
func (s *Server) adminKeyUsage(w http.ResponseWriter, r *http.Request) {
	key, err := s.identity.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	payload, err := s.buildKeyUsageMap(r.Context(), key, parseUsageDays(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "failed to build usage"))
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
