package hosting

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Node is one Dokploy server that runs customer instances. Each instance on
// it gets a Cloudflare A record pointing at PublicIP.
type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ServerID string `json:"server_id"` // Dokploy serverId; empty = the Dokploy host itself
	// PublicIP is the node's IPv4 address, the target of its instances'
	// DNS records.
	PublicIP string `json:"public_ip"`
	// Capacity is the most instances the node takes; 0 means no limit.
	Capacity int  `json:"capacity"`
	Enabled  bool `json:"enabled"`
	// Region is the region the node's slots are sold in; empty means the
	// node is not sold in any region yet.
	Region string `json:"region"`
}

var nodeIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

func (s *Settings) normalizeNodes() error {
	if len(s.Nodes) > 50 {
		return errors.New("at most 50 nodes")
	}
	ids := map[string]bool{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		n.ID = strings.ToLower(strings.TrimSpace(n.ID))
		n.Name = strings.TrimSpace(n.Name)
		n.ServerID = strings.TrimSpace(n.ServerID)
		n.Region = strings.ToLower(strings.TrimSpace(n.Region))
		n.PublicIP = strings.TrimSpace(n.PublicIP)
		switch {
		case !nodeIDRe.MatchString(n.ID):
			return fmt.Errorf("node id %q must be lowercase letters, digits and dashes", n.ID)
		case ids[n.ID]:
			return fmt.Errorf("node id %q is used twice", n.ID)
		case n.Name == "" || len(n.Name) > 80:
			return fmt.Errorf("node %s needs a name (max 80 characters)", n.ID)
		case n.Enabled && !validIPv4(n.PublicIP):
			return fmt.Errorf("node %s: public_ip %q must be an IPv4 address", n.Name, n.PublicIP)
		case n.Capacity < 0 || n.Capacity > 10000:
			return fmt.Errorf("node %s: capacity must be 0-10000", n.Name)
		}
		ids[n.ID] = true
	}
	return nil
}

func validIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && !strings.Contains(s, ":")
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
