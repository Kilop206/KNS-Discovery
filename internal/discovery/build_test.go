package discovery

import (
	"encoding/json"
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


func TestSnapshotSerializationConformsToTopologyV1ProducerContract(t *testing.T) {
	result, err := Build(fixture(), Options{Bandwidth: 100, Delay: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["schema_version"] != "1.0" {
		t.Fatalf("unexpected schema_version: %v", document["schema_version"])
	}
	nodes, ok := document["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Fatal("nodes must serialize as a non-empty array")
	}
	links, ok := document["links"].([]any)
	if !ok {
		t.Fatal("links must serialize as an array")
	}
	for _, value := range nodes {
		node := value.(map[string]any)
		if _, ok := node["id"].(float64); !ok {
			t.Fatalf("node missing numeric id: %v", node)
		}
		if kind, ok := node["type"].(string); !ok || !ValidDeviceType(kind) {
			t.Fatalf("invalid node type: %v", node["type"])
		}
	}
	for _, value := range links {
		link := value.(map[string]any)
		if link["bandwidth"].(float64) <= 0 || link["delay"].(float64) < 0 {
			t.Fatalf("invalid serialized link metrics: %v", link)
		}
		loss := link["loss"].(float64)
		if loss < 0 || loss > 1 {
			t.Fatalf("invalid serialized link loss: %v", loss)
		}
		if _, ok := link["inferred"].(bool); !ok {
			t.Fatalf("inferred must serialize as boolean: %v", link)
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
