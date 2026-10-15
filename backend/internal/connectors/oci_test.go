package connectors

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

var (
	ociTestKeyOnce sync.Once
	ociTestKey     *rsa.PrivateKey
)

func ociKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	ociTestKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		ociTestKey = k
	})
	return ociTestKey
}

func ociPKCS8PEM(t *testing.T) string {
	der, err := x509.MarshalPKCS8PrivateKey(ociKey(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func ociCreds(t *testing.T, base string) core.Credentials {
	return core.Credentials{
		BaseURL: base,
		Extra: map[string]string{
			"oci_region":         "us-chicago-1",
			"oci_tenancy":        "ocid1.tenancy.oc1..tenancy",
			"oci_user":           "ocid1.user.oc1..user",
			"oci_fingerprint":    "aa:bb:cc",
			"oci_key":            ociPKCS8PEM(t),
			"oci_compartment_id": "ocid1.compartment.oc1..comp",
		},
	}
}

const ociTestKeyID = "ocid1.tenancy.oc1..tenancy/ocid1.user.oc1..user/aa:bb:cc"

// ociVerify independently re-derives the signing string from the request as
// the server received it and checks the RSA-SHA256 signature.
func ociVerify(r *http.Request, body []byte, pub *rsa.PublicKey) error {
	auth := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(auth, "Signature ")
	if !ok {
		return fmt.Errorf("authorization is not a Signature: %q", auth)
	}
	params := map[string]string{}
	for _, kv := range strings.Split(rest, ",") {
		k, v, _ := strings.Cut(kv, "=")
		params[k] = strings.Trim(v, `"`)
	}
	if params["version"] != "1" || params["algorithm"] != "rsa-sha256" || params["keyId"] != ociTestKeyID {
		return fmt.Errorf("bad signature params: %v", params)
	}
	names := strings.Fields(params["headers"])
	var lines []string
	for _, n := range names {
		var v string
		switch n {
		case "(request-target)":
			v = strings.ToLower(r.Method) + " " + r.RequestURI
		case "host":
			v = r.Host
		default:
			v = r.Header.Get(n)
		}
		lines = append(lines, n+": "+v)
	}
	want := []string{"date", "(request-target)", "host"}
	if r.Method == http.MethodPost {
		want = append(want, "content-length", "content-type", "x-content-sha256")
		sum := sha256.Sum256(body)
		if got := r.Header.Get("x-content-sha256"); got != base64.StdEncoding.EncodeToString(sum[:]) {
			return fmt.Errorf("x-content-sha256 mismatch")
		}
		if r.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
			return fmt.Errorf("content-length mismatch: %q vs %d", r.Header.Get("Content-Length"), len(body))
		}
	}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		return fmt.Errorf("signed headers = %v, want %v", names, want)
	}
	date, err := http.ParseTime(r.Header.Get("Date"))
	if err != nil || time.Since(date).Abs() > 5*time.Minute {
		return fmt.Errorf("bad date header %q", r.Header.Get("Date"))
	}
	sig, err := base64.StdEncoding.DecodeString(params["signature"])
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig)
}

// ociServer answers signed requests: handler gets the decoded body.
func ociServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body []byte)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := ociVerify(r, body, &ociKey(t).PublicKey); err != nil {
			t.Errorf("signature verification failed: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"NotAuthenticated","message":"bad signature"}`))
			return
		}
		handler(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ociCollect(t *testing.T, ch <-chan core.StreamChunk) []core.StreamChunk {
	t.Helper()
	var out []core.StreamChunk
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, c)
		case <-timeout:
			t.Fatal("stream did not close")
		}
	}
}

func ociIntP(v int) *int           { return &v }
func ociFloatP(v float64) *float64 { return &v }

func TestOCISignRequestSigningString(t *testing.T) {
	s := ociSigner{keyID: "t/u/f", key: ociKey(t)}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	body := []byte(`{"a":1}`)
	h, err := ociSignRequest(http.MethodPost, "https://inference.example.com/20231130/actions/chat?x=a%20b", body, s, now)
	if err != nil {
		t.Fatal(err)
	}
	if h["date"] != "Fri, 02 Jan 2026 03:04:05 GMT" || h["content-length"] != "7" || h["content-type"] != "application/json" {
		t.Fatalf("unexpected headers: %v", h)
	}
	if _, ok := h["host"]; ok {
		t.Fatal("host must be left to the HTTP client")
	}
	sum := sha256.Sum256(body)
	wantSigning := strings.Join([]string{
		"date: Fri, 02 Jan 2026 03:04:05 GMT",
		"(request-target): post /20231130/actions/chat?x=a%20b",
		"host: inference.example.com",
		"content-length: 7",
		"content-type: application/json",
		"x-content-sha256: " + base64.StdEncoding.EncodeToString(sum[:]),
	}, "\n")
	signed := map[string]string{"date": h["date"], "host": "inference.example.com", "content-length": "7",
		"content-type": "application/json", "x-content-sha256": h["x-content-sha256"]}
	if got := ociSigningString(ociSignedHeadersBody, "post /20231130/actions/chat?x=a%20b", signed); got != wantSigning {
		t.Fatalf("signing string:\n%s\nwant:\n%s", got, wantSigning)
	}
	auth := h["authorization"]
	if !strings.Contains(auth, `keyId="t/u/f"`) || !strings.Contains(auth, `algorithm="rsa-sha256"`) ||
		!strings.Contains(auth, `headers="date (request-target) host content-length content-type x-content-sha256"`) {
		t.Fatalf("authorization = %s", auth)
	}
	sigB64 := auth[strings.Index(auth, `signature="`)+len(`signature="`) : len(auth)-1]
	sig, _ := base64.StdEncoding.DecodeString(sigB64)
	digest := sha256.Sum256([]byte(wantSigning))
	if err := rsa.VerifyPKCS1v15(&ociKey(t).PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	// PKCS#1 v1.5 is deterministic: same input, same signature.
	h2, _ := ociSignRequest(http.MethodPost, "https://inference.example.com/20231130/actions/chat?x=a%20b", body, s, now)
	if h2["authorization"] != auth {
		t.Fatal("signature is not deterministic")
	}
}

func TestOCISignRequestGET(t *testing.T) {
	h, err := ociSignRequest(http.MethodGet, "https://h.example.com/20231130/models?compartmentId=c&limit=1",
		nil, ociSigner{keyID: "k", key: ociKey(t)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h["x-content-sha256"]; ok {
		t.Fatal("GET must not sign content headers")
	}
	if !strings.Contains(h["authorization"], `headers="date (request-target) host"`) {
		t.Fatalf("authorization = %s", h["authorization"])
	}
}

func TestOCIParsePrivateKey(t *testing.T) {
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(ociKey(t))}))
	if k, err := parseOCIPrivateKey(pkcs1); err != nil || !k.Equal(ociKey(t)) {
		t.Fatalf("pkcs1: %v", err)
	}
	if k, err := parseOCIPrivateKey(ociPKCS8PEM(t)); err != nil || !k.Equal(ociKey(t)) {
		t.Fatalf("pkcs8: %v", err)
	}
	// Single-line paste with literal \n and the console's trailing marker.
	flat := strings.ReplaceAll(ociPKCS8PEM(t), "\n", `\n`) + "OCI_API_KEY"
	if _, err := parseOCIPrivateKey(flat); err != nil {
		t.Fatalf("escaped newlines: %v", err)
	}
	legacy := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00"}, Bytes: []byte{1, 2}}))
	if _, err := parseOCIPrivateKey(legacy); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("legacy encrypted: %v", err)
	}
	if _, err := parseOCIPrivateKey(string(pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1}}))); err == nil ||
		!strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("pkcs8 encrypted: %v", err)
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(ec)
	if _, err := parseOCIPrivateKey(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))); err == nil ||
		!strings.Contains(err.Error(), "RSA") {
		t.Fatalf("ec key: %v", err)
	}
	if _, err := parseOCIPrivateKey("not a key"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestOCISignerCachesKeyAndReportsMissingFields(t *testing.T) {
	creds := ociCreds(t, "")
	a, err := ociSignerFor("oci", creds)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ociSignerFor("oci", creds)
	if a.key != b.key || a.keyID != ociTestKeyID {
		t.Fatalf("key not cached or bad keyId %q", a.keyID)
	}
	delete(creds.Extra, "oci_user")
	delete(creds.Extra, "oci_key")
	_, err = ociSignerFor("oci", creds)
	pe := core.AsProviderError(err)
	if pe.Kind != core.ErrAuth || !strings.Contains(pe.Message, "user OCID") || !strings.Contains(pe.Message, "API private key") {
		t.Fatalf("missing fields error = %v", err)
	}
	creds.Extra["oci_user"] = "u"
	creds.Extra["oci_key"] = "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----"
	_, err = ociSignerFor("oci", creds)
	if core.AsProviderError(err).Kind != core.ErrAuth || strings.Contains(err.Error(), "AAAA") {
		t.Fatalf("bad key error = %v", err)
	}
}

func TestOCIEndpoints(t *testing.T) {
	c := NewOCI("oci", "https://inference.generativeai.{oci_region}.oci.oraclecloud.com")
	creds := ociCreds(t, "")
	creds.Extra["oci_region"] = "eu-frankfurt-1"
	if got, _ := c.inferenceBase(creds); got != "https://inference.generativeai.eu-frankfurt-1.oci.oraclecloud.com" {
		t.Fatalf("inference base = %s", got)
	}
	if got, _ := c.controlBase(creds); got != "https://generativeai.eu-frankfurt-1.oci.oraclecloud.com" {
		t.Fatalf("control base = %s", got)
	}
	creds.Extra["oci_compartment_id"] = "ocid1.compartment.oc2..gov"
	creds.Extra["oci_region"] = "us-langley-1"
	if got, _ := c.inferenceBase(creds); got != "https://inference.generativeai.us-langley-1.oci.oraclegovcloud.com" {
		t.Fatalf("gov realm base = %s", got)
	}
	creds.Extra["oci_region"] = "evil.com/x"
	if _, err := c.inferenceBase(creds); err == nil {
		t.Fatal("invalid region accepted")
	}
	creds.BaseURL = "https://example.test/20231130/actions/chat"
	if got, _ := c.inferenceBase(creds); got != "https://example.test" {
		t.Fatalf("override base = %s", got)
	}
	if s := ociServing(creds, "ocid1.generativeaiendpoint.oc1.x"); s.ServingType != "DEDICATED" || s.EndpointID == "" {
		t.Fatalf("endpoint OCID serving = %+v", s)
	}
	creds.Extra["oci_serving_mode"] = "DEDICATED"
	if s := ociServing(creds, "ocid1.x"); s.ServingType != "DEDICATED" || s.EndpointID != "ocid1.x" || s.ModelID != "" {
		t.Fatalf("dedicated serving = %+v", s)
	}
}

func TestOCIChatGeneric(t *testing.T) {
	var got map[string]any
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.URL.Path != "/20231130/actions/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"modelId":"meta.llama-3.3-70b-instruct","modelVersion":"1","chatResponse":{
			"apiFormat":"GENERIC","timeCreated":"2026-01-01T00:00:00Z",
			"choices":[{"index":0,"finishReason":"TOOL_CALLS","message":{"role":"ASSISTANT",
				"content":[{"type":"TEXT","text":"Let me "},{"type":"TEXT","text":"check."}],
				"toolCalls":[{"id":"call_1","type":"FUNCTION","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}]}}],
			"usage":{"promptTokens":12,"completionTokens":7,"totalTokens":19,
				"promptTokensDetails":{"cachedTokens":4},"completionTokensDetails":{"reasoningTokens":2}}}}`))
	})
	req := &core.ChatRequest{
		Model:     "meta.llama-3.3-70b-instruct",
		System:    "be brief",
		MaxTokens: ociIntP(256), Temperature: ociFloatP(0.2), TopP: ociFloatP(0.9), Stop: []string{"END"},
		Extra: map[string]json.RawMessage{"top_k": json.RawMessage(`40`), "frequency_penalty": json.RawMessage(`0.5`)},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{
				{Type: core.PartText, Text: "weather?"},
				{Type: core.PartImage, Media: &core.MediaPayload{MIMEType: "image/jpeg", Data: "QUJD"}},
			}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartToolCall,
				ToolCall: &core.ToolCall{ID: "call_0", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Rome"}`)}}}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult,
				ToolResult: &core.ToolResult{CallID: "call_0", Content: "sunny"}}}},
		},
		Tools: []core.Tool{{Name: "get_weather", Description: "weather", Parameters: json.RawMessage(
			`{"type":"object","title":"W","properties":{"city":{"anyOf":[{"type":"string"},{"type":"null"}],"title":"City","default":null},
			"tags":{"type":"array"},"unit":{"$ref":"#/$defs/Unit"}},"required":["city","ghost"],"$defs":{"Unit":{"type":"string","enum":["c","f"]}}}`)}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}},
	}
	resp, err := NewOCI("oci", "").Chat(context.Background(), req, ociCreds(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	if got["compartmentId"] != "ocid1.compartment.oc1..comp" {
		t.Fatalf("compartmentId = %v", got["compartmentId"])
	}
	if sm := got["servingMode"].(map[string]any); sm["servingType"] != "ON_DEMAND" || sm["modelId"] != "meta.llama-3.3-70b-instruct" {
		t.Fatalf("servingMode = %v", sm)
	}
	cr := got["chatRequest"].(map[string]any)
	if cr["apiFormat"] != "GENERIC" || cr["maxTokens"] != float64(256) || cr["topK"] != float64(40) ||
		cr["frequencyPenalty"] != 0.5 || cr["temperature"] != 0.2 || cr["isStream"] != nil {
		t.Fatalf("chatRequest params = %v", cr)
	}
	if tc := cr["toolChoice"].(map[string]any); tc["type"] != "FUNCTION" || tc["name"] != "get_weather" {
		t.Fatalf("toolChoice = %v", tc)
	}
	msgs, _ := json.Marshal(cr["messages"])
	wantMsgs := `[{"content":[{"text":"be brief","type":"TEXT"}],"role":"SYSTEM"},` +
		`{"content":[{"text":"weather?","type":"TEXT"},{"imageUrl":{"url":"data:image/jpeg;base64,QUJD"},"type":"IMAGE"}],"role":"USER"},` +
		`{"role":"ASSISTANT","toolCalls":[{"arguments":"{\"city\":\"Rome\"}","id":"call_0","name":"get_weather","type":"FUNCTION"}]},` +
		`{"content":[{"text":"sunny","type":"TEXT"}],"role":"TOOL","toolCallId":"call_0"}]`
	if string(msgs) != wantMsgs {
		t.Fatalf("messages:\n%s\nwant:\n%s", msgs, wantMsgs)
	}
	tools, _ := json.Marshal(cr["tools"])
	wantTools := `[{"description":"weather","name":"get_weather","parameters":{"properties":{"city":{"type":"string"},` +
		`"tags":{"items":{"type":"object"},"type":"array"},"unit":{"enum":["c","f"],"type":"string"}},"required":["city"],"type":"object"},"type":"FUNCTION"}]`
	if string(tools) != wantTools {
		t.Fatalf("tools:\n%s\nwant:\n%s", tools, wantTools)
	}

	if resp.FinishReason != core.FinishToolCalls || resp.Message.TextContent() != "Let me check." {
		t.Fatalf("response = %+v", resp)
	}
	tc := resp.Message.Content[len(resp.Message.Content)-1].ToolCall
	if tc == nil || tc.ID != "call_1" || tc.Name != "get_weather" || string(tc.Arguments) != `{"city":"Paris"}` {
		t.Fatalf("tool call = %+v", tc)
	}
	u := resp.Usage
	if u.PromptTokens != 12 || u.CompletionTokens != 7 || u.TotalTokens != 19 || u.CachedTokens != 4 || u.ReasoningTokens != 2 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestOCIChatReasoningModelUsesMaxCompletionTokens(t *testing.T) {
	var cr map[string]any
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		cr = got["chatRequest"].(map[string]any)
		_, _ = w.Write([]byte(`{"chatResponse":{"apiFormat":"GENERIC","choices":[{"finishReason":"MAX_TOKENS",
			"message":{"content":[{"type":"TEXT","text":"hi"}],"reasoningContent":"thinking"}}],
			"usage":{"promptTokens":1,"totalTokens":3,"completionTokens":2}}}`))
	})
	req := &core.ChatRequest{Model: "openai.gpt-5", Reasoning: &core.ReasoningConfig{Effort: "high"},
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	resp, err := NewOCI("oci", "").Chat(context.Background(), req, ociCreds(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if cr["maxCompletionTokens"] != float64(ociDefaultMaxTokens) || cr["maxTokens"] != nil || cr["reasoningEffort"] != "HIGH" {
		t.Fatalf("chatRequest = %v", cr)
	}
	if resp.FinishReason != core.FinishLength || resp.Message.Content[0].Type != core.PartThinking {
		t.Fatalf("response = %+v", resp)
	}
	if !ociUsesMaxCompletionTokens("google.gemini-2.5-pro") || ociUsesMaxCompletionTokens("openai.gpt-oss-120b") ||
		ociUsesMaxCompletionTokens("meta.llama-3.3-70b-instruct") {
		t.Fatal("maxCompletionTokens model rule")
	}
}

func TestOCIChatCohere(t *testing.T) {
	var cr map[string]any
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		cr = got["chatRequest"].(map[string]any)
		_, _ = w.Write([]byte(`{"modelId":"cohere.command-a-03-2025","chatResponse":{"apiFormat":"COHERE","text":"",
			"finishReason":"COMPLETE","toolCalls":[{"name":"lookup","parameters":{"q":"x"}}],
			"usage":{"promptTokens":5,"completionTokens":3,"totalTokens":8}}}`))
	})
	req := &core.ChatRequest{
		Model: "cohere.command-a-03-2025",
		Stop:  []string{"##"},
		Messages: []core.Message{
			{Role: core.RoleSystem, Content: []core.ContentPart{{Type: core.PartText, Text: "sys"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "first"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "ok"}}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "find x and y"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "a", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)}},
				{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: "b", Name: "lookup", Arguments: json.RawMessage(`{"q":"y"}`)}},
			}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "a", Content: "X"}}}},
			{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: "b", Content: "Y"}}}},
		},
		Tools: []core.Tool{{Name: "lookup", Description: "search", Parameters: json.RawMessage(
			`{"type":"object","properties":{"q":{"type":"string","description":"query","enum":["x","y"]},"n":{"type":"integer","minimum":1}},"required":["q"]}`)}},
		ToolChoice: "auto",
	}
	resp, err := NewOCI("oci", "").Chat(context.Background(), req, ociCreds(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if cr["apiFormat"] != "COHERE" || cr["message"] != "find x and y" || cr["preambleOverride"] != "sys" || cr["toolChoice"] != nil {
		t.Fatalf("cohere request = %v", cr)
	}
	if s, _ := json.Marshal(cr["stopSequences"]); string(s) != `["##"]` {
		t.Fatalf("stopSequences = %s", s)
	}
	hist, _ := json.Marshal(cr["chatHistory"])
	wantHist := `[{"message":"first","role":"USER"},{"message":"ok","role":"CHATBOT"},` +
		`{"message":"","role":"CHATBOT","toolCalls":[{"name":"lookup","parameters":{"q":"x"}},{"name":"lookup","parameters":{"q":"y"}}]},` +
		`{"role":"TOOL","toolResults":[{"call":{"name":"lookup","parameters":{"q":"x"}},"outputs":[{"output":"X"}]},` +
		`{"call":{"name":"lookup","parameters":{"q":"y"}},"outputs":[{"output":"Y"}]}]}]`
	if string(hist) != wantHist {
		t.Fatalf("chatHistory:\n%s\nwant:\n%s", hist, wantHist)
	}
	tools, _ := json.Marshal(cr["tools"])
	wantTools := `[{"description":"search","name":"lookup","parameterDefinitions":{"n":{"description":"Range: min=1","isRequired":false,"type":"int"},` +
		`"q":{"description":"query. Allowed values: [\"x\",\"y\"]","isRequired":true,"type":"str"}}}]`
	if string(tools) != wantTools {
		t.Fatalf("tools:\n%s\nwant:\n%s", tools, wantTools)
	}
	if resp.FinishReason != core.FinishToolCalls || resp.Usage.TotalTokens != 8 {
		t.Fatalf("response = %+v", resp)
	}
	tc := resp.Message.Content[0].ToolCall
	if tc == nil || tc.Name != "lookup" || string(tc.Arguments) != `{"q":"x"}` || !strings.HasPrefix(tc.ID, "call_") {
		t.Fatalf("tool call = %+v", tc)
	}
}

func TestOCICohereRequiresUserMessage(t *testing.T) {
	req := &core.ChatRequest{Model: "cohere.command-r-08-2024",
		Messages: []core.Message{{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	_, err := NewOCI("oci", "").Chat(context.Background(), req, ociCreds(t, "http://127.0.0.1:1"))
	if pe := core.AsProviderError(err); pe.Kind != core.ErrBadRequest || pe.Provider != "oci" {
		t.Fatalf("err = %v", err)
	}
}

func ociSSE(w http.ResponseWriter, frames ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, f := range frames {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
	}
}

func TestOCIStreamGeneric(t *testing.T) {
	var cr map[string]any
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		cr = got["chatRequest"].(map[string]any)
		ociSSE(w,
			`{"index":0,"message":{"role":"ASSISTANT","content":[{"type":"TEXT","text":"Hel"}]}}`,
			`{"index":0,"message":{"role":"ASSISTANT","content":[{"type":"TEXT","text":"lo"}]}}`,
			`{"index":0,"message":{"role":"ASSISTANT","toolCalls":[{"id":"call_9","type":"FUNCTION","name":"f","arguments":""}]}}`,
			`{"index":0,"message":{"role":"ASSISTANT","toolCalls":[{"type":"FUNCTION","arguments":"{\"a\":"}]}}`,
			`{"index":0,"message":{"role":"ASSISTANT","toolCalls":[{"type":"FUNCTION","arguments":"1}"}]}}`,
			`{"index":0,"finishReason":"stop"}`,
			`{"usage":{"promptTokens":3,"completionTokens":4,"totalTokens":7}}`,
		)
	})
	req := &core.ChatRequest{Model: "xai.grok-4", Stream: true,
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	ch, err := NewOCI("oci", "").Stream(context.Background(), req, ociCreds(t, srv.URL), core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	chunks := ociCollect(t, ch)
	if cr["isStream"] != true || cr["streamOptions"].(map[string]any)["isIncludeUsage"] != true {
		t.Fatalf("stream request = %v", cr)
	}
	var text, args string
	var ids []string
	for _, c := range chunks {
		switch c.Type {
		case core.ChunkText:
			text += c.Delta
		case core.ChunkToolCall:
			args += string(c.ToolCall.Arguments)
			ids = append(ids, c.ToolCall.ID)
			if c.Index != 0 {
				t.Fatalf("tool index = %d", c.Index)
			}
		case core.ChunkError:
			t.Fatalf("unexpected error: %v", c.Err)
		}
	}
	if text != "Hello" || args != `{"a":1}` || strings.Join(ids, ",") != "call_9,call_9,call_9" {
		t.Fatalf("text=%q args=%q ids=%v", text, args, ids)
	}
	n := len(chunks)
	if chunks[n-2].Type != core.ChunkUsage || chunks[n-2].Usage.TotalTokens != 7 ||
		chunks[n-1].Type != core.ChunkFinish || chunks[n-1].FinishReason != core.FinishToolCalls {
		t.Fatalf("tail = %+v %+v", chunks[n-2], chunks[n-1])
	}
}

func TestOCIStreamCohereDropsRestatements(t *testing.T) {
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		ociSSE(w,
			`{"apiFormat":"COHERE","text":"Hi"}`,
			`{"apiFormat":"COHERE","text":" there"}`,
			`{"apiFormat":"COHERE","text":"Hi there","toolCalls":[{"name":"t","parameters":{"k":1}}]}`,
			`{"apiFormat":"COHERE","text":"Hi there","toolCalls":[{"name":"t","parameters":{"k":1}}],"chatHistory":[{"role":"USER"}],"finishReason":"COMPLETE"}`,
		)
	})
	req := &core.ChatRequest{Model: "cohere.command-a-03-2025",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	ch, err := NewOCI("oci", "").Stream(context.Background(), req, ociCreds(t, srv.URL), core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	calls := 0
	var last core.StreamChunk
	for _, c := range ociCollect(t, ch) {
		switch c.Type {
		case core.ChunkText:
			text += c.Delta
		case core.ChunkToolCall:
			calls++
		}
		last = c
	}
	if text != "Hi there" || calls != 1 || last.Type != core.ChunkFinish || last.FinishReason != core.FinishToolCalls {
		t.Fatalf("text=%q calls=%d last=%+v", text, calls, last)
	}
}

func TestOCIStreamInBandError(t *testing.T) {
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		ociSSE(w, `{"index":0,"message":{"content":[{"type":"TEXT","text":"a"}]}}`,
			`{"code":"TooManyRequests","message":"Too many requests for the tenancy"}`)
	})
	req := &core.ChatRequest{Model: "meta.llama-3.3-70b-instruct",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	ch, err := NewOCI("oci", "").Stream(context.Background(), req, ociCreds(t, srv.URL), core.StreamConfig{})
	if err != nil {
		t.Fatal(err)
	}
	chunks := ociCollect(t, ch)
	last := chunks[len(chunks)-1]
	pe := core.AsProviderError(last.Err)
	if last.Type != core.ChunkError || pe.Kind != core.ErrRateLimit || pe.Provider != "oci" {
		t.Fatalf("last chunk = %+v (%v)", last, last.Err)
	}
}

func TestOCIHTTPErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		kind   core.ErrorKind
	}{
		{401, `{"code":"NotAuthenticated","message":"The required information to complete authentication was not provided."}`, core.ErrAuth},
		{429, `{"code":"TooManyRequests","message":"Too many requests"}`, core.ErrRateLimit},
		{404, `{"code":"NotAuthorizedOrNotFound","message":"Entity with key x not found"}`, core.ErrModelUnavailable},
		{400, `{"code":"InvalidParameter","message":"bad"}`, core.ErrBadRequest},
	} {
		srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		req := &core.ChatRequest{Model: "meta.llama-3.3-70b-instruct",
			Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
		_, err := NewOCI("oci", "").Chat(context.Background(), req, ociCreds(t, srv.URL))
		if pe := core.AsProviderError(err); pe.Kind != tc.kind || pe.StatusCode != tc.status {
			t.Fatalf("status %d: err = %v", tc.status, err)
		}
		_, err = NewOCI("oci", "").Stream(context.Background(), req, ociCreds(t, srv.URL), core.StreamConfig{})
		if pe := core.AsProviderError(err); pe.Kind != tc.kind {
			t.Fatalf("stream status %d: err = %v", tc.status, err)
		}
	}
}

func TestOCIValidate(t *testing.T) {
	status := http.StatusOK
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/20231130/models" || q.Get("compartmentId") != "ocid1.compartment.oc1..comp" ||
			q.Get("capability") != "CHAT" || q.Get("limit") != "1" {
			t.Errorf("unexpected probe %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"items":[]}`))
		} else {
			_, _ = w.Write([]byte(`{"code":"NotAuthenticated","message":"no"}`))
		}
	})
	c := NewOCI("oci", "")
	if err := c.Validate(context.Background(), ociCreds(t, srv.URL)); err != nil {
		t.Fatalf("validate: %v", err)
	}
	status = http.StatusUnauthorized
	err := c.Validate(context.Background(), ociCreds(t, srv.URL))
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Kind != core.ErrAuth {
		t.Fatalf("validate 401: %v", err)
	}
	// Unreachable endpoint: a transport error, not an auth failure.
	err = c.Validate(context.Background(), ociCreds(t, "http://127.0.0.1:1"))
	if !errors.As(err, &pe) || pe.Kind == core.ErrAuth || pe.StatusCode != 0 {
		t.Fatalf("validate unreachable: %v", err)
	}
	bad := ociCreds(t, srv.URL)
	delete(bad.Extra, "oci_fingerprint")
	if err := c.Validate(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("validate missing field: %v", err)
	}
}

func TestOCIListModels(t *testing.T) {
	srv := ociServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("opc-next-page", "p2")
			_, _ = w.Write([]byte(`{"items":[
				{"id":"ocid1.a","displayName":"meta.llama-3.3-70b-instruct","type":"BASE","lifecycleState":"ACTIVE","capabilities":["CHAT"]},
				{"id":"ocid1.b","displayName":"meta.llama-3.3-70b-instruct","type":"BASE","lifecycleState":"ACTIVE"},
				{"id":"ocid1.c","displayName":"my-finetune","type":"CUSTOM","lifecycleState":"ACTIVE"},
				{"id":"ocid1.d","displayName":"old.model","type":"BASE","lifecycleState":"DELETED"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[
			{"id":"ocid1.e","displayName":"cohere.command-a-03-2025","type":"BASE","lifecycleState":"ACTIVE"},
			{"id":"ocid1.f","displayName":"retired.model","type":"BASE","lifecycleState":"ACTIVE","timeOnDemandRetired":"2020-01-01T00:00:00Z"}]}`))
	})
	models, err := NewOCI("oci", "").ListModels(context.Background(), ociCreds(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "meta.llama-3.3-70b-instruct,cohere.command-a-03-2025" {
		t.Fatalf("models = %v", ids)
	}
}

func TestOCIRegistered(t *testing.T) {
	f, ok := dialectFactories[core.DialectOCI]
	if !ok {
		t.Fatal("OCI dialect not registered")
	}
	c := f("oci", "")
	if _, ok := c.(LiveModelSource); !ok {
		t.Fatal("OCI connector should be a live model source")
	}
	if _, ok := c.(core.Validator); !ok {
		t.Fatal("OCI connector should validate")
	}
	if len(providerModels["oci"]) == 0 {
		t.Fatal("no static OCI models")
	}
}
