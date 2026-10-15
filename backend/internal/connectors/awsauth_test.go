package connectors

import (
	"strings"
	"testing"
	"time"
)

func TestSignAWSRequestMatchesReferenceVectors(t *testing.T) {
	creds := awsCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}
	at := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

	cases := []struct {
		name, method, url, region, service, signature, signed string
		headers                                               map[string]string
	}{
		{
			name: "get-vanilla", method: "GET", url: "https://example.amazonaws.com/",
			region: "us-east-1", service: "service",
			signature: "5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31",
			signed:    "host;x-amz-date",
		},
		{
			name: "iam-list-users", method: "GET", url: "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08",
			region: "us-east-1", service: "iam",
			headers:   map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=utf-8"},
			signature: "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7",
			signed:    "content-type;host;x-amz-date",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := signAWSRequest(tc.method, tc.url, nil, tc.headers, creds, tc.region, tc.service, at)
			if err != nil {
				t.Fatal(err)
			}
			auth := h["Authorization"]
			if !strings.Contains(auth, "Signature="+tc.signature) {
				t.Fatalf("signature mismatch:\n got %s\nwant %s", auth, tc.signature)
			}
			if !strings.Contains(auth, "SignedHeaders="+tc.signed+",") {
				t.Fatalf("signed headers mismatch: %s", auth)
			}
			if h["X-Amz-Date"] != "20150830T123600Z" {
				t.Fatalf("x-amz-date = %q", h["X-Amz-Date"])
			}
		})
	}
}

func TestSignAWSRequestSessionTokenAndColonPath(t *testing.T) {
	creds := awsCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret", SessionToken: "tok"}
	h, err := signAWSRequest("POST", "https://bedrock-runtime.us-east-1.amazonaws.com/model/"+awsPathEscape("anthropic.claude-v2:1")+"/converse",
		[]byte(`{}`), map[string]string{"Content-Type": "application/json"}, creds, "us-east-1", "bedrock", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if h["X-Amz-Security-Token"] != "tok" {
		t.Fatal("session token header missing")
	}
	if !strings.Contains(h["Authorization"], "SignedHeaders=content-type;host;x-amz-date;x-amz-security-token,") {
		t.Fatalf("unexpected signed headers: %s", h["Authorization"])
	}
	if awsPathEscape("anthropic.claude-v2:1") != "anthropic.claude-v2%3A1" {
		t.Fatalf("path escape = %s", awsPathEscape("anthropic.claude-v2:1"))
	}
}
