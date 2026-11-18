package discovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DecodeSnapshot validates the fields used to establish a previous observation
// and to compare node and link identities.
func DecodeSnapshot(data []byte) (Snapshot, error) {
	// Use pointers for required integer fields so omitted values cannot become
	// node 0 or accidentally refer to it.
	var input struct {
		Snapshot
		Nodes []struct {
			Node
			ID *int `json:"id"`
		} `json:"nodes"`
		Links []struct {
			Link
			From *int `json:"from"`
			To   *int `json:"to"`
		} `json:"links"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return Snapshot{}, err
	}
	snapshot := input.Snapshot
	if snapshot.SchemaVersion != snapshotSchemaVersion {
		return Snapshot{}, fmt.Errorf("unsupported snapshot schema_version %q", snapshot.SchemaVersion)
	}
	if len(input.Nodes) == 0 || len(input.Nodes) > MaxNodes || input.Links == nil {
		return Snapshot{}, fmt.Errorf("snapshot requires 1 to %d nodes and a links array", MaxNodes)
	}
	if snapshot.ObservedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt); err != nil ||
			!strings.HasSuffix(snapshot.ObservedAt, "Z") || strings.Contains(snapshot.ObservedAt, ",") {
			return Snapshot{}, fmt.Errorf("snapshot observed_at must be a UTC timestamp")
		}
	}
	snapshot.Nodes = make([]Node, 0, len(input.Nodes))
	ids := make(map[int]bool, len(input.Nodes))
	identities := make(map[string]bool, len(input.Nodes))
	for _, entry := range input.Nodes {
		if entry.ID == nil || *entry.ID < 0 || ids[*entry.ID] || entry.ExternalID == "" || identities[entry.ExternalID] {
			return Snapshot{}, fmt.Errorf("snapshot contains an invalid or duplicate node identity")
		}
		node := entry.Node
		node.ID = *entry.ID
		ids[node.ID] = true
		identities[node.ExternalID] = true
		snapshot.Nodes = append(snapshot.Nodes, node)
	}
	snapshot.Links = make([]Link, 0, len(input.Links))
	links := make(map[[2]int]bool, len(input.Links))
	for _, entry := range input.Links {
		if entry.From == nil || entry.To == nil {
			return Snapshot{}, fmt.Errorf("snapshot contains a link without two endpoints")
		}
		link := entry.Link
		link.From, link.To = *entry.From, *entry.To
		pair := [2]int{min(link.From, link.To), max(link.From, link.To)}
		if !ids[link.From] || !ids[link.To] || link.From == link.To || links[pair] {
			return Snapshot{}, fmt.Errorf("snapshot contains an invalid or duplicate link between %d and %d", link.From, link.To)
		}
		if link.Bandwidth <= 0 || link.Delay < 0 || link.Loss < 0 || link.Loss > 1 {
			return Snapshot{}, fmt.Errorf("snapshot contains invalid link metrics between %d and %d", link.From, link.To)
		}
		links[pair] = true
		snapshot.Links = append(snapshot.Links, link)
	}
	return snapshot, nil
}
