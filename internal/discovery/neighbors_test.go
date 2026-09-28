package discovery

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
)

func TestConflictingNeighborsPreferReachabilityWithStableIdentity(t *testing.T) {
	observation := fixture()
	address := netip.MustParseAddr("192.0.2.20")
	observation.Neighbors = append(observation.Neighbors,
		Neighbor{7, address, "02:00:00:00:00:30", "Reachable"},
		Neighbor{7, address, "02-00-00-00-00-25", "REACHABLE"},
		Neighbor{7, address, "02:00:00:00:00:10", "Stale"},
		Neighbor{7, address, "02:00:00:00:00:01", "Unreachable"},
	)
	identifier := NewIdentifier()
	var calls atomic.Int32
	identifier.lookup = func(context.Context, string) ([]string, error) {
		calls.Add(1)
		return []string{"desktop-test.local"}, nil
	}
	identifier.Enrich(context.Background(), &observation, "")
	first, err := Build(observation, Options{Bandwidth: 100})
	if err != nil {
		t.Fatal(err)
	}
	selected := observedNeighbors(observation)[addressKey(7, address)]
	if selected.MAC != "02:00:00:00:00:25" {
		t.Fatalf("selected stale or unstable MAC: %+v", selected)
	}
	if !slices.ContainsFunc(first.Nodes, func(node Node) bool { return node.ExternalID == "device:7:Ethernet:02:00:00:00:00:25" }) {
		t.Fatal("topology used a different neighbor")
	}
	slices.Reverse(observation.Neighbors)
	identifier.Enrich(context.Background(), &observation, "")
	second, err := Build(observation, Options{Bandwidth: 100})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(first)
	after, _ := json.Marshal(second)
	if string(before) != string(after) {
		t.Fatal("OS enumeration order changed device identity")
	}
	if calls.Load() != 2 {
		t.Fatal("OS enumeration order invalidated DNS cache")
	}
}
