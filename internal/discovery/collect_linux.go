package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os/exec"
)

func collectNeighbors(ctx context.Context, observation *Observation) error {
	indices := map[string]int{}
	for _, iface := range observation.Interfaces {
		indices[iface.Name] = iface.Index
	}
	for _, family := range []string{"-4", "-6"} {
		data, err := exec.CommandContext(ctx, "ip", "-j", family, "neigh", "show").Output()
		if err != nil {
			return fmt.Errorf("ip neighbor (install iproute2): %w", err)
		}
		var neighbors []struct {
			Dst, Dev, Lladdr string
			State            []string
		}
		if err := json.Unmarshal(data, &neighbors); err != nil {
			return fmt.Errorf("ip neighbor JSON: %w", err)
		}
		for _, entry := range neighbors {
			address, err := netip.ParseAddr(entry.Dst)
			if err != nil || len(entry.State) == 0 {
				continue
			}
			observation.Neighbors = append(observation.Neighbors, Neighbor{indices[entry.Dev], address, entry.Lladdr, entry.State[0]})
		}
		data, err = exec.CommandContext(ctx, "ip", "-j", family, "route", "show", "default").Output()
		if err != nil {
			return fmt.Errorf("ip route: %w", err)
		}
		var routes []struct{ Gateway, Dev string }
		if err := json.Unmarshal(data, &routes); err != nil {
			return fmt.Errorf("ip route JSON: %w", err)
		}
		for _, entry := range routes {
			address, err := netip.ParseAddr(entry.Gateway)
			if err == nil {
				observation.Gateways = append(observation.Gateways, Gateway{indices[entry.Dev], address})
			}
		}
	}
	return nil
}
