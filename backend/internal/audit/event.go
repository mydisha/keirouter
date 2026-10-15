// Package audit records who changed what in KeiRouter: dashboard logins and
// every administrative mutation (keys, accounts, chains, budgets, settings,
// backups ...).
//
// Events are queued without blocking the admin request, batch-written to the
// audit_log table, and — when an OTLP endpoint is configured — exported as
// OpenTelemetry log records over OTLP/HTTP, so any OTel collector, Grafana
// Loki, Datadog, Elastic or Splunk pipeline can ingest them unchanged.
//
// Each writer process keeps an HMAC hash chain over the rows it writes, which
// makes the stored log tamper-evident: Verify detects a row that was edited,
// inserted or deleted after the fact.
package audit

import (
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// Outcome values.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeDenied  = "denied"
)

// Actor types.
const (
	ActorDashboard = "dashboard"
	ActorAPIKey    = "api_key"
	ActorSystem    = "system"
)

// Severity texts, matching OpenTelemetry's severity names.
const (
	SeverityInfo  = "INFO"
	SeverityWarn  = "WARN"
	SeverityError = "ERROR"
)

// Event is one auditable action. Only Action is required; the recorder fills
// the time, id and severity.
type Event struct {
	Time         time.Time
	TenantID     string
	Action       string
	Outcome      string
	Severity     string
	ActorType    string
	ActorID      string
	ClientIP     string
	UserAgent    string
	Method       string
	Route        string
	StatusCode   int
	ResourceType string
	ResourceID   string
	RequestID    string
	TraceID      string
	SpanID       string
	// Details are extra attributes. Never put secrets or request bodies here:
	// they are stored and exported verbatim.
	Details map[string]string
}

// OutcomeForStatus maps an HTTP status to an outcome.
func OutcomeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests:
		return OutcomeDenied
	case status >= 400 || status == 0:
		return OutcomeFailure
	default:
		return OutcomeSuccess
	}
}

func severityFor(outcome string) string {
	switch outcome {
	case OutcomeFailure, OutcomeDenied:
		return SeverityWarn
	default:
		return SeverityInfo
	}
}

// ActionForRoute names an admin request after its chi route pattern:
//
//	POST   /api/keys                  -> keys.create
//	PATCH  /api/keys/{id}             -> keys.update
//	DELETE /api/keys/{id}             -> keys.delete
//	POST   /api/accounts/{id}/test    -> accounts.test
//	POST   /api/settings/endpoint     -> settings.endpoint
//	PUT    /api/models/alias          -> models.alias.update
//	GET    /api/settings/sqlite/backup -> settings.sqlite.backup.read
//
// A POST to a sub-path is an RPC-style action and keeps the path as its verb;
// other methods on a sub-path get a CRUD verb suffix. resourceType is the
// first literal segment.
func ActionForRoute(method, pattern string) (action, resourceType string) {
	pattern = strings.TrimPrefix(pattern, "/api")
	var lits []string
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "" || seg == "*" || strings.HasPrefix(seg, "{") {
			continue
		}
		lits = append(lits, seg)
	}
	if len(lits) == 0 {
		return strings.ToLower(method), ""
	}
	verb := crudVerb(method)
	resourceType = lits[0]
	switch {
	case len(lits) == 1:
		return lits[0] + "." + verb, resourceType
	case method == http.MethodPost:
		return strings.Join(lits, "."), resourceType
	default:
		return strings.Join(lits, ".") + "." + verb, resourceType
	}
}

func crudVerb(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	case http.MethodGet, http.MethodHead:
		return "read"
	default:
		return strings.ToLower(method)
	}
}

// ParseTraceparent extracts the trace and parent span ids from a W3C
// traceparent header ("00-<32 hex>-<16 hex>-<2 hex>"), so an audit record
// joins the caller's distributed trace. Invalid or all-zero ids yield "".
func ParseTraceparent(h string) (traceID, spanID string) {
	parts := strings.Split(strings.TrimSpace(h), "-")
	if len(parts) < 4 || len(parts[0]) != 2 || parts[0] == "ff" {
		return "", ""
	}
	if !validHexID(parts[1], 32) || !validHexID(parts[2], 16) {
		return "", ""
	}
	return strings.ToLower(parts[1]), strings.ToLower(parts[2])
}

func validHexID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return false
	}
	for _, c := range b {
		if c != 0 {
			return true
		}
	}
	return false
}
