package hosting

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Region is a location customers choose at checkout. Each region has its own
// price; its stock is the open slots of the enabled nodes placed in it.
type Region struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PriceIDR int64  `json:"price_idr"`
	Enabled  bool   `json:"enabled"`
}

// DefaultRegions are the regions a fresh install offers.
func DefaultRegions() []Region {
	return []Region{
		{ID: "id", Name: "Indonesia", PriceIDR: 20000, Enabled: true},
		{ID: "eu", Name: "Europe", PriceIDR: 10000, Enabled: true},
	}
}

var regionIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`)

func (s *Settings) normalizeRegions() error {
	if len(s.Regions) == 0 {
		s.Regions = DefaultRegions()
	}
	if len(s.Regions) > 20 {
		return errors.New("at most 20 regions")
	}
	ids := map[string]bool{}
	for i := range s.Regions {
		r := &s.Regions[i]
		r.ID = strings.ToLower(strings.TrimSpace(r.ID))
		r.Name = strings.TrimSpace(r.Name)
		switch {
		case !regionIDRe.MatchString(r.ID):
			return fmt.Errorf("region id %q must be lowercase letters, digits and dashes", r.ID)
		case ids[r.ID]:
			return fmt.Errorf("region id %q is used twice", r.ID)
		case r.Name == "" || len(r.Name) > 60:
			return fmt.Errorf("region %s needs a name (max 60 characters)", r.ID)
		case r.PriceIDR < 0 || r.PriceIDR > 100_000_000:
			return fmt.Errorf("region %s: price must be 0-100000000", r.Name)
		}
		ids[r.ID] = true
	}
	for _, n := range s.Nodes {
		if n.Region != "" && !ids[n.Region] {
			return fmt.Errorf("node %s is in unknown region %q", n.Name, n.Region)
		}
	}
	return nil
}

// Region returns the region with id.
func (s Settings) Region(id string) (Region, bool) {
	for _, r := range s.Regions {
		if r.ID == id {
			return r, true
		}
	}
	return Region{}, false
}

// InstanceRegion is the region an instance belongs to: its own, or the
// region of the node it runs on (instances from before regions existed).
func (s Settings) InstanceRegion(region, nodeID string) string {
	if region != "" {
		return region
	}
	if n, ok := s.Node(nodeID); ok {
		return n.Region
	}
	return ""
}

// PeriodPrice is the price of one period in region. An instance with no
// region (made before regions existed) pays the legacy single price. ok is
// false when there is no usable price.
func (s Settings) PeriodPrice(region string) (int64, bool) {
	if r, ok := s.Region(region); ok && r.PriceIDR > 0 {
		return r.PriceIDR, true
	}
	if region == "" && s.MonthlyPriceIDR > 0 {
		return s.MonthlyPriceIDR, true
	}
	return 0, false
}

// RegionStock is how many new instances a region can still take.
type RegionStock struct {
	Region
	// Capacity is the slots of the region's enabled nodes; -1 when one of
	// them has no limit.
	Capacity int `json:"capacity"`
	// Used counts instances on the region's nodes; Reserved counts paid
	// instances not created yet.
	Used     int `json:"used"`
	Reserved int `json:"reserved"`
	// Stock is the open slots, or -1 for unlimited. 0 means sold out.
	Stock   int  `json:"stock"`
	SoldOut bool `json:"sold_out"`
	Nodes   int  `json:"nodes"` // enabled nodes in the region
}

// RegionStocks totals every region's stock. used maps node id to instances
// on it; reserved maps region id to paid instances waiting to be created.
func (s Settings) RegionStocks(used map[string]int, reserved map[string]int) []RegionStock {
	out := make([]RegionStock, 0, len(s.Regions))
	for _, r := range s.Regions {
		st := RegionStock{Region: r, Reserved: reserved[r.ID]}
		unlimited := false
		for _, n := range s.Nodes {
			if n.Region != r.ID {
				continue
			}
			st.Used += used[n.ID]
			if !n.Enabled {
				continue
			}
			st.Nodes++
			if n.Capacity == 0 {
				unlimited = true
			} else {
				st.Capacity += n.Capacity
			}
		}
		switch {
		case st.Nodes == 0:
			st.Stock = 0
		case unlimited:
			st.Capacity, st.Stock = -1, -1
		default:
			// Instances on disabled nodes do not use enabled slots.
			onEnabled := 0
			for _, n := range s.Nodes {
				if n.Region == r.ID && n.Enabled {
					onEnabled += used[n.ID]
				}
			}
			st.Stock = max(st.Capacity-onEnabled-st.Reserved, 0)
		}
		st.SoldOut = !r.Enabled || st.Stock == 0
		out = append(out, st)
	}
	return out
}

// InRegion keeps the loads of nodes in region. An empty region keeps all.
func InRegion(loads []NodeLoad, region string) []NodeLoad {
	if region == "" {
		return loads
	}
	out := make([]NodeLoad, 0, len(loads))
	for _, l := range loads {
		if l.Region == region {
			out = append(out, l)
		}
	}
	return out
}

// PickNodeOvercommit is PickNode for when every node is full and the admin
// forces it: the enabled node with the fewest instances over its capacity.
func PickNodeOvercommit(loads []NodeLoad) (NodeLoad, bool) {
	if best, ok := PickNode(loads); ok {
		return best, true
	}
	var best NodeLoad
	found := false
	over := func(l NodeLoad) int { return l.Used - l.Capacity }
	for _, l := range loads {
		if !l.Enabled {
			continue
		}
		if !found || over(l) < over(best) {
			best, found = l, true
		}
	}
	return best, found
}
