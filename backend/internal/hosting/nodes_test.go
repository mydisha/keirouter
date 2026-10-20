package hosting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeNodes(t *testing.T) {
	s := Settings{Nodes: []Node{
		{ID: "n1", Name: "Node 1", RootDomain: "*.N1.Example.com", Enabled: true},
		{ID: "n2", Name: "Node 2", RootDomain: "n2.example.com", Capacity: 20},
	}}
	require.NoError(t, s.Normalize())
	require.Equal(t, "n1.example.com", s.Nodes[0].RootDomain)

	for name, bad := range map[string][]Node{
		"dup id":     {{ID: "a", Name: "A", RootDomain: "a.example.com"}, {ID: "a", Name: "B", RootDomain: "b.example.com"}},
		"dup domain": {{ID: "a", Name: "A", RootDomain: "x.example.com"}, {ID: "b", Name: "B", RootDomain: "x.example.com"}},
		"bad id":     {{ID: "Bad ID", Name: "A", RootDomain: "a.example.com"}},
		"no name":    {{ID: "a", RootDomain: "a.example.com"}},
		"bad domain": {{ID: "a", Name: "A", RootDomain: "nodot"}},
		"capacity":   {{ID: "a", Name: "A", RootDomain: "a.example.com", Capacity: -1}},
	} {
		st := Settings{Nodes: bad}
		require.Error(t, st.Normalize(), name)
	}

	off := Settings{Enabled: true, DokployURL: "https://d.example.com", APIKey: "k", EnvironmentID: "e",
		Images: []string{"x:1"}, Nodes: []Node{{ID: "a", Name: "A", RootDomain: "a.example.com"}}}
	require.ErrorContains(t, off.Normalize(), "enabled node")
}

func TestPickNode(t *testing.T) {
	s := Settings{Nodes: []Node{
		{ID: "full", Capacity: 2, Enabled: true},
		{ID: "roomy", Capacity: 10, Enabled: true},
		{ID: "off", Capacity: 100, Enabled: false},
		{ID: "small", Capacity: 5, Enabled: true},
	}}
	loads := s.Loads(map[string]int{"full": 2, "roomy": 7, "small": 1})
	got, ok := PickNode(loads)
	require.True(t, ok)
	require.Equal(t, "small", got.ID, "4 free beats 3 free; full and disabled nodes are skipped")
	require.Equal(t, 0, loads[0].Free)
	require.False(t, loads[0].Available)

	_, ok = PickNode(Settings{Nodes: []Node{{ID: "x", Capacity: 1, Enabled: true}}}.Loads(map[string]int{"x": 1}))
	require.False(t, ok, "every node full")

	unl := Settings{Nodes: []Node{{ID: "a", Enabled: true}, {ID: "b", Enabled: true}, {ID: "c", Capacity: 50, Enabled: true}}}
	got, ok = PickNode(unl.Loads(map[string]int{"a": 9, "b": 3}))
	require.True(t, ok)
	require.Equal(t, "b", got.ID)
	require.Equal(t, -1, got.Free)
}
