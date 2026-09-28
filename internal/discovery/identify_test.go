package discovery

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNamesClassifyWithoutChangingIdentitiesAndInventoryWins(t *testing.T) {
	observation := fixture()
	observation.Names = map[string]string{
		"7:192.0.2.1":  "desktop-gateway.local",
		"7:192.0.2.20": "office-printer.local",
	}
	options := Options{Bandwidth: 100, Delay: 1}
	result, err := Build(observation, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range result.Nodes {
		switch node.MAC {
		case "02:00:00:00:00:01":
			if node.Type != "router" || node.Evidence != "default_route+reverse_dns" {
				t.Fatalf("lost routing evidence: %+v", node)
			}
		case "02:00:00:00:00:20":
			if node.Label != "office-printer.local" || node.Type != "printer" || !strings.Contains(node.Evidence, "hostname_hint") {
				t.Fatalf("missing name/type: %+v", node)
			}
			if node.ExternalID != "device:7:Ethernet:02:00:00:00:00:20" || len(node.Addresses) != 2 {
				t.Fatal("name changed device identity")
			}
		}
	}
	options.Inventory = map[string]Override{"device:7:Ethernet:02:00:00:00:00:20": {Label: "Manual label", Type: "server"}}
	result, err = Build(observation, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range result.Nodes {
		if node.MAC == "02:00:00:00:00:20" && (node.Label != "Manual label" || node.Type != "server") {
			t.Fatal("DNS overrode inventory")
		}
	}
}

func TestIdentifierCachesPositiveNegativeAndExpiresEntries(t *testing.T) {
	identifier := NewIdentifier()
	now := time.Unix(1000, 0)
	identifier.now = func() time.Time { return now }
	var calls atomic.Int32
	identifier.lookup = func(_ context.Context, address string) ([]string, error) {
		calls.Add(1)
		if address == "192.0.2.20" {
			return []string{"Z-name.local.", "Office-Printer.local."}, nil
		}
		return nil, errors.New("no PTR record")
	}
	observation := fixture()
	identifier.Enrich(context.Background(), &observation, "Ethernet")
	if calls.Load() != 2 || observation.Names["7:192.0.2.20"] != "office-printer.local" {
		t.Fatalf("unexpected resolution: %v calls=%d", observation.Names, calls.Load())
	}
	identifier.Enrich(context.Background(), &observation, "Ethernet")
	if calls.Load() != 2 {
		t.Fatal("cache did not suppress repeated queries")
	}
	now = now.Add(2 * time.Minute)
	identifier.Enrich(context.Background(), &observation, "Ethernet")
	if calls.Load() != 3 {
		t.Fatal("negative cache was not refreshed independently")
	}
	now = now.Add(10 * time.Minute)
	identifier.Enrich(context.Background(), &observation, "Ethernet")
	if calls.Load() != 5 {
		t.Fatal("positive cache did not expire")
	}
	observation.Neighbors[1].MAC = "02:00:00:00:00:99"
	identifier.Enrich(context.Background(), &observation, "Ethernet")
	if calls.Load() != 6 {
		t.Fatal("reused a cached name after IP reassignment")
	}
	identifier.Enrich(context.Background(), &observation, "missing")
	if calls.Load() != 6 || len(observation.Names) != 0 || len(identifier.cache) != 0 {
		t.Fatal("names leaked across interface selection")
	}
}

func TestIdentifierLimitsConcurrencyAndHonorsCancellation(t *testing.T) {
	identifier := NewIdentifier()
	identifier.workers = 2
	observation := fixture()
	for i := byte(40); i < 50; i++ {
		observation.Neighbors = append(observation.Neighbors, Neighbor{7, netip.AddrFrom4([4]byte{192, 0, 2, i}), "02:00:00:00:00:40", "Reachable"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 20)
	var active atomic.Int32
	var maximum atomic.Int32
	identifier.lookup = func(ctx context.Context, _ string) ([]string, error) {
		count := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); count > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, count) {
				break
			}
		}
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan struct{})
	go func() { identifier.Enrich(ctx, &observation, ""); close(done) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("queries did not run in parallel")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop lookups")
	}
	if maximum.Load() != 2 || active.Load() != 0 {
		t.Fatal("worker limit or cleanup failed")
	}
}

func TestIdentifierBudgetKeepsUnresolvedDevices(t *testing.T) {
	identifier := NewIdentifier()
	identifier.budget = 10 * time.Millisecond
	identifier.lookup = func(ctx context.Context, _ string) ([]string, error) { <-ctx.Done(); return nil, ctx.Err() }
	observation := fixture()
	identifier.Enrich(context.Background(), &observation, "")
	result, err := Build(observation, Options{Bandwidth: 100})
	if err != nil || len(result.Nodes) != 5 {
		t.Fatalf("DNS failure removed devices: %+v %v", result, err)
	}
}

func TestHostnameHintsAreConservative(t *testing.T) {
	for name, expected := range map[string]string{
		"iphone-ana.local": "phone", "desktop-123.example": "computer",
		"office-printer.local": "printer", "synology-nas.local": "server",
		"roku.local": "iot", "pineapple.local": "unknown",
		"192-168-1-1.isp.example": "unknown", "printerish.local": "unknown",
	} {
		if got := typeFromHostname(name); got != expected {
			t.Errorf("%s: got %s, want %s", name, got, expected)
		}
	}
	if preferredName([]string{"192.0.2.1", "bad\nname", "valid.local."}) != "valid.local" {
		t.Fatal("invalid resolved name accepted")
	}
}

func TestIdentifierRetainsNamesOnlyForBoundedTransientFailures(t *testing.T) {
	for _, failure := range []struct {
		name   string
		err    error
		retain bool
	}{
		{"timeout", context.DeadlineExceeded, true},
		{"temporary", &net.DNSError{IsTemporary: true}, true},
		{"missing", &net.DNSError{IsNotFound: true}, false},
		{"empty", nil, false},
	} {
		t.Run(failure.name, func(t *testing.T) {
			identifier := NewIdentifier()
			now := time.Unix(1000, 0)
			identifier.now = func() time.Time { return now }
			identifier.lookup = func(context.Context, string) ([]string, error) { return []string{"office-printer.local"}, nil }
			observation := fixture()
			identifier.Enrich(context.Background(), &observation, "")
			identifier.lookup = func(context.Context, string) ([]string, error) { return nil, failure.err }
			now = now.Add(11 * time.Minute)
			identifier.Enrich(context.Background(), &observation, "")
			if (observation.Names["7:192.0.2.20"] != "") != failure.retain {
				t.Fatalf("unexpected refresh result: %v", observation.Names)
			}
			now = now.Add(20 * time.Minute)
			identifier.Enrich(context.Background(), &observation, "")
			if len(observation.Names) != 0 {
				t.Fatal("stale names survived the maximum retention period")
			}
			identifier.lookup = func(context.Context, string) ([]string, error) { return []string{"renamed-printer.local"}, nil }
			now = now.Add(2 * time.Minute)
			identifier.Enrich(context.Background(), &observation, "")
			if observation.Names["7:192.0.2.20"] != "renamed-printer.local" {
				t.Fatal("resolution did not recover")
			}
		})
	}
}

func TestIdentifierKeepsCachedNameWhenRefreshBudgetExpires(t *testing.T) {
	identifier := NewIdentifier()
	now := time.Unix(1000, 0)
	identifier.now = func() time.Time { return now }
	identifier.lookup = func(context.Context, string) ([]string, error) { return []string{"office-printer.local"}, nil }
	observation := fixture()
	identifier.Enrich(context.Background(), &observation, "")
	now = now.Add(11 * time.Minute)
	identifier.budget = time.Millisecond
	identifier.lookup = func(ctx context.Context, _ string) ([]string, error) { <-ctx.Done(); return nil, ctx.Err() }
	identifier.Enrich(context.Background(), &observation, "")
	if observation.Names["7:192.0.2.20"] != "office-printer.local" {
		t.Fatal("refresh deadline discarded an existing name")
	}
}
