// Package dokploy calls the Dokploy REST API to run hosted 9router
// containers: create an application from a Docker image, attach a domain and
// deploy it.
package dokploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxResponseBytes bounds upstream response bodies. project.all can be large.
const maxResponseBytes = 4 << 20

// Client calls one Dokploy instance with an API key.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient builds a client. baseURL is the Dokploy panel root, for example
// https://dokploy.example.com (no /api suffix).
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/api"), "/"),
		apiKey:  apiKey,
		// Never follow redirects: Go forwards custom headers such as
		// x-api-key to the redirect target.
		http: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Application is the subset of a Dokploy application we use.
type Application struct {
	ApplicationID string `json:"applicationId"`
	AppName       string `json:"appName"`
	Name          string `json:"name"`
}

// Domain is the subset of a Dokploy domain we use.
type Domain struct {
	DomainID string `json:"domainId"`
	Host     string `json:"host"`
}

// CreateApplication creates an empty application in an environment. A
// non-empty serverID places it on that remote Dokploy server.
func (c *Client) CreateApplication(ctx context.Context, name, appName, environmentID, serverID string) (Application, error) {
	var out Application
	body := map[string]any{
		"name":          name,
		"appName":       appName,
		"description":   "Hosted 9router (keirouter)",
		"environmentId": environmentID,
	}
	if serverID != "" {
		body["serverId"] = serverID
	}
	err := c.post(ctx, "application.create", body, &out)
	if err != nil {
		return Application{}, err
	}
	if out.ApplicationID == "" {
		return Application{}, fmt.Errorf("dokploy: application.create returned no applicationId")
	}
	return out, nil
}

// SaveDockerProvider points the application at a public Docker image.
func (c *Client) SaveDockerProvider(ctx context.Context, applicationID, image string) error {
	return c.post(ctx, "application.saveDockerProvider", map[string]any{
		"applicationId": applicationID,
		"dockerImage":   image,
		"username":      nil,
		"password":      nil,
		"registryUrl":   nil,
	}, nil)
}

// SaveEnvironment sets the application's environment variables (KEY=VALUE
// lines).
func (c *Client) SaveEnvironment(ctx context.Context, applicationID, env string) error {
	return c.post(ctx, "application.saveEnvironment", map[string]any{
		"applicationId": applicationID,
		"env":           env,
		"buildArgs":     "",
		"buildSecrets":  "",
		"createEnvFile": false,
	}, nil)
}

// CreateDomain routes host to the application's port over HTTPS with a
// Let's Encrypt certificate.
func (c *Client) CreateDomain(ctx context.Context, applicationID, host string, port int) (Domain, error) {
	var out Domain
	err := c.post(ctx, "domain.create", map[string]any{
		"host":            host,
		"path":            "/",
		"port":            port,
		"https":           true,
		"certificateType": "letsencrypt",
		"applicationId":   applicationID,
		"domainType":      "application",
	}, &out)
	if err != nil {
		return Domain{}, err
	}
	return out, nil
}

// CreateVolumeMount mounts a named Docker volume into the application so its
// data survives redeploys.
func (c *Client) CreateVolumeMount(ctx context.Context, applicationID, volumeName, mountPath string) error {
	return c.post(ctx, "mounts.create", map[string]any{
		"type":        "volume",
		"volumeName":  volumeName,
		"mountPath":   mountPath,
		"serviceId":   applicationID,
		"serviceType": "application",
	}, nil)
}

// SetResources applies memory (bytes) and CPU (nano-CPUs) limits. Empty
// values are left unchanged.
func (c *Client) SetResources(ctx context.Context, applicationID, memoryLimit, cpuLimit string) error {
	body := map[string]any{"applicationId": applicationID}
	if memoryLimit != "" {
		body["memoryLimit"] = memoryLimit
	}
	if cpuLimit != "" {
		body["cpuLimit"] = cpuLimit
	}
	if len(body) == 1 {
		return nil
	}
	return c.post(ctx, "application.update", body, nil)
}

// Deploy queues a deployment of the application.
func (c *Client) Deploy(ctx context.Context, applicationID string) error {
	return c.post(ctx, "application.deploy", map[string]any{"applicationId": applicationID}, nil)
}

// Stop stops the application's container.
func (c *Client) Stop(ctx context.Context, applicationID string) error {
	return c.post(ctx, "application.stop", map[string]any{"applicationId": applicationID}, nil)
}

// Start starts a stopped application.
func (c *Client) Start(ctx context.Context, applicationID string) error {
	return c.post(ctx, "application.start", map[string]any{"applicationId": applicationID}, nil)
}

// Delete removes the application and its container.
func (c *Client) Delete(ctx context.Context, applicationID string) error {
	return c.post(ctx, "application.delete", map[string]any{"applicationId": applicationID}, nil)
}

// Server is a remote Dokploy server that can run applications.
type Server struct {
	ServerID  string `json:"serverId"`
	Name      string `json:"name"`
	IPAddress string `json:"ipAddress"`
	Status    string `json:"serverStatus"`
}

// ListServers returns the remote servers the key can see, minus build-only
// servers, which cannot run applications.
func (c *Client) ListServers(ctx context.Context) ([]Server, error) {
	data, err := c.do(ctx, http.MethodGet, "server.all", nil)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Server
		Type string `json:"serverType"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("dokploy: decode server.all: %w", err)
	}
	out := make([]Server, 0, len(rows))
	for _, r := range rows {
		if r.Type == "build" {
			continue
		}
		out = append(out, r.Server)
	}
	return out, nil
}

// CheckEnvironment confirms the API key works and that environmentID exists
// in some project the key can see.
func (c *Client) CheckEnvironment(ctx context.Context, environmentID string) error {
	data, err := c.do(ctx, http.MethodGet, "project.all", nil)
	if err != nil {
		return err
	}
	if environmentID == "" {
		return nil
	}
	var projects []struct {
		Environments []struct {
			EnvironmentID string `json:"environmentId"`
		} `json:"environments"`
	}
	if err := json.Unmarshal(data, &projects); err != nil {
		return fmt.Errorf("dokploy: decode project.all: %w", err)
	}
	for _, p := range projects {
		for _, e := range p.Environments {
			if e.EnvironmentID == environmentID {
				return nil
			}
		}
	}
	return fmt.Errorf("dokploy: environment %q not found", environmentID)
}

func (c *Client) post(ctx context.Context, endpoint string, body any, out any) error {
	data, err := c.do(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("dokploy: decode %s: %w", endpoint, err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/"+endpoint, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dokploy: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("dokploy: %s redirected (status %d); check the Dokploy URL", endpoint, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("dokploy: %s status %d: %s", endpoint, resp.StatusCode, msg)
	}
	return data, nil
}
