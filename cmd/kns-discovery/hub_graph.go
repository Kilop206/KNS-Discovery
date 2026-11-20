package main

import (
	"encoding/json"
	"fmt"

	"kns.local/discovery/internal/discovery"
)

// Hub accepts up to 5000 stored nodes; new Discovery snapshots remain limited
// to discovery.MaxNodes. Legacy integer graphs identify their nodes by index.
const maxStoredHubNodes = 5000

func (detail *storedTopologyDetail) UnmarshalJSON(data []byte) error {
	var input struct {
		Topology storedTopologySummary `json:"topology"`
		Graph    json.RawMessage       `json:"graph"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	graph, err := decodeStoredHubGraph(input.Graph)
	if err != nil {
		return err
	}
	detail.Topology, detail.Graph = input.Topology, graph
	return nil
}

func decodeStoredHubGraph(data []byte) (discovery.Snapshot, error) {
	var input struct {
		SchemaVersion string          `json:"schema_version"`
		Name          string          `json:"name"`
		ObservedAt    string          `json:"observed_at"`
		Nodes         json.RawMessage `json:"nodes"`
		Links         []struct {
			discovery.Link
			From *int `json:"from"`
			To   *int `json:"to"`
		} `json:"links"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return discovery.Snapshot{}, err
	}
	if input.SchemaVersion != "" && input.SchemaVersion != "1.0" {
		return discovery.Snapshot{}, fmt.Errorf("unsupported stored topology schema %q", input.SchemaVersion)
	}
	graph := discovery.Snapshot{SchemaVersion: input.SchemaVersion, Name: input.Name, ObservedAt: input.ObservedAt}
	var count int
	if err := json.Unmarshal(input.Nodes, &count); err == nil {
		if count < 1 || count > maxStoredHubNodes {
			return graph, fmt.Errorf("stored topology requires 1 to %d nodes", maxStoredHubNodes)
		}
		for id := 0; id < count; id++ {
			graph.Nodes = append(graph.Nodes, discovery.Node{ID: id})
		}
	} else {
		var nodes []struct {
			discovery.Node
			ID *int `json:"id"`
		}
		if err := json.Unmarshal(input.Nodes, &nodes); err != nil {
			return graph, err
		}
		if len(nodes) < 1 || len(nodes) > maxStoredHubNodes {
			return graph, fmt.Errorf("stored topology requires 1 to %d nodes", maxStoredHubNodes)
		}
		for _, node := range nodes {
			if node.ID == nil {
				return graph, fmt.Errorf("stored topology node requires an id")
			}
			node.Node.ID = *node.ID
			graph.Nodes = append(graph.Nodes, node.Node)
		}
	}
	ids, keys := map[int]bool{}, map[string]bool{}
	for _, node := range graph.Nodes {
		key := node.ExternalID
		if key == "" {
			key = fmt.Sprintf("id:%d", node.ID)
		}
		if node.ID < 0 || node.ID > 2147483647 || ids[node.ID] || keys[key] {
			return graph, fmt.Errorf("stored topology contains invalid or ambiguous node identities")
		}
		ids[node.ID], keys[key] = true, true
	}
	if input.Links == nil {
		return graph, fmt.Errorf("stored topology requires a links array")
	}
	graph.Links = make([]discovery.Link, 0, len(input.Links))
	pairs := map[[2]int]bool{}
	for _, entry := range input.Links {
		if entry.From == nil || entry.To == nil {
			return graph, fmt.Errorf("stored topology link requires two endpoints")
		}
		link := entry.Link
		link.From, link.To = *entry.From, *entry.To
		pair := [2]int{min(link.From, link.To), max(link.From, link.To)}
		if !ids[link.From] || !ids[link.To] || link.From == link.To || pairs[pair] {
			return graph, fmt.Errorf("stored topology contains invalid or ambiguous links")
		}
		pairs[pair] = true
		graph.Links = append(graph.Links, link)
	}
	return graph, nil
}
