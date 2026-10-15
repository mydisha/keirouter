package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/store"
)

const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

const (
	cloudflareReadinessAttempts     = 12
	cloudflareReadinessInterval     = 3 * time.Second
	cloudflareReadinessProbeTimeout = 8 * time.Second
)

// cloudflareRelayKeyPlaceholder is replaced with a per-deploy secret. The
// Worker only relays requests carrying it as ?key=, so a discovered
// workers.dev URL cannot be used as an open proxy on the operator's account.
const cloudflareRelayKeyPlaceholder = "__KEIROUTER_RELAY_KEY__"

// cloudflareRelayWorker forwards x-relay-target + x-relay-path. It drops the
// headers Cloudflare adds about the caller (CF-Connecting-IP, X-Forwarded-For,
// ...): forwarding them would hand the provider the KeiRouter server's own IP,
// defeating the relay.
const cloudflareRelayWorker = `const RELAY_KEY = "` + cloudflareRelayKeyPlaceholder + `";
const DROP_HEADERS = [
  "x-relay-target", "x-relay-path", "host",
  "cf-connecting-ip", "cf-connecting-ipv6", "cf-ipcountry", "cf-ray", "cf-visitor",
  "cf-worker", "cf-ew-via", "cdn-loop", "true-client-ip",
  "x-forwarded-for", "x-forwarded-proto", "x-forwarded-host", "x-real-ip", "forwarded",
];

export default {
  async fetch(request) {
    if (new URL(request.url).searchParams.get("key") !== RELAY_KEY) {
      return new Response(JSON.stringify({ error: "Forbidden" }), {
        status: 403,
        headers: { "content-type": "application/json" },
      });
    }
    const target = request.headers.get("x-relay-target");
    const relayPath = request.headers.get("x-relay-path") || "/";

    if (!target) {
      return new Response(JSON.stringify({ error: "Missing x-relay-target header" }), {
        status: 400,
        headers: { "content-type": "application/json" },
      });
    }

    const targetUrl = target.replace(/\/$/, "") + relayPath;
    if (!/^https?:\/\/[^/?#]+$/.test(target.replace(/\/$/, ""))) {
      return new Response(JSON.stringify({ error: "Invalid x-relay-target header" }), {
        status: 400,
        headers: { "content-type": "application/json" },
      });
    }
    const headers = new Headers(request.headers);
    for (const name of DROP_HEADERS) headers.delete(name);

    const init = { method: request.method, headers };
    if (request.method !== "GET" && request.method !== "HEAD") {
      init.body = request.body;
      init.duplex = "half";
    }

    try {
      const response = await fetch(targetUrl, init);
      return new Response(response.body, {
        status: response.status,
        headers: response.headers,
      });
    } catch (error) {
      return new Response(JSON.stringify({ error: error.message }), {
        status: 502,
        headers: { "content-type": "application/json" },
      });
    }
  },
};
`

var cloudflareWorkerName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var cloudflareAccountID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type cloudflareDeployInput struct {
	AccountID        string `json:"account_id"`
	AccountIDCamel   string `json:"accountId"`
	APIToken         string `json:"api_token"`
	APITokenCamel    string `json:"apiToken"`
	ProjectName      string `json:"project_name"`
	ProjectNameCamel string `json:"projectName"`
}

type cloudflareEnvelope struct {
	Success *bool `json:"success"`
	Result  struct {
		Subdomain string `json:"subdomain"`
	} `json:"result"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (s *Server) adminDeployCloudflareRelay(w http.ResponseWriter, r *http.Request) {
	var body cloudflareDeployInput
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := normalizeCloudflareDeployInput(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.pools == nil {
		writeError(w, http.StatusInternalServerError, "proxy pools not configured")
		return
	}

	client := &http.Client{Timeout: 45 * time.Second}
	deployURL, err := deployCloudflareWorker(r, client, cloudflareAPIBase, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	now := time.Now()
	pool := store.ProxyPool{
		ID:         uuid.NewString(),
		Name:       body.ProjectName,
		Type:       "cloudflare",
		ProxyURL:   deployURL,
		IsActive:   false,
		TestStatus: "testing",
		LastError:  "Waiting for Worker propagation; readiness will retry automatically",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.pools.Create(r.Context(), pool); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "relay deployed but proxy pool creation failed"))
		return
	}
	go s.monitorRelayReadiness(pool.ID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id": pool.ID, "name": pool.Name, "deploy_url": deployURL,
		"deployUrl": deployURL, "test_status": pool.TestStatus,
		"proxyPool": map[string]any{
			"id": pool.ID, "name": pool.Name, "type": pool.Type,
			"proxyUrl": pool.ProxyURL, "noProxy": pool.NoProxy,
			"isActive": pool.IsActive, "strictProxy": pool.Strict,
		},
	})
}

func normalizeCloudflareDeployInput(input *cloudflareDeployInput) error {
	input.AccountID = strings.ToLower(strings.TrimSpace(defaultStr(input.AccountID, input.AccountIDCamel)))
	input.APIToken = strings.TrimSpace(defaultStr(input.APIToken, input.APITokenCamel))
	input.ProjectName = strings.ToLower(strings.TrimSpace(defaultStr(input.ProjectName, input.ProjectNameCamel)))
	if input.AccountID == "" || input.APIToken == "" {
		return fmt.Errorf("account_id and api_token are required")
	}
	if !cloudflareAccountID.MatchString(input.AccountID) {
		return fmt.Errorf("account_id must be a 32-character Cloudflare account ID")
	}
	if len(input.APIToken) < 20 || len(strings.Fields(input.APIToken)) != 1 {
		return fmt.Errorf("api_token is not a valid Cloudflare API token")
	}
	if input.ProjectName == "" {
		input.ProjectName = "relay-" + strings.ToLower(uuid.NewString()[:8])
	}
	if !cloudflareWorkerName.MatchString(input.ProjectName) {
		return fmt.Errorf("project_name must be 1-63 lowercase letters, numbers, or hyphens and cannot start or end with a hyphen")
	}
	return nil
}

func (s *Server) monitorRelayReadiness(poolID string) {
	maxDuration := time.Duration(cloudflareReadinessAttempts)*cloudflareReadinessProbeTimeout +
		time.Duration(cloudflareReadinessAttempts-1)*cloudflareReadinessInterval + 5*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
	defer cancel()
	s.runRelayReadiness(ctx, poolID, cloudflareReadinessAttempts, cloudflareReadinessInterval, testRelayPool)
}

func (s *Server) runRelayReadiness(
	ctx context.Context,
	poolID string,
	attempts int,
	interval time.Duration,
	probe func(context.Context, string, time.Duration) proxyTestResult,
) {
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		pool, err := s.pools.Get(ctx, poolID)
		if err != nil {
			return
		}
		result := probe(ctx, pool.ProxyURL, cloudflareReadinessProbeTimeout)
		now := time.Now()
		// Narrow writes: the operator may rename or edit the pool while the
		// Worker propagates, and a full-row update would revert that.
		if result.status == "active" {
			active := true
			_ = s.pools.RecordTest(ctx, poolID, "active", now, "", &active)
			return
		}

		if attempt == attempts || !retryableRelayReadiness(result) {
			_ = s.pools.RecordTest(ctx, poolID, "error", now, result.lastError, nil)
			return
		}
		msg := fmt.Sprintf("Worker propagation in progress (%s); retry %d/%d is scheduled automatically", result.lastError, attempt+1, attempts)
		if err := s.pools.RecordTest(ctx, poolID, "testing", now, msg, nil); err != nil {
			return
		}
	}
}

func retryableRelayReadiness(result proxyTestResult) bool {
	if result.httpStatus == 0 {
		return true
	}
	switch result.httpStatus {
	case http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooEarly,
		http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return result.httpStatus >= 520 && result.httpStatus <= 527
	}
}

func deployCloudflareWorker(r *http.Request, client *http.Client, apiBase string, input cloudflareDeployInput) (string, error) {
	scriptURL := strings.TrimRight(apiBase, "/") + "/accounts/" + url.PathEscape(input.AccountID) + "/workers/scripts/" + url.PathEscape(input.ProjectName)

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	jsHeader := make(textproto.MIMEHeader)
	jsHeader.Set("Content-Disposition", `form-data; name="index.js"; filename="index.js"`)
	jsHeader.Set("Content-Type", "application/javascript+module")
	part, err := writer.CreatePart(jsHeader)
	if err != nil {
		return "", fmt.Errorf("build worker upload: %w", err)
	}
	relayKey := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	if _, err := io.WriteString(part, strings.Replace(cloudflareRelayWorker, cloudflareRelayKeyPlaceholder, relayKey, 1)); err != nil {
		return "", fmt.Errorf("build worker upload: %w", err)
	}

	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Disposition", `form-data; name="metadata"; filename="metadata.json"`)
	metadataHeader.Set("Content-Type", "application/json")
	part, err = writer.CreatePart(metadataHeader)
	if err != nil {
		return "", fmt.Errorf("build worker metadata: %w", err)
	}
	metadata := `{"main_module":"index.js","compatibility_date":"2024-03-20","observability":{"enabled":true}}`
	if _, err := io.WriteString(part, metadata); err != nil {
		return "", fmt.Errorf("build worker metadata: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("finish worker upload: %w", err)
	}

	if _, err := cloudflareRequest(r, client, http.MethodPut, scriptURL, input.APIToken, writer.FormDataContentType(), &form); err != nil {
		return "", fmt.Errorf("worker upload failed: %w", err)
	}

	enableBody := bytes.NewBufferString(`{"enabled":true}`)
	_, _ = cloudflareRequest(r, client, http.MethodPost, scriptURL+"/subdomain", input.APIToken, "application/json", enableBody)

	raw, err := cloudflareRequest(r, client, http.MethodGet,
		strings.TrimRight(apiBase, "/")+"/accounts/"+url.PathEscape(input.AccountID)+"/workers/subdomain",
		input.APIToken, "", nil)
	if err != nil {
		return "", fmt.Errorf("retrieve workers.dev subdomain: %w", err)
	}
	var envelope cloudflareEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || strings.TrimSpace(envelope.Result.Subdomain) == "" {
		return "", fmt.Errorf("worker deployed but workers.dev subdomain could not be determined")
	}
	subdomain := strings.ToLower(strings.TrimSpace(envelope.Result.Subdomain))
	if !cloudflareWorkerName.MatchString(subdomain) {
		return "", fmt.Errorf("worker deployed but Cloudflare returned an invalid workers.dev subdomain")
	}
	deployURL := "https://" + input.ProjectName + "." + subdomain + ".workers.dev/?key=" + relayKey
	if err := validateProxyPoolURL("cloudflare", deployURL); err != nil {
		return "", fmt.Errorf("worker deployed but the relay URL is invalid: %w", err)
	}
	return deployURL, nil
}

func cloudflareRequest(r *http.Request, client *http.Client, method, endpoint, token, contentType string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(r.Context(), method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var envelope cloudflareEnvelope
		_ = json.Unmarshal(raw, &envelope)
		message := "request rejected"
		if len(envelope.Errors) > 0 && strings.TrimSpace(envelope.Errors[0].Message) != "" {
			message = envelope.Errors[0].Message
		}
		return nil, fmt.Errorf("%s (HTTP %d)", message, resp.StatusCode)
	}
	var envelope cloudflareEnvelope
	if json.Unmarshal(raw, &envelope) == nil && envelope.Success != nil && !*envelope.Success {
		message := "request rejected"
		if len(envelope.Errors) > 0 && strings.TrimSpace(envelope.Errors[0].Message) != "" {
			message = envelope.Errors[0].Message
		}
		return nil, fmt.Errorf("%s", message)
	}
	return raw, nil
}
