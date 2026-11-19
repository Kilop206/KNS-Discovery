package discovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The wire fields use pointers to distinguish missing IDs from node 0, which
// is a valid identity. Once decoded, the snapshot can use ordinary integers.
type snapshotDocument struct {
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

// DecodeSnapshot checks whether a previous observation can serve as evidence
// for additions and removals in the next diff.
func DecodeSnapshot(data []byte) (Snapshot, error) {
	var input snapshotDocument
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
		// Go's parser accepts some forms outside the contract, including comma
		// fractions. Reusing one would publish an invalid timestamp on the next poll.
		if _, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt); err != nil ||
			!strings.HasSuffix(snapshot.ObservedAt, "Z") || strings.Contains(snapshot.ObservedAt, ",") {
			return Snapshot{}, fmt.Errorf("snapshot observed_at must be a UTC timestamp")
		}
	}

	snapshot.Nodes = make([]Node, 0, len(input.Nodes))
	seenNodeIDs := make(map[int]bool, len(input.Nodes))
	seenExternalIDs := make(map[string]bool, len(input.Nodes))
	for _, entry := range input.Nodes {
		if entry.ID == nil || *entry.ID < 0 || seenNodeIDs[*entry.ID] ||
			entry.ExternalID == "" || seenExternalIDs[entry.ExternalID] {
			return Snapshot{}, fmt.Errorf("snapshot contains an invalid or duplicate node identity")
		}
		node := entry.Node
		node.ID = *entry.ID
		seenNodeIDs[node.ID] = true
		seenExternalIDs[node.ExternalID] = true
		snapshot.Nodes = append(snapshot.Nodes, node)
	}

	snapshot.Links = make([]Link, 0, len(input.Links))
	seenLinkPairs := make(map[[2]int]bool, len(input.Links))
	for _, entry := range input.Links {
		if entry.From == nil || entry.To == nil {
			return Snapshot{}, fmt.Errorf("snapshot contains a link without two endpoints")
		}
		link := entry.Link
		link.From, link.To = *entry.From, *entry.To
		// The diff keys inferred links by an unordered pair. Reject duplicates
		// here so one observation cannot silently replace another in that map.
		pair := [2]int{min(link.From, link.To), max(link.From, link.To)}
		if !seenNodeIDs[link.From] || !seenNodeIDs[link.To] || link.From == link.To || seenLinkPairs[pair] {
			return Snapshot{}, fmt.Errorf("snapshot contains an invalid or duplicate link between %d and %d", link.From, link.To)
		}
		if link.Bandwidth <= 0 || link.Delay < 0 || link.Loss < 0 || link.Loss > 1 {
			return Snapshot{}, fmt.Errorf("snapshot contains invalid link metrics between %d and %d", link.From, link.To)
		}
		seenLinkPairs[pair] = true
		snapshot.Links = append(snapshot.Links, link)
	}
	return snapshot, nil
}
