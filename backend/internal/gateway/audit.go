package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mydisha/keirouter/backend/internal/audit"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// auditedReads are GET routes that hand out secrets or bulk data, so reading
// them is itself an auditable action.
var auditedReads = map[string]bool{
	"/api/settings/database":      true, // full config export, credentials included
	"/api/settings/sqlite/backup": true,
	"/api/guardrails/export":      true,
	"/api/audit/export":           true,
}

// auditBodyLimit caps how much of a JSON request body is buffered to list
// its field names.
const auditBodyLimit = 64 << 10

// auditAdmin records every mutating dashboard request (and the reads in
// auditedReads) once the handler has run, with the route, outcome and the
// names — never the values — of the JSON fields it sent.
func (s *Server) auditAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.audit == nil || r.Method == http.MethodOptions ||
			(r.Method == http.MethodGet && !auditedReads[r.URL.Path]) {
			next.ServeHTTP(w, r)
			return
		}
		fields := requestFieldNames(r)
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		// A create returns the new resource; keep the start of the response
		// to read its id.
		var head *cappedBuffer
		if r.Method == http.MethodPost {
			head = &cappedBuffer{limit: 4096}
			ww.Tee(head)
		}
		next.ServeHTTP(ww, r)

		pattern := r.URL.Path
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			pattern = rc.RoutePattern()
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		ev := s.auditEvent(r)
		ev.Action, ev.ResourceType = audit.ActionForRoute(r.Method, pattern)
		ev.Route = pattern
		ev.StatusCode = status
		ev.Outcome = audit.OutcomeForStatus(status)
		ev.ResourceID = routeResourceID(r)
		if ev.ResourceID == "" && head != nil && status < 300 {
			ev.ResourceID = responseID(head.Bytes())
		}
		if fields != "" {
			ev.Details["fields"] = fields
		}
		s.audit.Record(ev)
	})
}

// auditEvent fills the request-derived fields of an event.
func (s *Server) auditEvent(r *http.Request) audit.Event {
	ev := audit.Event{
		TenantID:  adminTenant,
		ActorType: audit.ActorDashboard,
		ClientIP:  extractIP(r),
		UserAgent: r.UserAgent(),
		Method:    r.Method,
		Route:     r.URL.Path,
		RequestID: middleware.GetReqID(r.Context()),
		Details:   map[string]string{},
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		ev.ActorID = sessionActor(c.Value)
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ev.Details["forwarded_for"] = truncateString(fwd, 256)
	}
	ev.TraceID, ev.SpanID = audit.ParseTraceparent(r.Header.Get("traceparent"))
	return ev
}

// sessionActor identifies a dashboard session without storing its token: the
// same session always maps to the same id, so one operator's actions can be
// followed across the log.
func sessionActor(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "session:" + hex.EncodeToString(sum[:6])
}

// recordLogin audits a login attempt. A success is attributed to the session
// it created.
func (s *Server) recordLogin(r *http.Request, status int, token string) {
	if s.audit == nil {
		return
	}
	ev := s.auditEvent(r)
	ev.Action, ev.ResourceType = "auth.login", "auth"
	ev.StatusCode = status
	ev.Outcome = audit.OutcomeForStatus(status)
	// Any cookie sent with a login attempt is unauthenticated (and cookies
	// are not port-scoped, so it may belong to another instance on the same
	// host). Only a successful login names a session: the one it created.
	ev.ActorID = ""
	if token != "" {
		ev.ActorID = sessionActor(token)
	}
	s.audit.Record(ev)
}

// recordLogout audits a logout of the session in the request cookie.
func (s *Server) recordLogout(r *http.Request) {
	if s.audit == nil {
		return
	}
	ev := s.auditEvent(r)
	ev.Action, ev.ResourceType = "auth.logout", "auth"
	ev.StatusCode = http.StatusOK
	ev.Outcome = audit.OutcomeSuccess
	s.audit.Record(ev)
}

// requestFieldNames lists the top-level keys of a small JSON request body and
// restores the body for the handler.
func requestFieldNames(r *http.Request) string {
	if r.Body == nil || r.ContentLength == 0 || r.ContentLength > auditBodyLimit ||
		!strings.Contains(r.Header.Get("Content-Type"), "json") {
		return ""
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, auditBodyLimit+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(buf), r.Body), r.Body}
	if err != nil || len(buf) > auditBodyLimit {
		return ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(buf, &obj) != nil || len(obj) == 0 {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return truncateString(strings.Join(keys, ","), 512)
}

// routeResourceID is the {id} URL parameter, else the first one.
func routeResourceID(r *http.Request) string {
	rc := chi.RouteContext(r.Context())
	if rc == nil {
		return ""
	}
	if id := rc.URLParam("id"); id != "" {
		return id
	}
	for i, k := range rc.URLParams.Keys {
		if k != "*" && i < len(rc.URLParams.Values) && rc.URLParams.Values[i] != "" {
			return rc.URLParams.Values[i]
		}
	}
	return ""
}

// responseID reads a top-level "id" (or nested key/account/... "id") from the
// start of a JSON response.
func responseID(b []byte) string {
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return ""
	}
	if id := rawString(top["id"]); id != "" {
		return id
	}
	for _, k := range []string{"key", "account", "chain", "plan", "budget", "pool", "policy", "provider"} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(top[k], &nested) == nil {
			if id := rawString(nested["id"]); id != "" {
				return id
			}
		}
	}
	return ""
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// cappedBuffer keeps the first limit bytes written to it and discards the
// rest, so teeing a large response costs at most limit bytes.
type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.Len(); room > 0 {
		if len(p) > room {
			c.Buffer.Write(p[:room])
		} else {
			c.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// ---- admin endpoints --------------------------------------------------------

func (s *Server) mountAudit(r chi.Router) {
	r.Get("/audit", s.adminListAudit)
	r.Get("/audit/export", s.adminExportAudit)
	r.Get("/audit/verify", s.adminVerifyAudit)
	r.Get("/audit/status", s.adminAuditStatus)
}

// auditEntryJSON is the API shape of an audit row.
type auditEntryJSON struct {
	ID           string            `json:"id"`
	Time         time.Time         `json:"time"`
	Action       string            `json:"action"`
	Outcome      string            `json:"outcome"`
	Severity     string            `json:"severity"`
	ActorType    string            `json:"actor_type"`
	ActorID      string            `json:"actor_id"`
	ClientIP     string            `json:"client_ip"`
	UserAgent    string            `json:"user_agent"`
	Method       string            `json:"method"`
	Route        string            `json:"route"`
	StatusCode   int               `json:"status_code"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	RequestID    string            `json:"request_id"`
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	Details      map[string]string `json:"details"`
	ChainID      string            `json:"chain_id"`
	ChainSeq     int64             `json:"chain_seq"`
	Hash         string            `json:"hash"`
}

func toAuditJSON(e store.AuditEntry) auditEntryJSON {
	details := map[string]string{}
	if e.Detail != "" {
		_ = json.Unmarshal([]byte(e.Detail), &details)
	}
	return auditEntryJSON{
		ID: e.ID, Time: e.CreatedAt, Action: e.Action, Outcome: e.Outcome, Severity: e.Severity,
		ActorType: e.ActorType, ActorID: e.Actor, ClientIP: e.ClientIP, UserAgent: e.UserAgent,
		Method: e.HTTPMethod, Route: e.HTTPRoute, StatusCode: e.StatusCode,
		ResourceType: e.ResourceType, ResourceID: e.Target, RequestID: e.RequestID,
		TraceID: e.TraceID, SpanID: e.SpanID, Details: details,
		ChainID: e.ChainID, ChainSeq: e.ChainSeq, Hash: e.Hash,
	}
}

// auditFilter parses the list/export query string. ok is false after an
// error response has been written.
func auditFilter(w http.ResponseWriter, r *http.Request) (store.AuditFilter, bool) {
	q := r.URL.Query()
	f := store.AuditFilter{
		Action:       q.Get("action"),
		Actor:        q.Get("actor"),
		Outcome:      q.Get("outcome"),
		ResourceType: q.Get("resource_type"),
		Target:       q.Get("resource_id"),
		Query:        strings.TrimSpace(q.Get("q")),
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, http.StatusBadRequest, p.name+" must be an RFC 3339 timestamp")
				return f, false
			}
			*p.dst = t
		}
	}
	if v := q.Get("cursor"); v != "" {
		ts, id, ok := strings.Cut(v, "|")
		t, err := time.Parse(time.RFC3339Nano, ts)
		if !ok || err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return f, false
		}
		f.BeforeTime, f.BeforeID = t, id
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return f, false
		}
		f.Limit = min(n, 500)
	}
	return f, true
}

func (s *Server) adminListAudit(w http.ResponseWriter, r *http.Request) {
	f, ok := auditFilter(w, r)
	if !ok {
		return
	}
	if f.Limit == 0 {
		f.Limit = 100
	}
	rows, err := s.db.Audit().List(r.Context(), adminTenant, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "failed to list audit log"))
		return
	}
	out := make([]auditEntryJSON, 0, len(rows))
	for _, e := range rows {
		out = append(out, toAuditJSON(e))
	}
	resp := map[string]any{"entries": out}
	if len(rows) == f.Limit {
		last := rows[len(rows)-1]
		resp["next_cursor"] = last.CreatedAt.Format(time.RFC3339Nano) + "|" + last.ID
	}
	writeJSON(w, http.StatusOK, resp)
}

// auditExportMax caps one export so a single request cannot stream an
// unbounded table; narrow the window with since/until for more.
const auditExportMax = 200_000

// adminExportAudit streams matching rows as JSON Lines, newest first — the
// format SIEM bulk importers (Splunk, Elastic, Loki) accept directly.
func (s *Server) adminExportAudit(w http.ResponseWriter, r *http.Request) {
	f, ok := auditFilter(w, r)
	if !ok {
		return
	}
	f.Limit = 1000
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="keirouter-audit-`+time.Now().UTC().Format("20060102-150405")+`.jsonl"`)
	bw := bufio.NewWriterSize(w, 64<<10)
	enc := json.NewEncoder(bw)
	written := 0
	for written < auditExportMax {
		rows, err := s.db.Audit().List(r.Context(), adminTenant, f)
		if err != nil {
			if written == 0 {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "failed to export audit log"))
			}
			s.log.Warn("audit export aborted", "err", err, "written", written)
			break
		}
		for _, e := range rows {
			_ = enc.Encode(toAuditJSON(e))
		}
		written += len(rows)
		if len(rows) < f.Limit {
			break
		}
		last := rows[len(rows)-1]
		f.BeforeTime, f.BeforeID = last.CreatedAt, last.ID
	}
	_ = bw.Flush()
}

func (s *Server) adminVerifyAudit(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	// Make this instance's queued rows part of the check.
	s.audit.Flush(ctx)
	rep, err := audit.Verify(ctx, s.db.Audit(), s.audit.Key())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "audit verification failed"))
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) adminAuditStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"stats":          s.audit.Stats(),
		"retention_days": s.cfg.Audit.RetentionDays,
	})
}
