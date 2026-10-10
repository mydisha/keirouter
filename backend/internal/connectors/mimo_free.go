package connectors

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

const (
	mimoFreeDefaultBase   = "https://api.xiaomimimo.com"
	mimoFreeBootstrapURL  = mimoFreeDefaultBase + "/api/free-ai/bootstrap"
	mimoFreeChatBase     = mimoFreeDefaultBase + "/api/free-ai/openai"
	mimoFreeSourceHeader  = "mimocode-cli-free"
	mimoFreeRefreshBuf    = 5 * time.Minute
	mimoFreeDefaultExpiry = 50 * time.Minute
	mimoSystemSignature   = "You are MiMoCode, an interactive CLI tool that helps users with software engineering tasks."
	sessionAffinityChars  = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// MimoFree drives the Xiaomi MiMo free-tier endpoint. It speaks an
// OpenAI-compatible wire format with a custom bootstrap/JWT authentication
// flow and URL rewriting (/chat/completions → /chat).
//
// The free chat endpoint gates on a system message containing the MiMoCode
// signature in the request body. The connector injects this idempotently.
type MimoFree struct {
	id          string
	defaultBase string
	sessionID   string
	codec       transform.OpenAICodec

	mu       sync.Mutex
	cached   *mimoFreeJWT
	inflight *mimoFreeFetch
}

type mimoFreeJWT struct {
	token string
	exp   time.Time
}

type mimoFreeFetch struct {
	ch  chan struct{}
	jwt *mimoFreeJWT
	err error
}

func NewMimoFree(id, defaultBaseURL string) *MimoFree {
	base := defaultBaseURL
	if base == "" {
		base = mimoFreeDefaultBase
	}
	return &MimoFree{id: id, defaultBase: base, sessionID: newSessionAffinityID()}
}

func (c *MimoFree) ID() string            { return c.id }
func (c *MimoFree) Dialect() core.Dialect { return core.DialectMimoFree }

// --- bootstrap / JWT --------------------------------------------------------

func mimoFreeClientHash() string {
	seed := fmt.Sprintf("mimocode-free-%d", time.Now().UnixMilli())
	h := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%x", h[:])
}

func newSessionAffinityID() string {
	id := make([]byte, 4+24)
	copy(id, "ses_")
	for i := 4; i < len(id); i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(sessionAffinityChars))))
		id[i] = sessionAffinityChars[n.Int64()]
	}
	return string(id)
}

func mimoFreeBootstrap(ctx context.Context) (*mimoFreeJWT, error) {
	body, err := json.Marshal(map[string]string{"client": mimoFreeClientHash()})
	if err != nil {
		return nil, fmt.Errorf("mimo-free: marshal client hash: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mimoFreeBootstrapURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mimo-free: build bootstrap request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mimocode/0.1.0")
	req.Header.Set("Accept", "*/*")

	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, fmt.Errorf("mimo-free: bootstrap request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("mimo-free: bootstrap returned %d: %s", resp.StatusCode, truncateError(respBody))
	}

	var data struct {
		JWT       string `json:"jwt"`
		ExpiresIn int    `json:"expiresIn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("mimo-free: decode bootstrap response: %w", err)
	}
	if data.JWT == "" {
		return nil, fmt.Errorf("mimo-free: bootstrap response missing jwt")
	}

	exp := jwtExpiry(data.JWT)
	if data.ExpiresIn > 0 {
		exp = time.Now().Add(time.Duration(data.ExpiresIn) * time.Second)
	}
	return &mimoFreeJWT{token: data.JWT, exp: exp}, nil
}

// jwtExpiry extracts the exp claim from a JWT. Falls back to a conservative
// default when the token cannot be parsed.
func jwtExpiry(token string) time.Time {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return time.Now().Add(mimoFreeDefaultExpiry)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Now().Add(mimoFreeDefaultExpiry)
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Now().Add(mimoFreeDefaultExpiry)
	}
	return time.Unix(int64(claims.Exp), 0)
}

func (c *MimoFree) getJWT(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.cached != nil && time.Until(c.cached.exp) > mimoFreeRefreshBuf {
		tok := c.cached.token
		c.mu.Unlock()
		return tok, nil
	}
	if c.inflight != nil {
		f := c.inflight
		c.mu.Unlock()
		select {
		case <-f.ch:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if f.err != nil {
			return "", f.err
		}
		return f.jwt.token, nil
	}
	f := &mimoFreeFetch{ch: make(chan struct{})}
	c.inflight = f
	c.cached = nil
	c.mu.Unlock()

	jwt, err := mimoFreeBootstrap(ctx)
	f.jwt = jwt
	f.err = err
	close(f.ch)

	c.mu.Lock()
	c.inflight = nil
	if err == nil {
		c.cached = jwt
	}
	c.mu.Unlock()

	if err != nil {
		return "", err
	}
	return jwt.token, nil
}

func (c *MimoFree) invalidateJWT() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

// --- request construction ---------------------------------------------------

func (c *MimoFree) chatURL() string {
	return mimoFreeChatBase + "/chat"
}

func (c *MimoFree) buildHeaders(jwt string, creds core.Credentials) map[string]string {
	h := map[string]string{
		"Authorization":      bearer(jwt),
		"X-Mimo-Source":      mimoFreeSourceHeader,
		"x-session-affinity": c.sessionID,
	}
	return mergeHeaders(h, creds.Headers)
}

// injectMimoSystemMessage prepends the MiMoCode system signature to the
// messages array. It is a no-op when the signature is already present.
// On any parse failure the original body is returned unchanged.
func injectMimoSystemMessage(body []byte) []byte {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	raw, ok := req["messages"]
	if !ok || string(raw) == "null" {
		return body
	}

	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return body
	}

	for _, raw := range messages {
		var msg struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Role != "system" {
			continue
		}
		var text string
		if err := json.Unmarshal(msg.Content, &text); err == nil {
			if strings.Contains(text, mimoSystemSignature) {
				return body
			}
		}
	}

	sysMsg, _ := json.Marshal(map[string]string{
		"role":    "system",
		"content": mimoSystemSignature,
	})
	messages = append([]json.RawMessage{sysMsg}, messages...)

	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return body
	}
	req["messages"] = messagesJSON
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// --- public interface --------------------------------------------------------

// Chat performs a non-streaming completion against the MiMo free tier.
func (c *MimoFree) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	req.Stream = false
	body, err := c.codec.RenderRequestForProvider(req, c.id)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	body = injectMimoSystemMessage(body)

	chatURL := c.chatURL()
	for attempt := 0; attempt < 2; attempt++ {
		jwt, err := c.getJWT(ctx)
		if err != nil {
			return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
		}
		headers := c.buildHeaders(jwt, creds)

		respBody, err := doJSON(ctx, c.id, req.Model, chatURL, body, headers)
		if err != nil {
			if core.AsProviderError(err).Kind == core.ErrAuth && attempt == 0 {
				c.invalidateJWT()
				continue
			}
			return nil, err
		}

		resp, perr := c.codec.ParseResponse(respBody, req.Model)
		if perr != nil {
			return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: perr.Error(), Cause: perr}
		}
		return resp, nil
	}
	return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Model: req.Model, Message: "JWT exhausted after retry"}
}

// Stream performs a streaming completion against the MiMo free tier.
func (c *MimoFree) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	body, err := c.codec.RenderRequestForProvider(req, c.id)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	body = injectMimoSystemMessage(body)

	chatURL := c.chatURL()
	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		jwt, err := c.getJWT(ctx)
		if err != nil {
			return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
		}
		headers := c.buildHeaders(jwt, creds)

		resp, err = openStream(ctx, c.id, req.Model, chatURL, body, headers)
		if err != nil {
			if core.AsProviderError(err).Kind == core.ErrAuth && attempt == 0 {
				c.invalidateJWT()
				continue
			}
			return nil, err
		}
		break
	}
	if resp == nil {
		return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Model: req.Model, Message: "JWT exhausted after retry"}
	}

	out := make(chan core.StreamChunk, 16)
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		return c.codec.ParseStreamLine(payload, req.Model)
	})
	return out, nil
}

// Validate probes the MiMo free bootstrap endpoint to confirm the service is
// reachable. No credentials are required.
func (c *MimoFree) Validate(ctx context.Context, creds core.Credentials) error {
	c.invalidateJWT()
	_, err := c.getJWT(ctx)
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}
