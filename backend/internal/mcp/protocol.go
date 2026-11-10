// Package mcp implements KeiRouter's MCP gateway: a registry of upstream MCP
// servers, a client that speaks both protocol eras to them, catalog sync, and
// the presets offered in the dashboard.
//
// The gateway proxies; it never re-encodes tool arguments or results. Only the
// JSON-RPC envelope, `_meta`, and the HTTP headers the spec mirrors from the
// body are read or rewritten.
package mcp

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Protocol versions. Modern versions carry version and capabilities in each
// request's `_meta`; legacy versions negotiate them once with `initialize`.
const (
	VersionModern       = "2026-07-28"
	VersionLegacyLatest = "2025-11-25"
)

// LegacyVersions are the initialize-era versions the gateway accepts from
// clients and upstreams, newest first.
var LegacyVersions = []string{VersionLegacyLatest, "2025-06-18", "2025-03-26"}

// SupportedVersions lists every version the gateway speaks, newest first.
var SupportedVersions = append([]string{VersionModern}, LegacyVersions...)

// IsModern reports whether v is a per-request-metadata protocol version.
func IsModern(v string) bool { return v == VersionModern }

// IsLegacy reports whether v is a supported initialize-era version.
func IsLegacy(v string) bool {
	for _, l := range LegacyVersions {
		if l == v {
			return true
		}
	}
	return false
}

// Request `_meta` keys reserved by the protocol.
const (
	MetaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	MetaClientInfo         = "io.modelcontextprotocol/clientInfo"
	MetaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	MetaServerInfo         = "io.modelcontextprotocol/serverInfo"
	MetaLogLevel           = "io.modelcontextprotocol/logLevel"
	reservedMetaPrefix     = "io.modelcontextprotocol/"
)

// HTTP headers defined by the Streamable HTTP transport.
const (
	HeaderProtocolVersion = "MCP-Protocol-Version"
	HeaderMethod          = "Mcp-Method"
	HeaderName            = "Mcp-Name"
	HeaderSessionID       = "Mcp-Session-Id"
	HeaderParamPrefix     = "Mcp-Param-"
)

// JSON-RPC and MCP error codes.
const (
	CodeParseError                 = -32700
	CodeInvalidRequest             = -32600
	CodeMethodNotFound             = -32601
	CodeInvalidParams              = -32602
	CodeInternalError              = -32603
	CodeHeaderMismatch             = -32020
	CodeMissingClientCapability    = -32021
	CodeUnsupportedProtocolVersion = -32022
)

// IsModernErrorCode reports whether code is one only a modern server returns.
// A client uses it to tell a modern server that rejected a request from a
// legacy server that does not understand it.
func IsModernErrorCode(code int) bool {
	return code == CodeHeaderMismatch || code == CodeMissingClientCapability || code == CodeUnsupportedProtocolVersion
}

// Request is a JSON-RPC request or notification. Params stays raw.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request has no id.
func (r *Request) IsNotification() bool { return len(r.ID) == 0 || string(r.ID) == "null" }

// Response is a JSON-RPC response. Result stays raw.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// Implementation identifies a client or server in `clientInfo`/`serverInfo`.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// unsupportedVersionData is the `data` of an UnsupportedProtocolVersion error.
type unsupportedVersionData struct {
	Supported []string `json:"supported"`
	Requested string   `json:"requested,omitempty"`
}

// base64 sentinel used for header values that are not plain ASCII.
const (
	b64Prefix = "=?base64?"
	b64Suffix = "?="
)

// EncodeHeaderValue renders s as a header value, using the base64 sentinel
// when s is not safe as a plain value or could be mistaken for the sentinel.
func EncodeHeaderValue(s string) string {
	if headerSafe(s) && !(strings.HasPrefix(s, b64Prefix) && strings.HasSuffix(s, b64Suffix)) {
		return s
	}
	return b64Prefix + base64.StdEncoding.EncodeToString([]byte(s)) + b64Suffix
}

// DecodeHeaderValue reverses EncodeHeaderValue. ok is false when the value
// uses the sentinel but is not valid base64 or UTF-8, or contains characters
// a plain header value may not carry.
func DecodeHeaderValue(v string) (string, bool) {
	if strings.HasPrefix(v, b64Prefix) && strings.HasSuffix(v, b64Suffix) && len(v) >= len(b64Prefix)+len(b64Suffix) {
		raw, err := base64.StdEncoding.DecodeString(v[len(b64Prefix) : len(v)-len(b64Suffix)])
		if err != nil || !utf8.Valid(raw) {
			return "", false
		}
		return string(raw), true
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\t' && (c < 0x20 || c > 0x7e) {
			return "", false
		}
	}
	return v, true
}

// headerSafe reports whether s can be sent verbatim: visible ASCII, space or
// tab, and no leading or trailing whitespace.
func headerSafe(s string) bool {
	if s == "" {
		return true
	}
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\t' && (c < 0x20 || c > 0x7e) {
			return false
		}
	}
	return true
}

// IsToken reports whether s is an RFC 9110 field-name token (1*tchar).
func IsToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTchar(s[i]) {
			return false
		}
	}
	return true
}

func isTchar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}
