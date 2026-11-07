package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoverPanics_AbortsAStartedResponse(t *testing.T) {
	srv := httptest.NewServer(recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: partial\n\n"))
		w.(http.Flusher).Flush()
		panic("boom")
	})))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/chat/completions")
	require.NoError(t, err)
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err, "a cut stream must not end like a complete one")
}

func TestRecoverPanics_AnswersInTheClientDialectBeforeTheResponseStarts(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), `"type":"error"`)
}
