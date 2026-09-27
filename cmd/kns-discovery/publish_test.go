package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kns.local/discovery/internal/discovery"
)

func fixtureCollector(context.Context) (discovery.Observation, error) {
	return discovery.Observation{
		Hostname: "example",
		Interfaces: []discovery.Interface{{Index: 1, Name: "Ethernet",
			Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}}},
	}, nil
}

func writeInventory(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPublishReloadsAndRemovesInventoryOverrides(t *testing.T) {
	directory := t.TempDir()
	inventory := filepath.Join(directory, "inventory.json")
	output := filepath.Join(directory, "network.json")
	options := discovery.Options{Bandwidth: 100, Delay: 1}
	for _, step := range []struct{ inventory, label, kind string }{
		{`{"host:example":{"label":"Workstation","type":"server"}}`, "Workstation", "server"},
		{`{"host:example":{"label":"New name","type":"computer"}}`, "New name", "computer"},
		{`{}`, "example", "computer"},
	} {
		writeInventory(t, inventory, step.inventory)
		if err := publishSnapshot(context.Background(), output, inventory, options, fixtureCollector); err != nil {
			t.Fatal(err)
		}
		var snapshot discovery.Snapshot
		if err := json.Unmarshal(readBytes(t, output), &snapshot); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, node := range snapshot.Nodes {
			if node.ExternalID == "host:example" {
				found = true
				if node.Label != step.label || node.Type != step.kind {
					t.Fatalf("unexpected override: %+v", node)
				}
				if step.inventory == "{}" && strings.Contains(node.Evidence, "user_inventory") {
					t.Fatal("removed override still appears in evidence")
				}
			}
		}
		if !found {
			t.Fatal("host missing from published snapshot")
		}
	}
	if options.Inventory != nil {
		t.Fatal("publication mutated caller options")
	}
}

func TestPublishRetainsSnapshotOnInventoryErrorsAndRecovers(t *testing.T) {
	directory := t.TempDir()
	inventory := filepath.Join(directory, "inventory.json")
	output := filepath.Join(directory, "network.json")
	options := discovery.Options{Bandwidth: 100, Delay: 1}
	writeInventory(t, inventory, `{}`)
	if err := publishSnapshot(context.Background(), output, inventory, options, fixtureCollector); err != nil {
		t.Fatal(err)
	}
	original := readBytes(t, output)
	for _, contents := range []string{`{partial`, `null`, `[]`, `{"host:example":{"type":"magic"}}`} {
		writeInventory(t, inventory, contents)
		if err := publishSnapshot(context.Background(), output, inventory, options, fixtureCollector); err == nil {
			t.Fatalf("accepted invalid inventory %q", contents)
		}
		if !bytes.Equal(original, readBytes(t, output)) {
			t.Fatal("invalid inventory changed the published snapshot")
		}
	}
	if err := os.Remove(inventory); err != nil {
		t.Fatal(err)
	}
	if err := publishSnapshot(context.Background(), output, inventory, options, fixtureCollector); err == nil {
		t.Fatal("accepted missing inventory")
	}
	if !bytes.Equal(original, readBytes(t, output)) {
		t.Fatal("missing inventory changed the published snapshot")
	}
	writeInventory(t, inventory, `{"host:example":{"label":"Recovered"}}`)
	if err := publishSnapshot(context.Background(), output, inventory, options, fixtureCollector); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(readBytes(t, output), []byte("Recovered")) {
		t.Fatal("corrected inventory was not applied")
	}
}

func TestPublishDoesNotReplaceSnapshotAfterCollectionFailureOrCancellation(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network.json")
	options := discovery.Options{Bandwidth: 100, Delay: 1}
	if err := publishSnapshot(context.Background(), output, "", options, fixtureCollector); err != nil {
		t.Fatal(err)
	}
	original := readBytes(t, output)
	failure := errors.New("network unavailable")
	if err := publishSnapshot(context.Background(), output, "", options, func(context.Context) (discovery.Observation, error) {
		return discovery.Observation{}, failure
	}); !errors.Is(err, failure) {
		t.Fatalf("lost collection error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := publishSnapshot(ctx, output, "", options, func(ctx context.Context) (discovery.Observation, error) {
		cancel()
		return fixtureCollector(ctx)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("publication ignored cancellation: %v", err)
	}
	if !bytes.Equal(original, readBytes(t, output)) {
		t.Fatal("failed or canceled collection changed the snapshot")
	}
}
