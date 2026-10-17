package hosting

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	s := Settings{RootDomain: " *.Host.Example.com. ", Images: []string{" a:1 ", "a:1", ""}, DokployURL: "https://d.example.com/"}
	require.NoError(t, s.Normalize())
	require.Equal(t, "host.example.com", s.RootDomain)
	require.Equal(t, []string{"a:1"}, s.Images)
	require.Equal(t, DefaultPort, s.ContainerPort)
	require.Equal(t, "https://d.example.com", s.DokployURL)

	s.Enabled = true
	require.ErrorContains(t, s.Normalize(), "monthly_price_idr")
	s.MonthlyPriceIDR = 50000
	require.ErrorContains(t, s.Normalize(), "API key")
	s.APIKey, s.EnvironmentID = "k", "env"
	require.NoError(t, s.Normalize())

	bad := Settings{RootDomain: "bad_domain.com"}
	require.Error(t, bad.Normalize())

	plain := Settings{DokployURL: "http://d.example.com"}
	require.ErrorContains(t, plain.Normalize(), "https")

	env := Settings{PasswordEnv: "BAD NAME"}
	require.Error(t, env.Normalize())

	mem := Settings{MemoryLimit: "512m"}
	require.Error(t, mem.Normalize())
}

func TestCheckImageVersion(t *testing.T) {
	for _, ok := range []string{"decolua/9router:latest", "decolua/9router:0.4.60", "decolua/9router:v1.0.0",
		"decolua/9router", "decolua/9router@sha256:abc", "other/app:0.1.0", "reg.io:5000/9router:0.5.0"} {
		require.NoError(t, checkImageVersion(ok), ok)
	}
	for _, bad := range []string{"decolua/9router:0.4.59", "decolua/9router:v0.3.9", "reg.io:5000/x/9router:0.4.1"} {
		require.Error(t, checkImageVersion(bad), bad)
	}
}

func TestRandomPassword(t *testing.T) {
	a, err := RandomPassword()
	require.NoError(t, err)
	b, err := RandomPassword()
	require.NoError(t, err)
	require.Len(t, a, 20)
	require.NotEqual(t, a, b)
}

func TestRandomSubdomain(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9]{7}$`)
	for i := 0; i < 50; i++ {
		s, err := RandomSubdomain()
		require.NoError(t, err)
		require.Regexp(t, re, s)
	}
}

func TestLoadDefaultsWhenMissing(t *testing.T) {
	get := func(context.Context, string) (string, error) { return "", nil }
	s, err := Load(context.Background(), get, nil)
	require.NoError(t, err)
	require.Equal(t, DefaultPort, s.ContainerPort)
}

func TestMaskAPIKey(t *testing.T) {
	require.Equal(t, "", MaskAPIKey(""))
	require.Equal(t, "••••wxyz", MaskAPIKey("abcdwxyz"))
}
