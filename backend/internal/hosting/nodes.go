package hosting

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// LegacyNodeID is the node that settings saved before multi-node support
// (a single server_id + root_domain) become, and that instances created
// back then are assigned to.
const LegacyNodeID = "default"

// Node is one Dokploy server that runs customer instances. Every node has its
// own wildcard DNS (*.RootDomain → the node's IP), because one wildcard can
// only point at one server.
type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ServerID string `json:"server_id"` // Dokploy serverId; empty = the Dokploy host itself
	// RootDomain is where this node's random subdomains are created.
	RootDomain string `json:"root_domain"`
	// MonitorTargetID is the monitoring server that reports this node's
	// container metrics.
	MonitorTargetID string `json:"monitor_target_id"`
	// Capacity is the most instances the node takes; 0 means no limit.
	Capacity int  `json:"capacity"`
	Enabled  bool `json:"enabled"`
	// Region is the region the node's slots are sold in; empty means the
	// node is not sold in any region yet.
	Region string `json:"region"`
}

var nodeIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// foldLegacyNode turns the pre-multi-node single server_id/root_domain into
// the "default" node, then clears the legacy fields.
func (s *Settings) foldLegacyNode() {
	if len(s.Nodes) == 0 && (s.RootDomain != "" || s.ServerID != "") {
		s.Nodes = []Node{{
			ID: LegacyNodeID, Name: "Node 1", ServerID: s.ServerID,
			RootDomain: s.RootDomain, Enabled: true,
		}}
	}
	s.RootDomain, s.ServerID = "", ""
}

func (s *Settings) normalizeNodes() error {
	if len(s.Nodes) > 50 {
		return errors.New("at most 50 nodes")
	}
	ids, domains := map[string]bool{}, map[string]bool{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		n.ID = strings.ToLower(strings.TrimSpace(n.ID))
		n.Name = strings.TrimSpace(n.Name)
		n.ServerID = strings.TrimSpace(n.ServerID)
		n.MonitorTargetID = strings.TrimSpace(n.MonitorTargetID)
		n.Region = strings.ToLower(strings.TrimSpace(n.Region))
		n.RootDomain = strings.TrimPrefix(strings.ToLower(strings.Trim(strings.TrimSpace(n.RootDomain), ".")), "*.")
		switch {
		case !nodeIDRe.MatchString(n.ID):
			return fmt.Errorf("node id %q must be lowercase letters, digits and dashes", n.ID)
		case ids[n.ID]:
			return fmt.Errorf("node id %q is used twice", n.ID)
		case n.Name == "" || len(n.Name) > 80:
			return fmt.Errorf("node %s needs a name (max 80 characters)", n.ID)
		case !validDomain(n.RootDomain):
			return fmt.Errorf("node %s: root_domain %q is not a valid domain", n.Name, n.RootDomain)
		case domains[n.RootDomain]:
			return fmt.Errorf("root_domain %q is used by two nodes; each node needs its own wildcard domain", n.RootDomain)
		case n.Capacity < 0 || n.Capacity > 10000:
			return fmt.Errorf("node %s: capacity must be 0-10000", n.Name)
		}
		ids[n.ID], domains[n.RootDomain] = true, true
	}
	return nil
}

// Node returns the node with id.
func (s Settings) Node(id string) (Node, bool) {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// NodeLoad is a node with how many instances occupy it.
type NodeLoad struct {
	Node
	Used int `json:"used"`
	// Free is the open slots, or -1 when the node has no capacity limit.
	Free      int  `json:"free"`
	Available bool `json:"available"` // enabled and not full
}

// Loads pairs every node with its usage. used maps node id to the number of
// instances occupying it.
func (s Settings) Loads(used map[string]int) []NodeLoad {
	out := make([]NodeLoad, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		l := NodeLoad{Node: n, Used: used[n.ID], Free: -1}
		if n.Capacity > 0 {
			l.Free = n.Capacity - l.Used
			if l.Free < 0 {
				l.Free = 0
			}
		}
		l.Available = n.Enabled && (n.Capacity == 0 || l.Free > 0)
		out = append(out, l)
	}
	return out
}

// PickNode returns the available node with the most free room: the most
// open slots, with unlimited nodes ranked by fewest instances. ok is false
// when every node is full or disabled.
func PickNode(loads []NodeLoad) (NodeLoad, bool) {
	var best NodeLoad
	found := false
	// Unlimited nodes win over limited ones; among unlimited nodes the one
	// with fewer instances wins, among limited ones the one with more slots.
	better := func(a, b NodeLoad) bool {
		switch {
		case a.Free == -1 && b.Free == -1:
			return a.Used < b.Used
		case a.Free == -1:
			return true
		case b.Free == -1:
			return false
		default:
			return a.Free > b.Free
		}
	}
	for _, l := range loads {
		if !l.Available {
			continue
		}
		if !found || better(l, best) {
			best, found = l, true
		}
	}
	return best, found
}
