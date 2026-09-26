package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Fixed script, with no interpolation of user-controlled interface names.
const windowsQuery = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$neighbors = @(Get-NetNeighbor | ForEach-Object {
    @{ Interface = $_.InterfaceIndex; Address = $_.IPAddress; MAC = $_.LinkLayerAddress; State = $_.State.ToString() }
})
$gateways = @(Get-NetRoute | Where-Object { $_.DestinationPrefix -eq '0.0.0.0/0' -or $_.DestinationPrefix -eq '::/0' } | ForEach-Object {
    @{ Interface = $_.InterfaceIndex; Address = $_.NextHop }
})
@{ Neighbors = $neighbors; Gateways = $gateways } | ConvertTo-Json -Depth 4 -Compress`

func collectNeighbors(ctx context.Context, observation *Observation) error {
	program := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.CommandContext(ctx, program, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", windowsQuery)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	data, err := command.Output()
	if err != nil {
		return fmt.Errorf("Windows network tables: %w", err)
	}
	return parseWindows(data, observation)
}

func parseWindows(data []byte, observation *Observation) error {
	var tables struct {
		Neighbors []struct {
			Interface           int
			Address, MAC, State string
		}
		Gateways []struct {
			Interface int
			Address   string
		}
	}
	if err := json.Unmarshal(data, &tables); err != nil {
		return fmt.Errorf("Windows network JSON: %w", err)
	}
	for _, entry := range tables.Neighbors {
		address, err := netip.ParseAddr(strings.Split(entry.Address, "%")[0])
		if err == nil {
			observation.Neighbors = append(observation.Neighbors, Neighbor{entry.Interface, address, entry.MAC, entry.State})
		}
	}
	for _, entry := range tables.Gateways {
		address, err := netip.ParseAddr(strings.Split(entry.Address, "%")[0])
		if err == nil {
			observation.Gateways = append(observation.Gateways, Gateway{entry.Interface, address})
		}
	}
	return nil
}
