package discovery

import "net/netip"

// Observation contains only data reported by the local operating system.
type Observation struct {
	Hostname   string
	Interfaces []Interface
	Neighbors  []Neighbor
	Gateways   []Gateway
	Names      map[string]string // Interface-scoped addresses resolved by the OS resolver.
}

type Interface struct {
	Index    int
	Name     string
	MAC      string
	Prefixes []netip.Prefix
}

type Neighbor struct {
	Interface int
	Address   netip.Addr
	MAC       string
	State     string
}

type Gateway struct {
	Interface int
	Address   netip.Addr
}

type Node struct {
	ID         int      `json:"id"`
	ExternalID string   `json:"external_id"`
	Label      string   `json:"label"`
	Type       string   `json:"type"`
	Addresses  []string `json:"addresses,omitempty"`
	MAC        string   `json:"mac,omitempty"`
	Evidence   string   `json:"evidence"`
}

type Link struct {
	From      int     `json:"from"`
	To        int     `json:"to"`
	Bandwidth float64 `json:"bandwidth"`
	Delay     float64 `json:"delay"`
	Loss      float64 `json:"loss"`
	Inferred  bool    `json:"inferred"`
	Evidence  string  `json:"evidence"`
}

type Snapshot struct {
	SchemaVersion string   `json:"schema_version"`
	Name          string   `json:"name"`
	Nodes         []Node   `json:"nodes"`
	Links         []Link   `json:"links"`
	Warnings      []string `json:"warnings,omitempty"`
}

type Override struct {
	Label string `json:"label"`
	Type  string `json:"type"`
}

type Options struct {
	Interface string
	Bandwidth float64
	Delay     float64
	Inventory map[string]Override
}

func ValidDeviceType(value string) bool {
	switch value {
	case "unknown", "computer", "router", "switch", "access_point", "server", "phone", "printer", "iot", "network_segment":
		return true
	default:
		return false
	}
}
