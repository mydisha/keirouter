package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

type relayRoundTripFunc func(*http.Request) (*http.Response, error)

func (f relayRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDeployCloudflareWorker(t *testing.T) {
	var uploaded bool
	var workerSrc string
	client := &http.Client{Transport: relayRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("missing API authorization")
		}
		response := map[string]any{"success": true}
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/workers/scripts/my-relay"):
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			file, _, err := r.FormFile("index.js")
			if err != nil {
				t.Fatal(err)
			}
			worker, _ := io.ReadAll(file)
			_ = file.Close()
			if !strings.Contains(string(worker), "x-relay-target") {
				t.Fatalf("worker is missing relay protocol")
			}
			if strings.Contains(string(worker), cloudflareRelayKeyPlaceholder) {
				t.Fatalf("worker relay key was not filled in")
			}
			workerSrc = string(worker)
			uploaded = true
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/workers/scripts/my-relay/subdomain"):
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/workers/subdomain"):
			response["result"] = map[string]string{"subdomain": "team"}
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		raw, _ := json.Marshal(response)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}

	req := httptest.NewRequest(http.MethodPost, "/proxy-pools/cloudflare-deploy", nil)
	deployURL, err := deployCloudflareWorker(req, client, "https://api.example", cloudflareDeployInput{
		AccountID: "account", APIToken: "token", ProjectName: "my-relay",
	})
	if err != nil {
		t.Fatal(err)
	}
	key, ok := strings.CutPrefix(deployURL, "https://my-relay.team.workers.dev/?key=")
	if !uploaded || !ok || len(key) != 64 {
		t.Fatalf("uploaded=%v deployURL=%q", uploaded, deployURL)
	}
	if !strings.Contains(workerSrc, `const RELAY_KEY = "`+key+`"`) {
		t.Fatalf("deploy URL key does not match the worker's key")
	}
}

func TestNormalizeCloudflareDeployInput(t *testing.T) {
	input := cloudflareDeployInput{
		AccountIDCamel: "ABCDEF0123456789ABCDEF0123456789",
		APITokenCamel:  "token_abcdefghijklmnopqrstuvwxyz",
		ProjectName:    " My-Relay ",
	}
	if err := normalizeCloudflareDeployInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.AccountID != "abcdef0123456789abcdef0123456789" || input.ProjectName != "my-relay" {
		t.Fatalf("input was not normalized: %+v", input)
	}

	invalid := cloudflareDeployInput{AccountID: "short", APIToken: input.APIToken}
	if err := normalizeCloudflareDeployInput(&invalid); err == nil {
		t.Fatal("expected invalid account ID to be rejected")
	}
}

func TestRelayReadinessRetriesTransientFailure(t *testing.T) {
	s, db := newBulkTestServer(t)
	s.pools = db.ProxyPools()
	now := time.Now()
	pool := store.ProxyPool{
		ID: "relay-pool", Name: "relay", Type: "cloudflare",
		ProxyURL: "https://relay.example.com", TestStatus: "testing",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.pools.Create(context.Background(), pool); err != nil {
		t.Fatal(err)
	}

	calls := 0
	s.runRelayReadiness(context.Background(), pool.ID, 3, 0, func(context.Context, string, time.Duration) proxyTestResult {
		calls++
		if calls == 1 {
			return proxyTestResult{status: "error", lastError: "relay returned HTTP 503", httpStatus: http.StatusServiceUnavailable}
		}
		return proxyTestResult{status: "active", httpStatus: http.StatusOK}
	})

	updated, err := s.pools.Get(context.Background(), pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || updated.TestStatus != "active" || !updated.IsActive || updated.LastError != "" || updated.LastTested == nil {
		t.Fatalf("unexpected readiness result: calls=%d pool=%+v", calls, updated)
	}
}
