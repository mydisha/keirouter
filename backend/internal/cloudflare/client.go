// Package cloudflare manages the DNS records of hosted instances through the
// Cloudflare v4 API.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the Cloudflare v4 API root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

const maxResponseBytes = 1 << 20

// codeRecordNotFound is Cloudflare's "Record does not exist." error.
const codeRecordNotFound = 81044

// Record is the subset of a DNS record we use.
type Record struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Client calls the Cloudflare API with one API token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client for the public Cloudflare API.
func NewClient(token string) *Client { return NewClientWithBase(DefaultBaseURL, token) }

// NewClientWithBase builds a client against another API root (tests).
func NewClientWithBase(base, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(base, "/"),
		token:   token,
		// Never follow redirects: Go would forward the Authorization header.
		http: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Zone returns the name of the zone, which also proves the token can read it.
func (c *Client) Zone(ctx context.Context, zoneID string) (string, error) {
	var out struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID), nil, &out); err != nil {
		return "", err
	}
	return out.Name, nil
}

// FindA returns the A record with exactly this name, if any.
func (c *Client) FindA(ctx context.Context, zoneID, name string) (Record, bool, error) {
	q := url.Values{"type": {"A"}, "name.exact": {name}}
	var out []Record
	if err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records?"+q.Encode(), nil, &out); err != nil {
		return Record{}, false, err
	}
	if len(out) == 0 {
		return Record{}, false, nil
	}
	return out[0], true, nil
}

// CreateA adds a proxied A record with automatic TTL.
func (c *Client) CreateA(ctx context.Context, zoneID, name, ip, comment string) (Record, error) {
	body := map[string]any{"type": "A", "name": name, "content": ip, "proxied": true, "ttl": 1, "comment": comment}
	var out Record
	err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", body, &out)
	return out, err
}

// Delete removes a record. A record that no longer exists counts as deleted.
func (c *Client) Delete(ctx context.Context, zoneID, recordID string) error {
	err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil, nil)
	var apiErr *apiError
	if errors.As(err, &apiErr) && (apiErr.status == http.StatusNotFound || apiErr.has(codeRecordNotFound)) {
		return nil
	}
	return err
}

type apiError struct {
	status int
	codes  []int
	msg    string
}

func (e *apiError) Error() string { return "cloudflare: " + e.msg }

func (e *apiError) has(code int) bool {
	for _, c := range e.codes {
		if c == code {
			return true
		}
	}
	return false
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %s %s: request failed", method, strings.SplitN(path, "?", 2)[0])
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("cloudflare: read response: %w", err)
	}
	var env struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(data, &env)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Success {
		e := &apiError{status: resp.StatusCode}
		var msgs []string
		for _, x := range env.Errors {
			e.codes = append(e.codes, x.Code)
			msgs = append(msgs, x.Message)
		}
		e.msg = strings.Join(msgs, "; ")
		if e.msg == "" {
			e.msg = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return e
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("cloudflare: decode response: %w", err)
	}
	return nil
}
