package discovery

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

func usableAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsUnspecified() && !address.IsLoopback() && !address.IsMulticast()
}

func normalizedMAC(value string) string {
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return ""
	}
	if slices.Equal(mac, []byte{0, 0, 0, 0, 0, 0}) {
		return ""
	}
	return mac.String()
}

// ValidateOptions checks configuration independently of network availability.
func ValidateOptions(options Options) error {
	if options.Bandwidth <= 0 || math.IsNaN(options.Bandwidth) || math.IsInf(options.Bandwidth, 0) ||
		options.Delay < 0 || math.IsNaN(options.Delay) || math.IsInf(options.Delay, 0) {
		return fmt.Errorf("simulation bandwidth must be finite and positive; delay must be finite and nonnegative")
	}
	for identity, override := range options.Inventory {
		if override.Type != "" && !ValidDeviceType(override.Type) {
			return fmt.Errorf("inventory %q has unknown device type %q", identity, override.Type)
		}
	}
	return nil
}

// Build produces a deterministic logical adjacency graph. It never claims that
// a neighbor cache identifies physical cables or a neighbor's hardware class.
func Build(observation Observation, options Options) (Snapshot, error) {
	if err := ValidateOptions(options); err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{SchemaVersion: "1.0", Name: "Local network", Nodes: []Node{}, Links: []Link{}}
	nodes := map[string]Node{}
	edges := map[[2]string]bool{}
	host := "host:" + observation.Hostname
	nodes[host] = Node{ExternalID: host, Label: observation.Hostname, Type: "computer", Evidence: "local_interface"}
	interfaces := map[int]Interface{}
	segments := map[int][]netip.Prefix{}
	segmentID := func(index int, prefix netip.Prefix) string {
		iface := interfaces[index]
		return "segment:" + strconv.Itoa(index) + ":" + iface.Name + ":" + prefix.Masked().String()
	}
	for _, iface := range observation.Interfaces {
		if options.Interface != "" && iface.Name != options.Interface {
			continue
		}
		interfaces[iface.Index] = iface
		for _, prefix := range iface.Prefixes {
			if !prefix.IsValid() || !usableAddress(prefix.Addr()) {
				continue
			}
			local := nodes[host]
			local.Addresses = append(local.Addresses, prefix.Addr().String())
			nodes[host] = local
			prefix = prefix.Masked()
			segments[iface.Index] = append(segments[iface.Index], prefix)
			identity := segmentID(iface.Index, prefix)
			nodes[identity] = Node{ExternalID: identity, Label: iface.Name + " " + prefix.String(), Type: "network_segment", Evidence: "interface_prefix"}
			edges[[2]string{host, identity}] = true
		}
	}
	if len(segments) == 0 {
		return Snapshot{}, fmt.Errorf("no active IP interfaces match %q", options.Interface)
	}
	neighborByAddress := map[string]Neighbor{}
	addressKey := func(index int, address netip.Addr) string { return strconv.Itoa(index) + ":" + address.String() }
	for _, neighbor := range observation.Neighbors {
		switch strings.ToLower(neighbor.State) {
		case "reachable", "stale", "delay", "probe", "permanent":
		default:
			continue
		}
		if usableAddress(neighbor.Address) && normalizedMAC(neighbor.MAC) != "" {
			neighborByAddress[addressKey(neighbor.Interface, neighbor.Address)] = neighbor
		}
	}
	addEndpoint := func(index int, address netip.Addr, gateway bool) {
		if !usableAddress(address) {
			return
		}
		iface, ok := interfaces[index]
		if !ok {
			return
		}
		var selected netip.Prefix
		for _, prefix := range segments[index] {
			if prefix.Contains(address) && (!selected.IsValid() || prefix.Bits() > selected.Bits()) {
				selected = prefix
			}
		}
		if !selected.IsValid() {
			result.Warnings = append(result.Warnings, "No local prefix for observed endpoint on "+iface.Name)
			return
		}
		for _, prefix := range iface.Prefixes {
			if prefix.Addr() == address {
				return
			}
		}
		neighbor := neighborByAddress[addressKey(index, address)]
		mac := normalizedMAC(neighbor.MAC)
		identity := "endpoint:" + strconv.Itoa(index) + ":" + iface.Name + ":" + address.String()
		if mac != "" {
			identity = "device:" + strconv.Itoa(index) + ":" + iface.Name + ":" + mac
		}
		node, exists := nodes[identity]
		if !exists {
			node = Node{ExternalID: identity, Label: address.String(), Type: "unknown", MAC: mac, Evidence: "neighbor_cache"}
		}
		node.Addresses = append(node.Addresses, address.String())
		if gateway {
			node.Type = "router"
			node.Evidence = "default_route"
		}
		nodes[identity] = node
		edges[[2]string{identity, segmentID(index, selected)}] = true
	}
	// Sort inputs so labels and output remain stable even if the OS changes order.
	keys := make([]string, 0, len(neighborByAddress))
	for key := range neighborByAddress {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		neighbor := neighborByAddress[key]
		addEndpoint(neighbor.Interface, neighbor.Address, false)
	}
	gateways := slices.Clone(observation.Gateways)
	slices.SortFunc(gateways, func(a, b Gateway) int {
		return strings.Compare(addressKey(a.Interface, a.Address), addressKey(b.Interface, b.Address))
	})
	for _, gateway := range gateways {
		addEndpoint(gateway.Interface, gateway.Address, true)
	}
	keys = keys[:0]
	for key := range nodes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	ids := map[string]int{}
	for _, key := range keys {
		node := nodes[key]
		node.ID = len(result.Nodes)
		ids[key] = node.ID
		slices.Sort(node.Addresses)
		node.Addresses = slices.Compact(node.Addresses)
		if override, ok := options.Inventory[key]; ok {
			if override.Label != "" {
				node.Label = override.Label
			}
			if override.Type != "" {
				node.Type = override.Type
			}
			node.Evidence += "+user_inventory"
		}
		result.Nodes = append(result.Nodes, node)
	}
	for edge := range edges {
		from, to := ids[edge[0]], ids[edge[1]]
		if from > to {
			from, to = to, from
		}
		result.Links = append(result.Links, Link{From: from, To: to, Bandwidth: options.Bandwidth, Delay: options.Delay, Inferred: true, Evidence: "shared_segment"})
	}
	slices.SortFunc(result.Links, func(a, b Link) int {
		if a.From != b.From {
			return a.From - b.From
		}
		return a.To - b.To
	})
	slices.Sort(result.Warnings)
	result.Warnings = slices.Compact(result.Warnings)
	return result, nil
}
