package discovery

import "strings"

func neighborRank(state string) int {
	switch strings.ToLower(state) {
	case "reachable":
		return 5
	case "permanent":
		return 4
	case "delay":
		return 3
	case "probe":
		return 2
	case "stale":
		return 1
	default:
		return 0
	}
}

// OS tables may contain conflicting observations for one address. Prefer a
// reachable record to stale data, with a stable MAC tie-breaker. Both topology
// identities and DNS cache keys must use the same selected observation.
func observedNeighbors(observation Observation) map[string]Neighbor {
	selected := make(map[string]Neighbor)
	for _, neighbor := range observation.Neighbors {
		rank := neighborRank(neighbor.State)
		neighbor.MAC = normalizedMAC(neighbor.MAC)
		if rank == 0 || !usableAddress(neighbor.Address) || neighbor.MAC == "" {
			continue
		}
		neighbor.State = strings.ToLower(neighbor.State)
		key := addressKey(neighbor.Interface, neighbor.Address)
		previous, exists := selected[key]
		if !exists || rank > neighborRank(previous.State) ||
			(rank == neighborRank(previous.State) && neighbor.MAC < previous.MAC) {
			selected[key] = neighbor
		}
	}
	return selected
}
