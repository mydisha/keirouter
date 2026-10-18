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
	require.Len(t, s.Nodes, 1, "legacy root_domain becomes the default node")
	require.Equal(t, LegacyNodeID, s.Nodes[0].ID)
	require.Equal(t, "host.example.com", s.Nodes[0].RootDomain)
	require.Empty(t, s.RootDomain)
	require.Equal(t, []string{"a:1"}, s.Images)
	require.Equal(t, DefaultPort, s.ContainerPort)
	require.Equal(t, "https://d.example.com", s.DokployURL)

	require.Equal(t, DefaultRegions(), s.Regions, "a fresh install gets the default regions")

	s.Enabled = true
	require.ErrorContains(t, s.Normalize(), "API key")
	s.APIKey, s.EnvironmentID = "k", "env"
	require.ErrorContains(t, s.Normalize(), "region", "the node is not in a region yet")
	s.Nodes[0].Region = "id"
	require.NoError(t, s.Normalize())
	s.Nodes[0].Region = "mars"
	require.ErrorContains(t, s.Normalize(), "unknown region")

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
	s := Settings{MonthlyPriceIDR: 50000, Regions: DefaultRegions(),
		Nodes: []Node{{ID: "a", Region: "eu"}}}
	p, ok := s.PeriodPrice("id")
	require.True(t, ok)
	require.EqualValues(t, 20000, p)
	p, ok = s.PeriodPrice(s.InstanceRegion("", "a"))
	require.True(t, ok)
	require.EqualValues(t, 10000, p, "an old instance takes its node's region")
	p, ok = s.PeriodPrice("")
	require.True(t, ok)
	require.EqualValues(t, 50000, p, "no region at all keeps the legacy price")
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
