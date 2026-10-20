package hosting

import (
	"context"
	"regexp"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	s := Settings{Images: []string{" a:1 ", "a:1", ""}, DokployURL: "https://d.example.com/",
		Nodes: []Node{{ID: "n1", Name: "Node 1", PublicIP: "203.0.113.7", Enabled: true}}}
	require.NoError(t, s.Normalize())
	require.Equal(t, []string{"a:1"}, s.Images)
	require.Equal(t, DefaultPort, s.ContainerPort)
	require.Equal(t, "https://d.example.com", s.DokployURL)

	require.Equal(t, DefaultRegions(), s.Regions, "a fresh install gets the default regions")

	s.Enabled = true
	require.ErrorContains(t, s.Normalize(), "API key")
	s.APIKey, s.EnvironmentID = "k", "env"
	s.RootDomain, s.CloudflareZoneID, s.CloudflareToken = "tokenizer.ai.id", "z1", "cf"
	require.ErrorContains(t, s.Normalize(), "region", "the node is not in a region yet")
	s.Nodes[0].Region = "id"
	require.NoError(t, s.Normalize())
	s.Nodes[0].Region = "mars"
	require.ErrorContains(t, s.Normalize(), "unknown region")

	plain := Settings{DokployURL: "http://d.example.com"}
	require.ErrorContains(t, plain.Normalize(), "https")

	env := Settings{PasswordEnv: "BAD NAME"}
	require.Error(t, env.Normalize())

	mem := Settings{MemoryLimit: "512m"}
	require.Error(t, mem.Normalize())
}

func TestNormalizeMainDomain(t *testing.T) {
	s := Settings{RootDomain: " *.Tokenizer.ai.id. "}
	require.NoError(t, s.Normalize())
	require.Equal(t, "tokenizer.ai.id", s.RootDomain)

	bad := Settings{RootDomain: "bad domain"}
	require.Error(t, bad.Normalize())
}

func enableable() Settings {
	return Settings{Enabled: true, DokployURL: "https://d.example.com", APIKey: "k", EnvironmentID: "e",
		RootDomain: "tokenizer.ai.id", CloudflareZoneID: "z1", CloudflareToken: "cf",
		Images: []string{"x:1"}, Regions: DefaultRegions(),
		Nodes: []Node{{ID: "a", Name: "A", PublicIP: "203.0.113.7", Enabled: true, Region: "id"}}}
}

func TestEnableNeedsCloudflare(t *testing.T) {
	ok := enableable()
	require.NoError(t, ok.Normalize())
	for name, mut := range map[string]func(*Settings){
		"zone":   func(s *Settings) { s.CloudflareZoneID = "" },
		"token":  func(s *Settings) { s.CloudflareToken = "" },
		"domain": func(s *Settings) { s.RootDomain = "" },
	} {
		s := enableable()
		mut(&s)
		require.ErrorContains(t, s.Normalize(), "Cloudflare", name)
	}
}

func TestCloudflareTokenSealed(t *testing.T) {
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	box, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	var raw string
	set := func(_ context.Context, _, v string) error { raw = v; return nil }
	get := func(context.Context, string) (string, error) { return raw, nil }
	require.NoError(t, Save(context.Background(), set, box, Settings{CloudflareZoneID: "z1", CloudflareToken: "cf-secret-token"}))
	require.NotContains(t, raw, "cf-secret-token")
	got, err := Load(context.Background(), get, box)
	require.NoError(t, err)
	require.Equal(t, "cf-secret-token", got.CloudflareToken)
	require.Equal(t, "z1", got.CloudflareZoneID)
}

func TestInZone(t *testing.T) {
	s := Settings{RootDomain: "tokenizer.ai.id"}
	require.True(t, s.InZone("tokenizer.ai.id"))
	require.True(t, s.InZone("ai.id"))
	require.False(t, s.InZone("tokenizer.com"))
	require.False(t, s.InZone("kenizer.ai.id"))
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

func TestRegionStocks(t *testing.T) {
	s := Settings{
		Regions: []Region{
			{ID: "id", Name: "Indonesia", PriceIDR: 20000, Enabled: true},
			{ID: "eu", Name: "Europe", PriceIDR: 10000, Enabled: true},
			{ID: "us", Name: "US", PriceIDR: 30000, Enabled: true},
		},
		Nodes: []Node{
			{ID: "a", Region: "id", Capacity: 3, Enabled: true},
			{ID: "b", Region: "id", Capacity: 2, Enabled: true},
			{ID: "c", Region: "id", Capacity: 9, Enabled: false}, // off: no stock
			{ID: "d", Region: "eu", Capacity: 1, Enabled: true},
		},
	}
	got := map[string]RegionStock{}
	for _, rs := range s.RegionStocks(map[string]int{"a": 3, "b": 1, "c": 4, "d": 1}, map[string]int{"id": 0}) {
		got[rs.ID] = rs
	}
	require.Equal(t, 1, got["id"].Stock, "5 slots - 4 used")
	require.False(t, got["id"].SoldOut)
	require.Equal(t, 0, got["eu"].Stock)
	require.True(t, got["eu"].SoldOut)
	require.True(t, got["us"].SoldOut, "a region with no node is sold out")

	// Paid instances not created yet hold a slot.
	for _, rs := range s.RegionStocks(map[string]int{"a": 3, "b": 1}, map[string]int{"id": 1}) {
		if rs.ID == "id" {
			require.Zero(t, rs.Stock)
			require.True(t, rs.SoldOut)
		}
	}

	// An unlimited node makes the region unlimited.
	s.Nodes = append(s.Nodes, Node{ID: "e", Region: "eu", Enabled: true})
	for _, rs := range s.RegionStocks(nil, nil) {
		if rs.ID == "eu" {
			require.Equal(t, -1, rs.Stock)
			require.False(t, rs.SoldOut)
		}
	}

	// A disabled region is sold out whatever its nodes hold.
	s.Regions[1].Enabled = false
	for _, rs := range s.RegionStocks(nil, nil) {
		if rs.ID == "eu" {
			require.True(t, rs.SoldOut)
		}
	}
}

func TestPeriodPriceAndOvercommit(t *testing.T) {
	s := Settings{Regions: DefaultRegions(),
		Nodes: []Node{{ID: "a", Region: "eu"}}}
	p, ok := s.PeriodPrice("id")
	require.True(t, ok)
	require.EqualValues(t, 20000, p)
	p, ok = s.PeriodPrice(s.InstanceRegion("", "a"))
	require.True(t, ok)
	require.EqualValues(t, 10000, p, "an old instance takes its node's region")
	_, ok = s.PeriodPrice("")
	require.False(t, ok, "no region has no price")
	_, ok = s.PeriodPrice("mars")
	require.False(t, ok)

	full := []NodeLoad{
		{Node: Node{ID: "x", Capacity: 2, Enabled: true}, Used: 4, Free: 0},
		{Node: Node{ID: "y", Capacity: 2, Enabled: true}, Used: 3, Free: 0},
		{Node: Node{ID: "z", Capacity: 2, Enabled: false}, Used: 0, Free: 2},
	}
	_, ok = PickNode(full)
	require.False(t, ok)
	best, ok := PickNodeOvercommit(full)
	require.True(t, ok)
	require.Equal(t, "y", best.ID, "the least overfull enabled node")
}
