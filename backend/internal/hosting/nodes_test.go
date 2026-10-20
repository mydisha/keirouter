package hosting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeNodes(t *testing.T) {
	s := Settings{Nodes: []Node{
		{ID: "n1", Name: "Node 1", PublicIP: " 203.0.113.7 ", Enabled: true},
		{ID: "n2", Name: "Node 2", PublicIP: "203.0.113.7", Capacity: 20, Enabled: true},
		{ID: "n3", Name: "Node 3"},
	}}
	require.NoError(t, s.Normalize(), "nodes may share an IP; a disabled node may have none")
	require.Equal(t, "203.0.113.7", s.Nodes[0].PublicIP)

	for name, bad := range map[string][]Node{
		"dup id":   {{ID: "a", Name: "A"}, {ID: "a", Name: "B"}},
		"bad id":   {{ID: "Bad ID", Name: "A"}},
		"no name":  {{ID: "a"}},
		"capacity": {{ID: "a", Name: "A", Capacity: -1}},
	} {
		st := Settings{Nodes: bad}
		require.Error(t, st.Normalize(), name)
	}

	off := enableable()
	off.Nodes[0].Enabled = false
	require.ErrorContains(t, off.Normalize(), "enabled node")
}

func TestNodePublicIP(t *testing.T) {
	for _, ip := range []string{"", "10.0.0", "::1", "203.0.113.300"} {
		s := Settings{Nodes: []Node{{ID: "a", Name: "Node A", PublicIP: ip, Enabled: true}}}
		require.ErrorContains(t, s.Normalize(), "Node A", ip)
	}
	s := Settings{Nodes: []Node{{ID: "a", Name: "Node A", PublicIP: "203.0.113.7", Enabled: true}}}
	require.NoError(t, s.Normalize())
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
