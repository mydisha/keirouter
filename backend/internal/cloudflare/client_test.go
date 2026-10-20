package cloudflare

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClientWithBase(srv.URL, "tok")
}

func TestCreateA(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/zones/z1/dns_records", r.URL.Path)
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(raw, &body))
		require.Equal(t, "A", body["type"])
		require.Equal(t, "ab.tokenizer.ai.id", body["name"])
		require.Equal(t, "203.0.113.7", body["content"])
		require.Equal(t, true, body["proxied"])
		require.EqualValues(t, 1, body["ttl"])
		require.Equal(t, "keirouter hosting h1", body["comment"])
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"r1","name":"ab.tokenizer.ai.id","content":"203.0.113.7"}}`))
	})
	rec, err := c.CreateA(t.Context(), "z1", "ab.tokenizer.ai.id", "203.0.113.7", "keirouter hosting h1")
	require.NoError(t, err)
	require.Equal(t, Record{ID: "r1", Name: "ab.tokenizer.ai.id", Content: "203.0.113.7"}, rec)
}

func TestFindA(t *testing.T) {
	found := false
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/zones/z1/dns_records", r.URL.Path)
		require.Equal(t, "A", r.URL.Query().Get("type"))
		require.Equal(t, "ab.tokenizer.ai.id", r.URL.Query().Get("name.exact"))
		if found {
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"r9","name":"ab.tokenizer.ai.id","content":"203.0.113.7"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
	})
	_, ok, err := c.FindA(t.Context(), "z1", "ab.tokenizer.ai.id")
	require.NoError(t, err)
	require.False(t, ok)

	found = true
	rec, ok, err := c.FindA(t.Context(), "z1", "ab.tokenizer.ai.id")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "r9", rec.ID)
}

func TestDeleteNotFoundIsOK(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/zones/z1/dns_records/r1", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":81044,"message":"Record does not exist."}]}`))
	})
	require.NoError(t, c.Delete(t.Context(), "z1", "r1"))
}

func TestErrorMessageSurfacedTokenHidden(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
	})
	_, err := c.Zone(t.Context(), "z1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Authentication error")
	require.NotContains(t, err.Error(), "tok")
}

func TestZone(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/zones/z1", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"result":{"name":"tokenizer.ai.id"}}`))
	})
	name, err := c.Zone(t.Context(), "z1")
	require.NoError(t, err)
	require.Equal(t, "tokenizer.ai.id", name)
}
