package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/mydisha/keirouter/backend/internal/core"
	qoderlib "github.com/mydisha/keirouter/backend/internal/qoder"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// Qoder drives Qoder's COSY-signed inference endpoint at api3.qoder.sh.
// Unlike standard OpenAI-compatible providers, every Qoder request requires:
//   - A custom payload shape (chat_context, model_config, business blocks)
//   - WAF-bypass body encoding (&Encode=1)
//   - COSY signing (RSA+AES+MD5 with ~17 Cosy-* headers)
//   - SSE envelope unwrapping ({statusCodeValue, body} → plain OpenAI chunks)
//
// Two auth methods are supported. OAuth device-token connections store
// user_id/machine_id/email in creds.Extra and carry the access token as the
// COSY security_oauth_token. Personal Access Token (PAT, pt-*) connections
// carry the PAT in creds.APIKey; a raw PAT cannot sign COSY directly, so it is
// first exchanged for a short-lived job token (jt-*) that becomes the COSY
// security_oauth_token (mirrors qodercli's headless PAT flow).
type Qoder struct {
	id          string
	defaultBase string
	codec       transform.OpenAICodec

	// Model catalog cache (COSY-signed /algo/api/v2/model/list).
	mu      sync.RWMutex
	catalog map[string]*qoderCatalogEntry // keyed by user_id
}

// qoderPATCache is the process-wide resolved-session cache for PAT
// connections, keyed by the pt-* token. It is shared by every Qoder instance
// (dispatch connectors and the quota source) and the exchange is
// single-flighted, so each PAT holds at most one live jt-* job token at a
// time even under concurrent cold-cache requests — the upstream may
// invalidate earlier job tokens when a new one is minted.
var qoderPATCache = struct {
	sync.Mutex
	sessions map[string]qoderPATSession
	flight   singleflight.Group
}{sessions: make(map[string]qoderPATSession)}

// qoderCatalogEntry is a cached model catalog for one Qoder account.
type qoderCatalogEntry struct {
	fetchedAt  time.Time
	rawConfigs map[string]json.RawMessage // key → full model_config JSON
}

// qoderPATSession is a cached PAT resolution: the exchanged jt-* job token
// plus the token owner's real COSY identity fetched from user/status. The
// COSY envelope's uid must be this real user id, not a synthetic value.
type qoderPATSession struct {
	jobToken  string
	uid       string
	email     string
	name      string
	expiresAt time.Time
}

const qoderCatalogTTL = 1 * time.Hour

// Job-token lifetime bounds. Qoder issues job tokens valid for ~24h; refresh a
// little early to avoid signing with a just-expired token.
const (
	qoderJobTokenDefaultTTL = 23 * time.Hour
	qoderJobTokenMinTTL     = 1 * time.Minute
	// qoderJobTokenRefreshSkew refreshes the job token a little before its
	// reported expiry so a signed request never uses a just-expired token.
	qoderJobTokenRefreshSkew = 5 * time.Minute
)

// NewQoder builds a Qoder connector.
func NewQoder(id, defaultBaseURL string) *Qoder {
	return &Qoder{
		id:          id,
		defaultBase: defaultBaseURL,
		catalog:     make(map[string]*qoderCatalogEntry),
	}
}

func (c *Qoder) ID() string            { return c.id }
func (c *Qoder) Dialect() core.Dialect { return core.DialectQoder }

// --- Credential helpers -----------------------------------------------------

// cosyCreds extracts the COSY signing material from core.Credentials.
// The Qoder OAuth flow stores user_id and machine_id in creds.Extra at
// connect time; the vault surfaces them here.
func (c *Qoder) cosyCreds(creds core.Credentials) qoderlib.CosyCreds {
	return qoderlib.CosyCreds{
		UserID:    creds.Extra["user_id"],
		AuthToken: creds.AccessToken,
		Name:      creds.Extra["email"], // best-effort; COSY name is optional
		Email:     creds.Extra["email"],
		MachineID: creds.Extra["machine_id"],
	}
}

// qoderPATToken resolves the raw Personal Access Token from the credentials.
// The PAT arrives in the dedicated APIKey field; imported connections may
// carry it as the access token instead.
func qoderPATToken(creds core.Credentials) string {
	if creds.APIKey != "" {
		return creds.APIKey
	}
	return creds.AccessToken
}

// qoderIsPAT reports whether the credentials authenticate with a Personal
// Access Token rather than an OAuth device-token session. Account creation
// marks PAT connections with qoder_auth_method=pat; the pt-* prefix is a
// belt-and-suspenders fallback for imported credentials.
func qoderIsPAT(creds core.Credentials) bool {
	if creds.Extra["qoder_auth_method"] == "pat" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(qoderPATToken(creds)), "pt-")
}

// validateCreds returns an error when the credential is missing the fields
// required for COSY signing. PAT connections only need the PAT itself (the
// job token and synthetic uid are derived at request time); OAuth connections
// need the user_id and access token captured at connect time.
func (c *Qoder) validateCreds(creds core.Credentials) error {
	if qoderIsPAT(creds) {
		if qoderPATToken(creds) == "" {
			return &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Message: "qoder credential is missing personal access token; reconnect the account"}
		}
		return nil
	}
	cc := c.cosyCreds(creds)
	if cc.UserID == "" {
		return &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Message: "qoder credential is missing user_id; reconnect the account"}
	}
	if cc.AuthToken == "" {
		return &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Message: "qoder credential is missing access token; reconnect the account"}
	}
	return nil
}

// resolveCosyCreds returns the COSY signing material for a request. OAuth
// connections use their stored fields directly. PAT connections exchange the
// PAT for a short-lived job token and pair it with the token owner's real
// user id (fetched from user/status) — the upstream rejects a synthetic uid
// with "Login expired".
func (c *Qoder) resolveCosyCreds(ctx context.Context, creds core.Credentials) (qoderlib.CosyCreds, error) {
	if !qoderIsPAT(creds) {
		return c.cosyCreds(creds), nil
	}
	sess, err := c.resolveQoderSession(ctx, qoderPATToken(creds))
	if err != nil {
		return qoderlib.CosyCreds{}, err
	}
	return qoderlib.CosyCreds{
		UserID:    sess.uid,
		AuthToken: sess.jobToken,
		Name:      sess.name,
		Email:     sess.email,
		MachineID: creds.Extra["machine_id"],
	}, nil
}

// resolveQoderSession returns a cached job token plus the owner's real COSY
// identity for the PAT, refreshing when the cache is empty or near expiry.
// The refresh is single-flighted per PAT: concurrent cold-cache callers share
// one exchange instead of minting competing job tokens.
func (c *Qoder) resolveQoderSession(ctx context.Context, pat string) (qoderPATSession, error) {
	qoderPATCache.Lock()
	if sess, ok := qoderPATCache.sessions[pat]; ok && time.Now().Before(sess.expiresAt) {
		qoderPATCache.Unlock()
		return sess, nil
	}
	qoderPATCache.Unlock()

	v, err, _ := qoderPATCache.flight.Do(pat, func() (any, error) {
		// A previous flight may have refreshed the session while this caller
		// was queued behind it.
		qoderPATCache.Lock()
		if sess, ok := qoderPATCache.sessions[pat]; ok && time.Now().Before(sess.expiresAt) {
			qoderPATCache.Unlock()
			return sess, nil
		}
		qoderPATCache.Unlock()

		token, ttl, err := c.exchangeJobToken(ctx, pat)
		if err != nil {
			return qoderPATSession{}, err
		}
		st, err := c.fetchUserStatus(ctx, token)
		if err != nil {
			return qoderPATSession{}, err
		}

		sess := qoderPATSession{
			jobToken:  token,
			uid:       st.ID,
			email:     st.Email,
			name:      st.Name,
			expiresAt: time.Now().Add(ttl),
		}
		qoderPATCache.Lock()
		qoderPATCache.sessions[pat] = sess
		qoderPATCache.Unlock()
		return sess, nil
	})
	if err != nil {
		return qoderPATSession{}, err
	}
	return v.(qoderPATSession), nil
}

// qoderUserStatus is the token owner's account profile from user/status. The
// id is the real COSY uid; email/name fill the COSY user-info envelope; the
// plan/quota fields drive the account quota panel.
type qoderUserStatus struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Email           string  `json:"email"`
	UserType        string  `json:"userType"`
	Quota           float64 `json:"quota"`
	IsQuotaExceeded bool    `json:"isQuotaExceeded"`
	Plan            string  `json:"plan"`
	UserTag         string  `json:"userTag"`
	NextResetAt     int64   `json:"nextResetAt"`
}

// fetchUserStatus reads the token owner's account profile from user/status
// using a plain Bearer job token (no COSY). The returned id is the real COSY
// uid; email and name fill the COSY user-info envelope; plan/quota fields feed
// FetchQuota.
func (c *Qoder) fetchUserStatus(ctx context.Context, jobToken string) (*qoderUserStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qoderlib.UserStatusURL, nil)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Authorization", "Bearer "+jobToken)
	req.Header.Set("Accept", "application/json")

	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Message: "qoder user status: " + err.Error(), Cause: err}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode >= 400 {
		kind := core.ErrUpstream
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			kind = core.ErrAuth
		}
		return nil, &core.ProviderError{Kind: kind, Provider: c.id, StatusCode: resp.StatusCode, Message: "qoder user status failed: " + truncateError(body)}
	}

	var status qoderUserStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Message: "qoder user status: parse: " + err.Error(), Cause: err}
	}
	if strings.TrimSpace(status.ID) == "" {
		return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Message: "qoder user status returned no user id; check the personal access token"}
	}
	return &status, nil
}

// --- Quota -----------------------------------------------------------------

func init() {
	// Register an instance for quota lookups. PAT sessions live in the shared
	// package-level qoderPATCache, so quota fetches reuse the job token minted
	// by the dispatch connector instead of performing a second exchange.
	RegisterQuotaSource("qoder", NewQoder("qoder", ""))
}

// FetchQuota reports the Qoder account's plan and request quota, read from the
// non-COSY user/status endpoint. Only PAT connections expose a queryable quota;
// other cases return a plain message (not an error) so the UI degrades cleanly.
func (c *Qoder) FetchQuota(ctx context.Context, creds core.Credentials) (*QuotaResult, error) {
	if !qoderIsPAT(creds) {
		return &QuotaResult{Message: "Qoder quota is only available for Personal Access Token connections."}, nil
	}
	if qoderPATToken(creds) == "" {
		return &QuotaResult{Message: "No personal access token; cannot fetch quota."}, nil
	}
	sess, err := c.resolveQoderSession(ctx, qoderPATToken(creds))
	if err != nil {
		return nil, err
	}
	st, err := c.fetchUserStatus(ctx, sess.jobToken)
	if err != nil {
		return nil, err
	}
	return qoderQuotaFromStatus(st), nil
}

// qoderQuotaFromStatus maps a user/status payload into a QuotaResult. Mirrors
// OmniRoute's parseQoderUserStatusUsage: exhausted quota reports a 0-remaining
// bar; pooled team/enterprise seats report a shared-pool note; accounts with no
// per-user counter (trials) report a not-metered note (a limit:0 bar would
// render as 0%/exhausted in the UI).
func qoderQuotaFromStatus(st *qoderUserStatus) *QuotaResult {
	plan := prettifyQoderPlan(st.Plan, st.UserTag)
	resetAt := qoderResetAt(st.NextResetAt)
	userType := strings.ToLower(strings.TrimSpace(st.UserType))
	pooled := userType == "teams" || userType == "enterprise"
	quota := int(st.Quota)

	result := &QuotaResult{PlanName: plan}
	switch {
	case st.IsQuotaExceeded:
		result.Quotas = append(result.Quotas, QuotaEntry{
			ResourceType: "requests",
			Used:         quota,
			Limit:        quota,
			Remaining:    0,
			ResetAt:      resetAt,
			PlanName:     plan,
		})
		result.Message = "Quota exceeded."
	case pooled:
		result.Message = plan + " plan · shared team pool"
	case quota <= 0:
		// Trial / individual plans that Qoder does not meter with a per-user
		// counter report quota:0 while isQuotaExceeded stays false. There is no
		// remaining-credit number to show, so name the plan and say so plainly.
		result.Message = plan + " plan · usage not metered by Qoder"
	default:
		result.Quotas = append(result.Quotas, QuotaEntry{
			ResourceType: "requests",
			Used:         0,
			Limit:        quota,
			Remaining:    quota,
			ResetAt:      resetAt,
			PlanName:     plan,
		})
	}
	return result
}

// prettifyQoderPlan turns Qoder's PLAN_TIER_* enum / userTag into a label.
// The userTag (e.g. "Pro Trial") is preferred when present.
func prettifyQoderPlan(plan, userTag string) string {
	if tag := strings.TrimSpace(userTag); tag != "" {
		return tag
	}
	stripped := strings.TrimSpace(plan)
	if len(stripped) >= len("PLAN_TIER_") && strings.EqualFold(stripped[:len("PLAN_TIER_")], "PLAN_TIER_") {
		stripped = stripped[len("PLAN_TIER_"):]
	}
	if stripped == "" {
		return "Qoder"
	}
	words := strings.FieldsFunc(stripped, func(r rune) bool { return r == '_' || r == ' ' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

// qoderResetAt renders the epoch-millisecond reset time as RFC3339 (empty when
// unset). RFC3339 is understood by both the account quota bar and the quota
// page's countdown formatter; a bare millisecond string would not parse.
func qoderResetAt(nextResetAt int64) string {
	if nextResetAt <= 0 {
		return ""
	}
	return time.UnixMilli(nextResetAt).UTC().Format(time.RFC3339)
}

// exchangeJobToken POSTs the PAT to Qoder's job-token exchange and returns the
// jt-* job token plus its lifetime. A raw PAT cannot sign COSY directly; this
// mirrors qodercli's headless exchange step.
func (c *Qoder) exchangeJobToken(ctx context.Context, pat string) (string, time.Duration, error) {
	reqBody, err := json.Marshal(map[string]string{"personal_token": pat})
	if err != nil {
		return "", 0, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qoderlib.JobTokenExchangeURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", 0, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return "", 0, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Message: "qoder job token exchange: " + err.Error(), Cause: err}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if resp.StatusCode >= 400 {
		kind := core.ErrUpstream
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			kind = core.ErrAuth
		}
		return "", 0, &core.ProviderError{Kind: kind, Provider: c.id, StatusCode: resp.StatusCode, Message: "qoder job token exchange failed: " + truncateError(body)}
	}

	token, ttl := parseQoderJobToken(body)
	if token == "" {
		return "", 0, &core.ProviderError{Kind: core.ErrAuth, Provider: c.id, Message: "qoder job token exchange returned no job token; check the personal access token"}
	}
	return token, ttl, nil
}

// parseQoderJobToken extracts the jt-* job token and its lifetime from the
// (loosely-specified) exchange response. The token appears under "token" (or
// job_token/jobToken/jt) at the root or under a data object. Expiry is taken
// from the RFC3339 expires_at when present; the numeric expires_in is reported
// in milliseconds. Defaults to ~24h and is clamped to a sane minimum.
func parseQoderJobToken(body []byte) (string, time.Duration) {
	type jobTokenFields struct {
		JobToken     string  `json:"job_token"`
		JobTokenCaml string  `json:"jobToken"`
		JT           string  `json:"jt"`
		Token        string  `json:"token"`
		ExpiresAt    string  `json:"expires_at"`
		ExpiresIn    float64 `json:"expires_in"`
		ExpiresInCml float64 `json:"expiresIn"`
	}
	var root struct {
		jobTokenFields
		Data jobTokenFields `json:"data"`
	}
	if json.Unmarshal(body, &root) != nil {
		return "", 0
	}
	candidates := []string{
		root.JobToken, root.JobTokenCaml, root.JT, root.Token,
		root.Data.JobToken, root.Data.JobTokenCaml, root.Data.JT, root.Data.Token,
	}
	var token string
	for _, cand := range candidates {
		if trimmed := strings.TrimSpace(cand); strings.HasPrefix(trimmed, "jt-") {
			token = trimmed
			break
		}
	}
	if token == "" {
		return "", 0
	}

	ttl := qoderJobTokenDefaultTTL
	derived := false
	// Prefer the absolute expiry timestamp; refresh a little before it lapses.
	for _, ts := range []string{root.ExpiresAt, root.Data.ExpiresAt} {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(ts)); err == nil {
			if d := time.Until(t); d > 0 {
				ttl = d
				derived = true
			}
			break
		}
	}
	// Fall back to the numeric expires_in (milliseconds).
	if !derived {
		for _, ms := range []float64{root.ExpiresIn, root.ExpiresInCml, root.Data.ExpiresIn, root.Data.ExpiresInCml} {
			if ms > 0 {
				ttl = time.Duration(ms) * time.Millisecond
				derived = true
				break
			}
		}
	}
	// Refresh slightly early so a signed request never uses a just-expired
	// token. Only skew a server-reported lifetime, never the static default.
	if derived && ttl > qoderJobTokenRefreshSkew {
		ttl -= qoderJobTokenRefreshSkew
	}
	if ttl < qoderJobTokenMinTTL {
		ttl = qoderJobTokenMinTTL
	}
	return token, ttl
}

// --- Request body building --------------------------------------------------

// qoderPayload is the exact JSON shape Qoder's chat endpoint expects.
type qoderPayload struct {
	RequestID    string            `json:"request_id"`
	RequestSetID string            `json:"request_set_id"`
	ChatRecordID string            `json:"chat_record_id"`
	SessionID    string            `json:"session_id"`
	Stream       bool              `json:"stream"`
	ChatTask     string            `json:"chat_task"`
	IsReply      bool              `json:"is_reply"`
	IsRetry      bool              `json:"is_retry"`
	Source       int               `json:"source"`
	Version      string            `json:"version"`
	SessionType  string            `json:"session_type"`
	AgentID      string            `json:"agent_id"`
	TaskID       string            `json:"task_id"`
	CodeLanguage string            `json:"code_language"`
	ChatPrompt   string            `json:"chat_prompt"`
	ImageURLs    any               `json:"image_urls"`
	AliyunUser   string            `json:"aliyun_user_type"`
	System       string            `json:"system"`
	Messages     []qoderMessage    `json:"messages"`
	Tools        []json.RawMessage `json:"tools"`
	Parameters   qoderParams       `json:"parameters"`
	ChatContext  qoderChatContext  `json:"chat_context"`
	ModelConfig  json.RawMessage   `json:"model_config"`
	Business     qoderBusiness     `json:"business"`
}

type qoderMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []qoderToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type qoderToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function qoderToolFunction `json:"function"`
}

type qoderToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type qoderParams struct {
	MaxTokens int `json:"max_tokens"`
}

type qoderChatContext struct {
	ChatPrompt string            `json:"chatPrompt"`
	ImageURLs  any               `json:"imageUrls"`
	Extra      qoderChatExtra    `json:"extra"`
	Features   []json.RawMessage `json:"features"`
	Text       string            `json:"text"`
}

type qoderChatExtra struct {
	Context         []json.RawMessage `json:"context"`
	ModelConfig     qoderModelRef     `json:"modelConfig"`
	OriginalContent string            `json:"originalContent"`
}

type qoderModelRef struct {
	Key         string `json:"key"`
	IsReasoning bool   `json:"is_reasoning"`
}

type qoderBusiness struct {
	Product string `json:"product"`
	Version string `json:"version"`
	Type    string `json:"type"`
	Stage   string `json:"stage"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	BeginAt int64  `json:"begin_at"`
}

// normalizeQoderMessages hoists system instructions and renders canonical tool
// calls/results into the OpenAI-compatible message shape accepted by Qoder.
func normalizeQoderMessages(msgs []core.Message) (out []qoderMessage, systemText string) {
	var sysParts []string
	for _, m := range msgs {
		text := m.TextContent()
		if m.Role == core.RoleSystem || m.Role == core.RoleDeveloper {
			if text != "" {
				sysParts = append(sysParts, text)
			}
			continue
		}

		var calls []qoderToolCall
		var results []qoderMessage
		for _, part := range m.Content {
			switch part.Type {
			case core.PartToolCall:
				if m.Role != core.RoleAssistant || part.ToolCall == nil || part.ToolCall.ID == "" {
					continue
				}
				args := string(part.ToolCall.Arguments)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, qoderToolCall{
					ID:   part.ToolCall.ID,
					Type: "function",
					Function: qoderToolFunction{
						Name:      part.ToolCall.Name,
						Arguments: args,
					},
				})
			case core.PartToolResult:
				if part.ToolResult == nil || part.ToolResult.CallID == "" {
					continue
				}
				results = append(results, qoderMessage{
					Role:       string(core.RoleTool),
					Content:    part.ToolResult.Content,
					ToolCallID: part.ToolResult.CallID,
				})
			}
		}

		// Anthropic clients carry tool results and user text in one message.
		// Qoder expects the OpenAI layout: tool messages first, then user text.
		out = append(out, results...)

		switch m.Role {
		case core.RoleTool:
			// Tool content was emitted above with its matching tool_call_id.
			continue
		case core.RoleAssistant:
			out = append(out, qoderMessage{
				Role:      string(m.Role),
				Content:   text,
				Name:      m.Name,
				ToolCalls: calls,
			})
		default:
			if text != "" || len(results) == 0 {
				out = append(out, qoderMessage{
					Role:    string(m.Role),
					Content: text,
					Name:    m.Name,
				})
			}
		}
	}
	return repairQoderToolHistory(out), strings.Join(sysParts, "\n\n")
}

// repairQoderToolHistory moves matching tool results directly after the
// assistant call that created them, synthesizes an empty result when missing,
// and drops results that do not belong to an earlier call.
func repairQoderToolHistory(msgs []qoderMessage) []qoderMessage {
	type indexedResult struct {
		index int
		msg   qoderMessage
		used  bool
	}

	results := make(map[string][]*indexedResult)
	for i, msg := range msgs {
		if msg.Role != string(core.RoleTool) || msg.ToolCallID == "" {
			continue
		}
		results[msg.ToolCallID] = append(results[msg.ToolCallID], &indexedResult{
			index: i,
			msg:   msg,
		})
	}

	out := make([]qoderMessage, 0, len(msgs))
	for i, msg := range msgs {
		if msg.Role == string(core.RoleTool) {
			continue
		}
		out = append(out, msg)
		if msg.Role != string(core.RoleAssistant) {
			continue
		}

		for _, call := range msg.ToolCalls {
			var matched *indexedResult
			for _, candidate := range results[call.ID] {
				if !candidate.used && candidate.index > i {
					matched = candidate
					break
				}
			}
			if matched != nil {
				matched.used = true
				out = append(out, matched.msg)
				continue
			}
			out = append(out, qoderMessage{
				Role:       string(core.RoleTool),
				Content:    "",
				ToolCallID: call.ID,
			})
		}
	}

	return out
}

// lastUserText returns the text of the last user message (for chat_context).
func lastUserText(msgs []qoderMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// stableHash produces a deterministic short hex digest for session/record ids.
func stableHash(prefix string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(prefix))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// stableChatRecordID generates a deterministic id from the request content.
func stableChatRecordID(model string, msgs []qoderMessage, tools []json.RawMessage, maxTokens int) string {
	h := sha256.New()
	h.Write([]byte("qoder-record"))
	h.Write([]byte{0})
	h.Write([]byte(model))
	for _, m := range msgs {
		h.Write([]byte{0})
		if encoded, err := json.Marshal(m); err == nil {
			h.Write(encoded)
		}
	}
	if len(tools) > 0 {
		h.Write([]byte{0})
		for _, t := range tools {
			h.Write(t)
		}
	}
	h.Write([]byte(fmt.Sprintf("\x00mt=%d", maxTokens)))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// resolveModelKey strips a "qoder/" prefix from the model name.
func resolveModelKey(model string) string {
	return strings.TrimPrefix(model, "qoder/")
}

// resolveMaxTokens determines the output token limit from the request and
// model config, defaulting to 32768.
func resolveMaxTokens(req *core.ChatRequest, modelConfig json.RawMessage) int {
	maxTokens := 32768

	// Extract max_output_tokens from model config if available.
	if modelConfig != nil {
		var cfg struct {
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		if json.Unmarshal(modelConfig, &cfg) == nil && cfg.MaxOutputTokens > 0 {
			maxTokens = cfg.MaxOutputTokens
		}
	}

	if req.MaxTokens != nil && *req.MaxTokens > 0 && *req.MaxTokens < maxTokens {
		maxTokens = *req.MaxTokens
	}
	return maxTokens
}

// serializeTools converts core.Tool slice to Qoder's OpenAI-compatible tool
// format. Qoder validates that every tool has a function object, but also
// rejects malformed/empty parameter schemas.
func serializeTools(tools []core.Tool) []json.RawMessage {
	if len(tools) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(tools))
	for _, t := range tools {
		toolJSON := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  normalizeQoderToolSchema(t.Parameters),
			},
		}
		b, err := json.Marshal(toolJSON)
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeQoderToolSchema(raw json.RawMessage) json.RawMessage {
	defaultSchema := json.RawMessage(`{"type":"object","properties":{}}`)
	if len(raw) == 0 {
		return defaultSchema
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return defaultSchema
	}
	if schema["type"] == nil {
		schema["type"] = "object"
	}
	if schema["properties"] == nil {
		schema["properties"] = map[string]any{}
	}
	out, err := json.Marshal(schema)
	if err != nil {
		return defaultSchema
	}
	return out
}

// buildPayload constructs the Qoder chat request payload from a canonical
// ChatRequest and the cached model config.
func (c *Qoder) buildPayload(req *core.ChatRequest, modelKey string, modelConfig json.RawMessage, accountScope string) qoderPayload {
	msgs, systemText := normalizeQoderMessages(req.Messages)
	tools := serializeTools(req.Tools)
	maxTokens := resolveMaxTokens(req, modelConfig)
	lastUser := lastUserText(msgs)
	if req.System != "" {
		if systemText == "" {
			systemText = req.System
		} else {
			systemText = req.System + "\n\n" + systemText
		}
	}

	recordID := stableChatRecordID(modelKey, msgs, tools, maxTokens)
	sessionKey := req.Metadata.ContextAffinityKey
	if sessionKey == "" {
		sessionKey = req.Metadata.RequestID
	}
	if sessionKey == "" {
		sessionKey = recordID
	}
	sessionID := stableHash("qoder-session", accountScope, sessionKey, modelKey)

	// Determine is_reasoning from model_config.
	var isReasoning bool
	if modelConfig != nil {
		var cfg struct {
			IsReasoning bool `json:"is_reasoning"`
		}
		_ = json.Unmarshal(modelConfig, &cfg)
		isReasoning = cfg.IsReasoning
	}

	// Default model_config when the live catalog hasn't been fetched yet.
	mc := modelConfig
	if mc == nil {
		mc, _ = json.Marshal(map[string]any{
			"key":          modelKey,
			"is_reasoning": isReasoning,
		})
	}

	return qoderPayload{
		RequestID:    uuid.NewString(),
		RequestSetID: recordID,
		ChatRecordID: recordID,
		SessionID:    sessionID,
		Stream:       true,
		ChatTask:     "FREE_INPUT",
		IsReply:      true,
		IsRetry:      false,
		Source:       1,
		Version:      "3",
		SessionType:  "qodercli",
		AgentID:      "agent_common",
		TaskID:       "common",
		CodeLanguage: "",
		ChatPrompt:   "",
		ImageURLs:    nil,
		AliyunUser:   "",
		System:       systemText,
		Messages:     msgs,
		Tools:        tools,
		Parameters:   qoderParams{MaxTokens: maxTokens},
		ChatContext: qoderChatContext{
			ChatPrompt: "",
			ImageURLs:  nil,
			Extra: qoderChatExtra{
				Context:         []json.RawMessage{},
				ModelConfig:     qoderModelRef{Key: modelKey, IsReasoning: isReasoning},
				OriginalContent: lastUser,
			},
			Features: []json.RawMessage{},
			Text:     lastUser,
		},
		ModelConfig: mc,
		Business: qoderBusiness{
			Product: "cli",
			Version: "1.0.0",
			Type:    "agent",
			Stage:   "start",
			ID:      uuid.NewString(),
			Name:    truncate(lastUser, 30),
			BeginAt: time.Now().UnixMilli(),
		},
	}
}

// --- Model catalog ----------------------------------------------------------

// fetchModelCatalog fetches the live model list from api3.qoder.sh and caches
// the raw model_config blocks by key. This is required because Qoder silently
// downgrades to a different model when the wrong model_config is sent.
func (c *Qoder) fetchModelCatalog(ctx context.Context, cc qoderlib.CosyCreds) (map[string]json.RawMessage, error) {
	cacheKey := cc.UserID

	// Check cache first.
	c.mu.RLock()
	entry, ok := c.catalog[cacheKey]
	c.mu.RUnlock()
	if ok && time.Since(entry.fetchedAt) < qoderCatalogTTL {
		return entry.rawConfigs, nil
	}

	// Fetch fresh catalog.
	cosyHeaders, err := qoderlib.BuildCosyHeaders(nil, qoderlib.ModelListURL, cc)
	if err != nil {
		return nil, fmt.Errorf("qoder: build model list headers: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qoderlib.ModelListURL, nil)
	if err != nil {
		return nil, fmt.Errorf("qoder: build model list request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	for k, v := range cosyHeaders {
		req.Header.Set(k, v)
	}

	resp, err := proxyClient(ctx).Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: model list request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("qoder: model list returned %d: %s", resp.StatusCode, truncateError(body))
	}

	var catalog struct {
		Chat []json.RawMessage `json:"chat"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("qoder: parse model list: %w", err)
	}

	configs := make(map[string]json.RawMessage, len(catalog.Chat))
	for _, entry := range catalog.Chat {
		var key struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(entry, &key) != nil || key.Key == "" {
			continue
		}
		configs[key.Key] = entry
	}

	// Update cache.
	c.mu.Lock()
	c.catalog[cacheKey] = &qoderCatalogEntry{
		fetchedAt:  time.Now(),
		rawConfigs: configs,
	}
	c.mu.Unlock()

	return configs, nil
}

// modelConfigForKey resolves the model_config block for a given model key,
// fetching the catalog if needed.
func (c *Qoder) modelConfigForKey(ctx context.Context, cc qoderlib.CosyCreds, modelKey string) (json.RawMessage, error) {
	configs, err := c.fetchModelCatalog(ctx, cc)
	if err != nil {
		return nil, err
	}
	cfg, ok := configs[modelKey]
	if !ok {
		return nil, fmt.Errorf("qoder: model_config for %q not found in live catalog", modelKey)
	}
	return cfg, nil
}

// --- Chat / Stream ----------------------------------------------------------

// signedRequest builds the COSY-signed, WAF-encoded request for the Qoder chat
// endpoint. Returns the URL, headers, and encoded body ready for sending.
func (c *Qoder) signedRequest(payload qoderPayload, cc qoderlib.CosyCreds) (url string, headers map[string]string, body []byte, err error) {
	url = qoderlib.ChatURLEncoded

	plainBody, err := json.Marshal(payload)
	if err != nil {
		return "", nil, nil, fmt.Errorf("qoder: marshal payload: %w", err)
	}

	// Apply WAF-bypass encoding.
	encodedBody := qoderlib.EncodeBody(plainBody)

	// Build COSY headers over the encoded body bytes.
	cosyHeaders, err := qoderlib.BuildCosyHeaders(encodedBody, url, cc)
	if err != nil {
		return "", nil, nil, fmt.Errorf("qoder: COSY signing: %w", err)
	}

	headers = map[string]string{
		"Content-Type":    "application/json",
		"Accept":          "text/event-stream",
		"Cache-Control":   "no-cache",
		"Accept-Encoding": "identity", // gzip breaks CDN signature validation
	}
	for k, v := range cosyHeaders {
		headers[k] = v
	}

	return url, headers, encodedBody, nil
}

// Stream opens a COSY-signed SSE connection and unwraps Qoder's
// {statusCodeValue, body} envelope into canonical OpenAI chunks.
func (c *Qoder) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	if err := c.validateCreds(creds); err != nil {
		return nil, err
	}

	cc, err := c.resolveCosyCreds(ctx, creds)
	if err != nil {
		return nil, err
	}

	modelKey := resolveModelKey(req.Model)
	modelConfig, err := c.modelConfigForKey(ctx, cc, modelKey)
	if err != nil {
		// Non-fatal: use a minimal model_config and let upstream decide.
		modelConfig = nil
	}

	payload := c.buildPayload(req, modelKey, modelConfig, cc.UserID)
	payload.Stream = true

	url, headers, body, err := c.signedRequest(payload, cc)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}

	resp, err := openStream(ctx, c.id, req.Model, url, body, headers)
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	// Raw lines: Qoder wraps its payload in a provider envelope that the
	// unwrapper parses (including the "data:" prefix and in-band errors).
	go streamSSE(ctx, c.id, req.Model, resp.Body, out, cfg, ndjsonLines, func(line []byte) ([]core.StreamChunk, error) {
		inner, ok, qerr := unwrapQoderSSELineWithError(string(line), c.id, req.Model)
		if qerr != nil {
			return []core.StreamChunk{{Type: core.ChunkError, Err: qerr}}, errStopStream
		}
		if !ok {
			return nil, nil
		}
		if inner == "[DONE]" {
			return nil, errStopStream
		}
		// Sanitize embedded newlines so the SSE frame is a single event.
		inner = strings.ReplaceAll(inner, "\n", "")
		inner = strings.ReplaceAll(inner, "\r", "")
		return c.codec.ParseStreamLine([]byte(inner), req.Model)
	})
	return out, nil
}

// Chat performs a non-streaming completion by collecting the streaming
// response. Qoder's chat endpoint is SSE-only.
func (c *Qoder) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	streamReq := *req
	streamReq.Stream = true // Qoder is SSE-only

	chunks, err := c.Stream(ctx, &streamReq, creds, core.StreamConfig{})
	if err != nil {
		return nil, err
	}

	msg, finishReason, usage, err := collectQoderChunks(chunks)
	if err != nil {
		return nil, err
	}

	return &core.ChatResponse{
		ID:           uuid.NewString(),
		Model:        req.Model,
		Message:      msg,
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}

func collectQoderChunks(chunks <-chan core.StreamChunk) (core.Message, core.FinishReason, core.Usage, error) {
	var (
		textBuf      strings.Builder
		thinkingBuf  strings.Builder
		toolCalls    []*core.ToolCall
		finishReason core.FinishReason
		usage        core.Usage
		streamErr    error
	)

	collect := func(ch core.StreamChunk) {
		switch ch.Type {
		case core.ChunkText:
			textBuf.WriteString(ch.Delta)
		case core.ChunkThinking:
			thinkingBuf.WriteString(ch.Delta)
		case core.ChunkToolCall:
			if ch.ToolCall != nil {
				toolCalls = append(toolCalls, ch.ToolCall)
			}
		case core.ChunkFinish:
			finishReason = ch.FinishReason
		case core.ChunkUsage:
			if ch.Usage != nil {
				usage = *ch.Usage
			}
		case core.ChunkError:
			if ch.Err != nil {
				streamErr = ch.Err
			}
		}
	}

	sanitizer := transform.NewToolArgSanitizer()
	for ch := range chunks {
		sanitizer.Process(ch, collect)
		if streamErr != nil {
			return core.Message{}, "", core.Usage{}, streamErr
		}
	}
	sanitizer.Flush(collect)
	if streamErr != nil {
		return core.Message{}, "", core.Usage{}, streamErr
	}

	msg := core.Message{
		Role: core.RoleAssistant,
	}
	if text := textBuf.String(); text != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: text})
	}
	if thinking := thinkingBuf.String(); thinking != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: thinking})
	}
	for _, tc := range toolCalls {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: tc})
	}

	return msg, finishReason, usage, nil
}

// Validate probes the Qoder model list endpoint to confirm the COSY signing
// and credentials are accepted.
func (c *Qoder) Validate(ctx context.Context, creds core.Credentials) error {
	if err := c.validateCreds(creds); err != nil {
		return err
	}
	cc, err := c.resolveCosyCreds(ctx, creds)
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	if _, err := c.fetchModelCatalog(ctx, cc); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}

// --- SSE envelope unwrapping ------------------------------------------------

// qoderSSEEnvelope is the wrapper Qoder puts around each OpenAI chunk.
type qoderSSEEnvelope struct {
	StatusCodeValue int    `json:"statusCodeValue"`
	Body            string `json:"body"`
}

// unwrapQoderSSELine extracts the inner OpenAI JSON from a Qoder SSE line.
// Returns ("", false) for non-data lines or empty payloads.
// Returns ("[DONE]", true) when the stream ends.
func unwrapQoderSSELine(line string) (string, bool) {
	inner, ok, _ := unwrapQoderSSELineWithError(line, "", "")
	return inner, ok
}

func unwrapQoderSSELineWithError(line, provider, model string) (string, bool, error) {
	line = strings.TrimRight(line, "\r")
	if !strings.HasPrefix(line, "data:") {
		return "", false, nil
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" {
		return "", false, nil
	}
	if data == "[DONE]" {
		return "[DONE]", true, nil
	}

	var env qoderSSEEnvelope
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return "", false, nil
	}
	if env.StatusCodeValue != 0 && env.StatusCodeValue != 200 {
		kind := core.ErrUpstream
		scope := core.FailureScopeProvider
		switch {
		case isQoderQuotaBlock(env.Body):
			kind = core.ErrQuotaExhausted
			scope = core.FailureScopeAccount
		case isQoderQueueThrottle(env.Body):
			kind = core.ErrRateLimit
			scope = core.FailureScopeAccount
		case env.StatusCodeValue == http.StatusUnauthorized || env.StatusCodeValue == http.StatusForbidden:
			kind = core.ErrAuth
			scope = core.FailureScopeAccount
		case env.StatusCodeValue == http.StatusTooManyRequests:
			kind = core.ErrRateLimit
			scope = core.FailureScopeAccount
		case env.StatusCodeValue >= 400 && env.StatusCodeValue < 500:
			kind = core.ErrBadRequest
			scope = core.FailureScopeRequest
		}
		return "", false, &core.ProviderError{
			Kind:       kind,
			Scope:      scope,
			Provider:   provider,
			Model:      model,
			StatusCode: env.StatusCodeValue,
			Message:    truncateError([]byte(env.Body)),
		}
	}
	if env.Body == "" || env.Body == "[DONE]" {
		if env.Body == "[DONE]" {
			return "[DONE]", true, nil
		}
		return "", false, nil
	}
	return env.Body, true, nil
}

func isQoderQuotaBlock(body string) bool {
	lower := strings.ToLower(body)
	return qoderErrorCode(body, "112") || strings.Contains(lower, "pricingurl")
}

func isQoderQueueThrottle(body string) bool {
	return qoderErrorCode(body, "10605")
}

func qoderErrorCode(body, code string) bool {
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(body)
	return strings.Contains(compact, `"code":"`+code+`"`) ||
		strings.Contains(compact, `\"code\":\"`+code+`\"`)
}

// ListModels implements live model discovery by fetching the COSY-signed
// model list endpoint.
func (c *Qoder) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	if err := c.validateCreds(creds); err != nil {
		return nil, err
	}

	cc, err := c.resolveCosyCreds(ctx, creds)
	if err != nil {
		return nil, err
	}

	configs, err := c.fetchModelCatalog(ctx, cc)
	if err != nil {
		return nil, err
	}

	out := make([]ModelSpec, 0, len(configs))
	for key, raw := range configs {
		var entry struct {
			Enable         *bool  `json:"enable"`
			DisplayName    string `json:"display_name"`
			MaxInputTokens int    `json:"max_input_tokens"`
		}
		_ = json.Unmarshal(raw, &entry)

		// Include all models (even disabled ones — upstream still accepts them).
		name := entry.DisplayName
		if name == "" {
			name = key
		}
		out = append(out, ModelSpec{
			ID:   key,
			Name: name,
			Kind: core.ServiceLLM,
		})
	}
	return out, nil
}

// --- Helpers ----------------------------------------------------------------

// Compile-time interface checks.
var (
	_ core.Connector = (*Qoder)(nil)
	_ core.Validator = (*Qoder)(nil)
)

// QoderModelSource implements LiveModelSource for Qoder's COSY-signed model
// list endpoint.
type QoderModelSource struct {
	connector *Qoder
}

// NewQoderModelSource builds a live model source backed by the Qoder connector.
func NewQoderModelSource(conn *Qoder) *QoderModelSource {
	return &QoderModelSource{connector: conn}
}

// ListModels delegates to the connector's ListModels.
func (s *QoderModelSource) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	return s.connector.ListModels(ctx, creds)
}
