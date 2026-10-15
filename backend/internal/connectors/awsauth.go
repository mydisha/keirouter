package connectors

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// AWS credential field keys shared by the Bedrock and SageMaker providers.
const (
	awsFieldAuth         = "aws_auth"
	awsFieldRegion       = "aws_region"
	awsFieldAccessKeyID  = "aws_access_key_id"
	awsFieldSecretKey    = "aws_secret_access_key"
	awsFieldSessionToken = "aws_session_token"
	awsFieldRoleARN      = "aws_role_arn"
	awsFieldExternalID   = "aws_external_id"

	awsAuthAPIKey      = "api_key"
	awsAuthAccessKey   = "access_key"
	awsAuthEnvironment = "environment"
)

// awsCredentials is one resolved AWS identity used to sign a request.
type awsCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expires         time.Time
}

func (c awsCredentials) valid() bool { return c.AccessKeyID != "" && c.SecretAccessKey != "" }

// awsRegion returns the account's AWS region (default us-east-1).
func awsRegion(creds core.Credentials) string {
	if r := extraValue(creds, awsFieldRegion); r != "" {
		return r
	}
	if r := extraValue(creds, "region"); r != "" {
		return r
	}
	return "us-east-1"
}

// resolveAWSCredentials returns the identity for an account: static access
// keys, or the server's environment chain (env vars, web identity, container
// credentials, instance metadata), optionally followed by an STS AssumeRole.
// Assumed-role and discovered credentials are cached until shortly before
// they expire.
func resolveAWSCredentials(ctx context.Context, provider string, creds core.Credentials) (awsCredentials, error) {
	var base awsCredentials
	mode := extraValue(creds, awsFieldAuth)
	switch {
	case mode == awsAuthEnvironment:
		var err error
		base, err = awsEnvironmentCredentials(ctx, provider)
		if err != nil {
			return awsCredentials{}, err
		}
	default:
		base = awsCredentials{
			AccessKeyID:     extraValue(creds, awsFieldAccessKeyID),
			SecretAccessKey: extraValue(creds, awsFieldSecretKey),
			SessionToken:    extraValue(creds, awsFieldSessionToken),
		}
		if !base.valid() {
			return awsCredentials{}, &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
				Message: "AWS access key ID and secret access key are required"}
		}
	}

	role := extraValue(creds, awsFieldRoleARN)
	if role == "" {
		return base, nil
	}
	return awsAssumeRole(ctx, provider, base, role, extraValue(creds, awsFieldExternalID), awsRegion(creds))
}

// awsCredCache holds assumed-role and environment-discovered identities.
var awsCredCache = struct {
	cache *tokenCache
}{cache: &tokenCache{tokens: map[string]exchangedToken{}}}

// cachedAWS stores AWS credentials in the shared token cache as JSON.
func cachedAWS(ctx context.Context, key string, fetch func(ctx context.Context) (awsCredentials, error)) (awsCredentials, error) {
	raw, err := awsCredCache.cache.get(ctx, key, func(ctx context.Context) (exchangedToken, error) {
		c, err := fetch(ctx)
		if err != nil {
			return exchangedToken{}, err
		}
		b, _ := json.Marshal(c)
		exp := c.Expires
		if exp.IsZero() {
			exp = time.Now().Add(15 * time.Minute)
		}
		return exchangedToken{Value: string(b), ExpiresAt: exp}, nil
	})
	if err != nil {
		return awsCredentials{}, err
	}
	var c awsCredentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return awsCredentials{}, err
	}
	return c, nil
}

// awsEnvironmentCredentials walks the standard server-side credential chain.
func awsEnvironmentCredentials(ctx context.Context, provider string) (awsCredentials, error) {
	if id, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"); id != "" && secret != "" {
		return awsCredentials{AccessKeyID: id, SecretAccessKey: secret, SessionToken: os.Getenv("AWS_SESSION_TOKEN")}, nil
	}
	if tokenFile, role := os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE"), os.Getenv("AWS_ROLE_ARN"); tokenFile != "" && role != "" {
		return cachedAWS(ctx, tokenCacheKey("aws-web-identity", tokenFile, role), func(ctx context.Context) (awsCredentials, error) {
			return awsAssumeRoleWithWebIdentity(ctx, provider, tokenFile, role)
		})
	}
	if rel, full := os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"), os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI"); rel != "" || full != "" {
		endpoint := full
		if rel != "" {
			endpoint = "http://169.254.170.2" + rel
		}
		return cachedAWS(ctx, tokenCacheKey("aws-container", endpoint), func(ctx context.Context) (awsCredentials, error) {
			headers := map[string]string{}
			if tok := os.Getenv("AWS_CONTAINER_AUTHORIZATION_TOKEN"); tok != "" {
				headers["Authorization"] = tok
			}
			return awsFetchCredentialDocument(ctx, endpoint, headers)
		})
	}
	return cachedAWS(ctx, "aws-imds", func(ctx context.Context) (awsCredentials, error) {
		return awsInstanceMetadataCredentials(ctx, provider)
	})
}

// awsCredentialDocument is the JSON shape served by the container and
// instance-metadata credential endpoints.
type awsCredentialDocument struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	Token           string `json:"Token"`
	Expiration      string `json:"Expiration"`
}

func (d awsCredentialDocument) credentials() awsCredentials {
	c := awsCredentials{AccessKeyID: d.AccessKeyID, SecretAccessKey: d.SecretAccessKey, SessionToken: d.Token}
	if t, err := time.Parse(time.RFC3339, d.Expiration); err == nil {
		c.Expires = t
	}
	return c
}

// metadataClient talks to link-local credential endpoints. It deliberately
// bypasses the account proxy pool: these addresses only exist on the host.
var metadataClient = &http.Client{Timeout: 3 * time.Second}

func awsFetchCredentialDocument(ctx context.Context, endpoint string, headers map[string]string) (awsCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return awsCredentials{}, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := metadataClient.Do(req)
	if err != nil {
		return awsCredentials{}, fmt.Errorf("aws credentials endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return awsCredentials{}, fmt.Errorf("aws credentials endpoint returned %d", resp.StatusCode)
	}
	var doc awsCredentialDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&doc); err != nil {
		return awsCredentials{}, fmt.Errorf("aws credentials endpoint: %w", err)
	}
	c := doc.credentials()
	if !c.valid() {
		return awsCredentials{}, fmt.Errorf("aws credentials endpoint returned no keys")
	}
	return c, nil
}

// awsInstanceMetadataCredentials reads the instance role via IMDSv2.
func awsInstanceMetadataCredentials(ctx context.Context, provider string) (awsCredentials, error) {
	const imds = "http://169.254.169.254/latest"
	noCreds := &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
		Message: "no AWS credentials found in the server environment (env vars, web identity, container or instance role)"}

	tokReq, err := http.NewRequestWithContext(ctx, http.MethodPut, imds+"/api/token", nil)
	if err != nil {
		return awsCredentials{}, err
	}
	tokReq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	tokResp, err := metadataClient.Do(tokReq)
	if err != nil {
		return awsCredentials{}, noCreds
	}
	tokBytes, _ := io.ReadAll(io.LimitReader(tokResp.Body, 4096))
	tokResp.Body.Close()
	if tokResp.StatusCode != http.StatusOK {
		return awsCredentials{}, noCreds
	}
	headers := map[string]string{"X-aws-ec2-metadata-token": string(tokBytes)}

	roleReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imds+"/meta-data/iam/security-credentials/", nil)
	if err != nil {
		return awsCredentials{}, err
	}
	roleReq.Header.Set("X-aws-ec2-metadata-token", string(tokBytes))
	roleResp, err := metadataClient.Do(roleReq)
	if err != nil {
		return awsCredentials{}, noCreds
	}
	roleBytes, _ := io.ReadAll(io.LimitReader(roleResp.Body, 4096))
	roleResp.Body.Close()
	role := strings.TrimSpace(strings.SplitN(string(roleBytes), "\n", 2)[0])
	if roleResp.StatusCode != http.StatusOK || role == "" {
		return awsCredentials{}, noCreds
	}
	return awsFetchCredentialDocument(ctx, imds+"/meta-data/iam/security-credentials/"+url.PathEscape(role), headers)
}

// stsCredentials is the Credentials element of an STS AssumeRole* response.
type stsCredentials struct {
	AccessKeyID     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	SessionToken    string `xml:"SessionToken"`
	Expiration      string `xml:"Expiration"`
}

type stsAssumeRoleResponse struct {
	Result struct {
		Credentials stsCredentials `xml:"Credentials"`
	} `xml:"AssumeRoleResult"`
}

type stsWebIdentityResponse struct {
	Result struct {
		Credentials stsCredentials `xml:"Credentials"`
	} `xml:"AssumeRoleWithWebIdentityResult"`
}

func (c stsCredentials) credentials() awsCredentials {
	out := awsCredentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken}
	if t, err := time.Parse(time.RFC3339, c.Expiration); err == nil {
		out.Expires = t
	}
	return out
}

func stsEndpoint(region string) string {
	if region == "" {
		return "https://sts.amazonaws.com/"
	}
	return "https://sts." + region + ".amazonaws.com/"
}

// awsAssumeRole exchanges base credentials for a role session via STS.
func awsAssumeRole(ctx context.Context, provider string, base awsCredentials, roleARN, externalID, region string) (awsCredentials, error) {
	key := tokenCacheKey("aws-assume-role", base.AccessKeyID, base.SecretAccessKey, roleARN, externalID, region)
	return cachedAWS(ctx, key, func(ctx context.Context) (awsCredentials, error) {
		form := url.Values{
			"Action":          {"AssumeRole"},
			"Version":         {"2011-06-15"},
			"RoleArn":         {roleARN},
			"RoleSessionName": {"keirouter-" + fmt.Sprint(time.Now().Unix())},
			"DurationSeconds": {"3600"},
		}
		if externalID != "" {
			form.Set("ExternalId", externalID)
		}
		body := []byte(form.Encode())
		endpoint := stsEndpoint(region)
		headers, err := signAWSRequest(http.MethodPost, endpoint, body, map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
		}, base, region, "sts", time.Now())
		if err != nil {
			return awsCredentials{}, err
		}
		raw, err := doRawRequest(ctx, provider, http.MethodPost, endpoint, body, headers)
		if err != nil {
			return awsCredentials{}, asAuthError(provider, err)
		}
		var out stsAssumeRoleResponse
		if err := xml.Unmarshal(raw, &out); err != nil {
			return awsCredentials{}, fmt.Errorf("sts AssumeRole: %w", err)
		}
		c := out.Result.Credentials.credentials()
		if !c.valid() {
			return awsCredentials{}, &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
				Message: "sts AssumeRole returned no credentials"}
		}
		return c, nil
	})
}

// awsAssumeRoleWithWebIdentity serves EKS/IRSA-style deployments: the
// projected service-account token is exchanged (unsigned) for role keys.
func awsAssumeRoleWithWebIdentity(ctx context.Context, provider, tokenFile, roleARN string) (awsCredentials, error) {
	tok, err := os.ReadFile(tokenFile)
	if err != nil {
		return awsCredentials{}, fmt.Errorf("read web identity token: %w", err)
	}
	form := url.Values{
		"Action":           {"AssumeRoleWithWebIdentity"},
		"Version":          {"2011-06-15"},
		"RoleArn":          {roleARN},
		"RoleSessionName":  {"keirouter-" + fmt.Sprint(time.Now().Unix())},
		"WebIdentityToken": {strings.TrimSpace(string(tok))},
	}
	raw, err := doRawRequest(ctx, provider, http.MethodPost, stsEndpoint(os.Getenv("AWS_REGION")), []byte(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if err != nil {
		return awsCredentials{}, asAuthError(provider, err)
	}
	var out stsWebIdentityResponse
	if err := xml.Unmarshal(raw, &out); err != nil {
		return awsCredentials{}, fmt.Errorf("sts AssumeRoleWithWebIdentity: %w", err)
	}
	c := out.Result.Credentials.credentials()
	if !c.valid() {
		return awsCredentials{}, fmt.Errorf("sts AssumeRoleWithWebIdentity returned no credentials")
	}
	return c, nil
}

// doRawRequest sends a request through the shared resilient transport and
// returns the body, mapping HTTP errors to ProviderErrors.
func doRawRequest(ctx context.Context, provider, method, endpoint string, body []byte, headers map[string]string) ([]byte, error) {
	build := func() (*http.Request, error) {
		var r io.Reader
		if body != nil {
			r = strings.NewReader(string(body))
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, r)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	resp, err := sendRequest(ctx, provider, "auth", build)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := readBodyLimited(provider, "auth", resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(provider, "auth", resp, b)
	}
	return b, nil
}

// signAWSRequest computes AWS Signature Version 4 headers for a request and
// returns them merged with the given headers. The caller sends exactly these
// headers with the same method, URL and body. Signed headers are host,
// x-amz-date, x-amz-security-token (when present) and every header passed in,
// so content negotiation cannot be altered in flight.
func signAWSRequest(method, rawURL string, body []byte, headers map[string]string, c awsCredentials, region, service string, now time.Time) (map[string]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	payloadHash := sha256Hex(body)

	out := make(map[string]string, len(headers)+4)
	for k, v := range headers {
		out[k] = v
	}
	out["X-Amz-Date"] = amzDate
	if service == "s3" {
		out["X-Amz-Content-Sha256"] = payloadHash
	}
	if c.SessionToken != "" {
		out["X-Amz-Security-Token"] = c.SessionToken
	}

	canon := make(map[string]string, len(out)+1)
	for k, v := range out {
		canon[strings.ToLower(k)] = strings.Join(strings.Fields(v), " ")
	}
	canon["host"] = u.Host
	names := make([]string, 0, len(canon))
	for k := range canon {
		names = append(names, k)
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, k := range names {
		ch.WriteString(k)
		ch.WriteByte(':')
		ch.WriteString(canon[k])
		ch.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		method,
		awsCanonicalURI(u, service),
		awsCanonicalQuery(u),
		ch.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))

	kDate := hmacSHA256([]byte("AWS4"+c.SecretAccessKey), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	out["Authorization"] = "AWS4-HMAC-SHA256 Credential=" + c.AccessKeyID + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
	return out, nil
}

// awsCanonicalURI URI-encodes each path segment of the already-escaped path a
// second time, as every service except S3 expects.
func awsCanonicalURI(u *url.URL, service string) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	if service == "s3" {
		return p
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = awsURIEncode(s)
	}
	return strings.Join(segs, "/")
}

func awsCanonicalQuery(u *url.URL) string {
	q := u.Query()
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsURIEncode(k)+"="+awsURIEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsURIEncode percent-encodes everything except RFC 3986 unreserved bytes.
func awsURIEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}

// awsPathEscape escapes one URL path segment the way AWS SDKs do (":" and
// other reserved characters become %XX), for model ids such as
// "anthropic.claude-sonnet-4-5-20250929-v1:0" or inference profile ARNs.
func awsPathEscape(segment string) string { return awsURIEncode(segment) }

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// awsAuthHeaders resolves the headers that authenticate one AWS request: a
// Bedrock API key travels as a bearer token; everything else is SigV4-signed
// for service in the account's region. headers are the request's own headers
// (Content-Type, Accept, ...) and are included in the signature.
func awsAuthHeaders(ctx context.Context, provider, service, method, rawURL string, body []byte, headers map[string]string, creds core.Credentials) (map[string]string, error) {
	mode := extraValue(creds, awsFieldAuth)
	if key := strings.TrimSpace(creds.APIKey); key != "" && (mode == "" || mode == awsAuthAPIKey) {
		out := make(map[string]string, len(headers)+1)
		for k, v := range headers {
			out[k] = v
		}
		out["Authorization"] = bearer(key)
		return mergeHeaders(out, creds.Headers), nil
	}
	if mode == awsAuthAPIKey {
		return nil, &core.ProviderError{Kind: core.ErrAuth, Provider: provider, StatusCode: http.StatusUnauthorized,
			Message: "API key is required"}
	}
	ac, err := resolveAWSCredentials(ctx, provider, creds)
	if err != nil {
		return nil, err
	}
	// Account-level extra headers are signed too.
	return signAWSRequest(method, rawURL, body, mergeHeaders(headers, creds.Headers), ac, awsRegion(creds), service, time.Now())
}

// awsCallerIdentity confirms that an account's AWS identity is accepted, via
// STS GetCallerIdentity (allowed for every principal, no IAM policy needed).
func awsCallerIdentity(ctx context.Context, provider string, creds core.Credentials) error {
	ac, err := resolveAWSCredentials(ctx, provider, creds)
	if err != nil {
		return err
	}
	region := awsRegion(creds)
	body := []byte("Action=GetCallerIdentity&Version=2011-06-15")
	endpoint := stsEndpoint(region)
	headers, err := signAWSRequest(http.MethodPost, endpoint, body,
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, ac, region, "sts", time.Now())
	if err != nil {
		return err
	}
	_, err = doRawRequest(ctx, provider, http.MethodPost, endpoint, body, headers)
	return asAuthError(provider, err)
}
