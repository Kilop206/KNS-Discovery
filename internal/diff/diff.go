package diff

import (
	"fmt"
	"sort"

	"kns.local/discovery/internal/discovery"
)

type Result struct {
	SchemaVersion     string   `json:"schema_version"`
	BaselineAvailable bool     `json:"baseline_available"`
	AddedNodes        []string `json:"added_nodes,omitempty"`
	RemovedNodes      []string `json:"removed_nodes,omitempty"`
	ChangedNodes      []string `json:"changed_nodes,omitempty"`
	AddedLinks        []string `json:"added_links,omitempty"`
	RemovedLinks      []string `json:"removed_links,omitempty"`
	ChangedLinks      []string `json:"changed_links,omitempty"`
}

func Compare(before *discovery.Snapshot, after discovery.Snapshot) Result {
	result := Result{
		SchemaVersion:     "1.0",
		BaselineAvailable: before != nil,
	}
	if before == nil {
		for _, node := range after.Nodes {
			result.AddedNodes = append(result.AddedNodes, nodeKey(node))
		}
		for key := range linksByKey(after) {
			result.AddedLinks = append(result.AddedLinks, key)
		}
		sort.Strings(result.AddedNodes)
		sort.Strings(result.AddedLinks)
		return result
	}

	beforeNodes := nodesByKey(*before)
	afterNodes := nodesByKey(after)
	for key, node := range afterNodes {
		previous, ok := beforeNodes[key]
		if !ok {
			result.AddedNodes = append(result.AddedNodes, key)
			continue
		}
		if !sameNode(previous, node) {
			result.ChangedNodes = append(result.ChangedNodes, key)
		}
	}
	for key := range beforeNodes {
		if _, ok := afterNodes[key]; !ok {
			result.RemovedNodes = append(result.RemovedNodes, key)
		}
	}

	beforeLinks := linksByKey(*before)
	afterLinks := linksByKey(after)
	for key, link := range afterLinks {
		previous, ok := beforeLinks[key]
		if !ok {
			result.AddedLinks = append(result.AddedLinks, key)
			continue
		}
		if previous != link {
			result.ChangedLinks = append(result.ChangedLinks, key)
		}
	}
	for key := range beforeLinks {
		if _, ok := afterLinks[key]; !ok {
			result.RemovedLinks = append(result.RemovedLinks, key)
		}
	}

	sort.Strings(result.AddedNodes)
	sort.Strings(result.RemovedNodes)
	sort.Strings(result.ChangedNodes)
	sort.Strings(result.AddedLinks)
	sort.Strings(result.RemovedLinks)
	sort.Strings(result.ChangedLinks)
	return result
}

func (r Result) Empty() bool {
	return len(r.AddedNodes) == 0 &&
		len(r.RemovedNodes) == 0 &&
		len(r.ChangedNodes) == 0 &&
		len(r.AddedLinks) == 0 &&
		len(r.RemovedLinks) == 0 &&
		len(r.ChangedLinks) == 0
}

func nodesByKey(snapshot discovery.Snapshot) map[string]discovery.Node {
	nodes := make(map[string]discovery.Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[nodeKey(node)] = node
	}
	return nodes
}

func nodeKey(node discovery.Node) string {
	if node.ExternalID != "" {
		return node.ExternalID
	}
	return fmt.Sprintf("id:%d", node.ID)
}

type comparableLink struct {
	Bandwidth float64
	Delay     float64
	Loss      float64
	Inferred  bool
	Evidence  string
}

func linksByKey(snapshot discovery.Snapshot) map[string]comparableLink {
	ids := make(map[int]string, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		ids[node.ID] = nodeKey(node)
	}
	links := make(map[string]comparableLink, len(snapshot.Links))
	for _, link := range snapshot.Links {
		from := ids[link.From]
		to := ids[link.To]
		if from > to {
			from, to = to, from
		}
		key := from + "<->" + to
		links[key] = comparableLink{
			Bandwidth: link.Bandwidth,
			Delay:     link.Delay,
			Loss:      link.Loss,
			Inferred:  link.Inferred,
			Evidence:  link.Evidence,
		}
	}
	return links
}

func sameNode(a, b discovery.Node) bool {
	if a.ExternalID != b.ExternalID ||
		a.Label != b.Label ||
		a.Type != b.Type ||
		a.MAC != b.MAC ||
		a.Evidence != b.Evidence ||
		len(a.Addresses) != len(b.Addresses) {
		return false
	}
	for index := range a.Addresses {
		if a.Addresses[index] != b.Addresses[index] {
			return false
		}
	}
	return true
}
