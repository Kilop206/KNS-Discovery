package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
)

// Collect fails as a whole if route/neighbor collection fails. Watch mode must
// not publish an incomplete observation that could falsely remove devices.
func Collect(ctx context.Context) (Observation, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return Observation{}, fmt.Errorf("hostname: %w", err)
	}
	available, err := net.Interfaces()
	if err != nil {
		return Observation{}, fmt.Errorf("interfaces: %w", err)
	}
	observation := Observation{Hostname: hostname}
	for _, iface := range available {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return Observation{}, fmt.Errorf("interface %s: %w", iface.Name, err)
		}
		entry := Interface{Index: iface.Index, Name: iface.Name, MAC: iface.HardwareAddr.String()}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err == nil {
				entry.Prefixes = append(entry.Prefixes, prefix)
			}
		}
		observation.Interfaces = append(observation.Interfaces, entry)
	}
	if err := collectNeighbors(ctx, &observation); err != nil {
		return Observation{}, err
	}
	return observation, nil
}
