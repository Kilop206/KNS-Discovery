package discovery

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"testing"
)

func fixture() Observation {
	return Observation{
		Hostname: "test-host",
		Interfaces: []Interface{{Index: 7, Name: "Ethernet", Prefixes: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.10/24"), netip.MustParsePrefix("fe80::10/64"),
		}}},
		Neighbors: []Neighbor{
			{7, netip.MustParseAddr("192.0.2.1"), "02-00-00-00-00-01", "Reachable"},
			{7, netip.MustParseAddr("192.0.2.20"), "02:00:00:00:00:20", "Stale"},
			{7, netip.MustParseAddr("fe80::20"), "02:00:00:00:00:20", "Reachable"},
			{7, netip.MustParseAddr("192.0.2.30"), "00:00:00:00:00:00", "Unreachable"},
			{7, netip.MustParseAddr("224.0.0.1"), "01:00:5e:00:00:01", "Permanent"},
		},
		Gateways: []Gateway{{7, netip.MustParseAddr("192.0.2.1")}},
	}
}

func TestBuildLogicalTopology(t *testing.T) {
	result, err := Build(fixture(), Options{Bandwidth: 100, Delay: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 5 || len(result.Links) != 5 {
		t.Fatalf("unexpected graph: %+v", result)
	}
	types := map[string]int{}
	for _, node := range result.Nodes {
		types[node.Type]++
		if node.Type == "unknown" && len(node.Addresses) != 2 {
			t.Fatal("IPv4/IPv6 identity not merged")
		}
	}
	if types["computer"] != 1 || types["router"] != 1 || types["network_segment"] != 2 || types["unknown"] != 1 {
		t.Fatal(types)
	}
	for _, link := range result.Links {
		if !link.Inferred || link.Evidence != "shared_segment" {
			t.Fatal("link claimed as physical")
		}
	}
}

func TestDeterminismAndInventory(t *testing.T) {
	observation := fixture()
	options := Options{Bandwidth: 100, Delay: 1, Inventory: map[string]Override{
		"device:7:Ethernet:02:00:00:00:00:20": {Type: "printer", Label: "Office printer"},
	}}
	first, err := Build(observation, options)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(observation.Neighbors)
	slices.Reverse(observation.Interfaces[0].Prefixes)
	second, err := Build(observation, options)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatalf("nondeterministic output\n%s\n%s", a, b)
	}
	if !slices.ContainsFunc(first.Nodes, func(node Node) bool { return node.Type == "printer" && node.Label == "Office printer" }) {
		t.Fatal("override not applied")
	}
}

func TestValidationAndInterfaceIsolation(t *testing.T) {
	for _, options := range []Options{
		{Bandwidth: 0}, {Bandwidth: math.Inf(1)}, {Bandwidth: 100, Delay: -1},
		{Bandwidth: 100, Interface: "missing"},
		{Bandwidth: 100, Inventory: map[string]Override{"x": {Type: "magic"}}},
	} {
		if _, err := Build(fixture(), options); err == nil {
			t.Fatalf("accepted invalid options: %+v", options)
		}
	}
	observation := fixture()
	observation.Neighbors = append(observation.Neighbors, Neighbor{8, netip.MustParseAddr("192.0.2.90"), "02:00:00:00:00:90", "Reachable"})
	result, err := Build(observation, Options{Bandwidth: 100, Interface: "Ethernet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 5 {
		t.Fatal("neighbor leaked across interface scope")
	}
}

func TestDisappearingNeighborIsRemovedWithoutInventingDevices(t *testing.T) {
	observation := fixture()
	observation.Neighbors = nil
	observation.Gateways = nil
	result, err := Build(observation, Options{Bandwidth: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 3 || len(result.Links) != 2 {
		t.Fatal(result)
	}
}
func TestSnapshotNodeLimitMatchesKNS(t *testing.T) {
	observation := Observation{Hostname: "limit-test",
		Interfaces: []Interface{{Index: 1, Name: "test", Prefixes: []netip.Prefix{
			netip.MustParsePrefix("10.0.0.1/16"),
		}}}}
	for i := 2; i < MaxNodes; i++ {
		observation.Neighbors = append(observation.Neighbors, Neighbor{
			1, netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}),
			fmt.Sprintf("02:00:00:00:%02x:%02x", i>>8, i&255), "reachable",
		})
	}
	result, err := Build(observation, Options{Bandwidth: 100, Delay: 1})
	if err != nil || len(result.Nodes) != MaxNodes {
		t.Fatalf("boundary: %d %v", len(result.Nodes), err)
	}
	observation.Neighbors = append(observation.Neighbors, Neighbor{
		1, netip.MustParseAddr("10.0.16.0"), "02:00:00:00:10:00", "reachable",
	})
	if _, err := Build(observation, Options{Bandwidth: 100, Delay: 1}); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
}
