package gateway

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/connectors"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func TestProviderAccountMetadataSpecialProviders(t *testing.T) {
	cf, ok := connectors.SpecByID("cloudflare-ai")
	require.True(t, ok)
	_, _, err := providerAccountMetadata(cf, providerMetadataInput{})
	require.Error(t, err)

	meta, _, err := providerAccountMetadata(cf, providerMetadataInput{AccountID: "acct-123"})
	require.NoError(t, err)
	require.Equal(t, "acct-123", meta["accountId"])

	azure, ok := connectors.SpecByID("azure")
	require.True(t, ok)
	meta, _, err = providerAccountMetadata(azure, providerMetadataInput{
		AzureEndpoint:   "https://example.openai.azure.com/",
		AzureDeployment: "prod-gpt",
		AzureAPIVersion: "2024-10-01-preview",
	})
	require.NoError(t, err)
	require.Equal(t, "https://example.openai.azure.com", meta["azure_endpoint"])
	require.Equal(t, "prod-gpt", meta["deployment"])
	require.Equal(t, "2024-10-01-preview", meta["api_version"])

	custom, ok := connectors.SpecByID("custom-openai")
	require.True(t, ok)
	_, _, err = providerAccountMetadata(custom, providerMetadataInput{})
	require.Error(t, err)
	meta, _, err = providerAccountMetadata(custom, providerMetadataInput{BaseURL: "https://llm.example.com/v1"})
	require.NoError(t, err)
	require.Equal(t, "https://llm.example.com/v1", meta["base_url"])
}

func TestAccountAuthKindNoAuthProvider(t *testing.T) {
	spec, ok := connectors.SpecByID("searxng")
	require.True(t, ok)
	require.Equal(t, store.AuthNone, accountAuthKind(spec, ""))
	require.Equal(t, store.AuthAPIKey, accountAuthKind(spec, "optional-key"))
}

func TestProviderAccountMetadataCredentialFields(t *testing.T) {
	bedrock, ok := connectors.SpecByID("bedrock")
	if !ok {
		t.Fatal("bedrock missing")
	}

	// Access-key mode: required keys enforced, secret split from metadata,
	// fields of other modes dropped.
	_, _, err := providerAccountMetadata(bedrock, providerMetadataInput{Fields: map[string]string{"aws_auth": "access_key"}})
	if err == nil {
		t.Fatal("missing access key must fail")
	}
	meta, secrets, err := providerAccountMetadata(bedrock, providerMetadataInput{Fields: map[string]string{
		"aws_auth": "access_key", "aws_region": "eu-west-1", "aws_access_key_id": "AKID",
		"aws_secret_access_key": "s3cr3t", "aws_role_arn": "arn:aws:iam::1:role/r",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if meta["aws_access_key_id"] != "AKID" || meta["aws_region"] != "eu-west-1" || meta["aws_role_arn"] == "" {
		t.Fatalf("meta = %v", meta)
	}
	if _, leaked := meta["aws_secret_access_key"]; leaked {
		t.Fatal("secret stored as plain metadata")
	}
	if secrets["aws_secret_access_key"] != "s3cr3t" {
		t.Fatalf("secrets = %v", secrets)
	}

	// API-key mode ignores the IAM fields entirely and fills defaults.
	meta, secrets, err = providerAccountMetadata(bedrock, providerMetadataInput{Fields: map[string]string{
		"aws_access_key_id": "AKID", "aws_secret_access_key": "stale",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if meta["aws_auth"] != "api_key" || meta["aws_region"] != "us-east-1" || meta["aws_access_key_id"] != "" || len(secrets) != 0 {
		t.Fatalf("api-key mode: meta=%v secrets=%v", meta, secrets)
	}

	// Unknown select options are rejected.
	if _, _, err := providerAccountMetadata(bedrock, providerMetadataInput{Fields: map[string]string{"aws_auth": "magic"}}); err == nil {
		t.Fatal("invalid option must fail")
	}

	// URL fields pass the SSRF policy.
	dbx, _ := connectors.SpecByID("databricks")
	if _, _, err := providerAccountMetadata(dbx, providerMetadataInput{Fields: map[string]string{"databricks_host": "http://169.254.169.254"}}); err == nil {
		t.Fatal("metadata address must be blocked")
	}
	meta, _, err = providerAccountMetadata(dbx, providerMetadataInput{Fields: map[string]string{"databricks_host": "https://adb-1.azuredatabricks.net/"}})
	if err != nil {
		t.Fatal(err)
	}
	if meta["databricks_host"] != "https://adb-1.azuredatabricks.net" {
		t.Fatalf("host = %q", meta["databricks_host"])
	}
}
