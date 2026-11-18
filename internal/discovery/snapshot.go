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
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.SchemaVersion != snapshotSchemaVersion {
		return Snapshot{}, fmt.Errorf("unsupported snapshot schema_version %q", snapshot.SchemaVersion)
	}
	if len(snapshot.Nodes) == 0 || len(snapshot.Nodes) > MaxNodes || snapshot.Links == nil {
		return Snapshot{}, fmt.Errorf("snapshot requires 1 to %d nodes and a links array", MaxNodes)
	}
	if snapshot.ObservedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt); err != nil ||
			!strings.HasSuffix(snapshot.ObservedAt, "Z") || strings.Contains(snapshot.ObservedAt, ",") {
			return Snapshot{}, fmt.Errorf("snapshot observed_at must be a UTC timestamp")
		}
	}
	ids := make(map[int]bool, len(snapshot.Nodes))
	identities := make(map[string]bool, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.ID < 0 || ids[node.ID] || node.ExternalID == "" || identities[node.ExternalID] {
			return Snapshot{}, fmt.Errorf("snapshot contains an invalid or duplicate node identity")
		}
		ids[node.ID] = true
		identities[node.ExternalID] = true
	}
	links := make(map[[2]int]bool, len(snapshot.Links))
	for _, link := range snapshot.Links {
		pair := [2]int{min(link.From, link.To), max(link.From, link.To)}
		if !ids[link.From] || !ids[link.To] || link.From == link.To || links[pair] {
			return Snapshot{}, fmt.Errorf("snapshot contains an invalid or duplicate link between %d and %d", link.From, link.To)
		}
		if link.Bandwidth <= 0 || link.Delay < 0 || link.Loss < 0 || link.Loss > 1 {
			return Snapshot{}, fmt.Errorf("snapshot contains invalid link metrics between %d and %d", link.From, link.To)
		}
		links[pair] = true
	}
	return snapshot, nil
}
