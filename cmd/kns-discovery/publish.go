package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"kns.local/discovery/internal/discovery"
	"kns.local/discovery/internal/snapshot"
)

type collector func(context.Context) (discovery.Observation, error)

// Each attempt reads a fresh inventory. Invalid or partially written input never
// publishes a graph and removed overrides do not survive in an old map.
func publishSnapshot(ctx context.Context, output, inventoryPath string, options discovery.Options, collect collector) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if inventoryPath != "" {
		data, err := os.ReadFile(inventoryPath)
		if err != nil {
			return fmt.Errorf("inventory %q: %w", inventoryPath, err)
		}
		options.Inventory = nil
		if err := json.Unmarshal(data, &options.Inventory); err != nil {
			return fmt.Errorf("inventory %q: %w", inventoryPath, err)
		}
		if options.Inventory == nil {
			return fmt.Errorf("inventory %q must be a JSON object; use {} to clear overrides", inventoryPath)
		}
	}
	observation, err := collect(ctx)
	if err != nil {
		return err
	}
	topology, err := discovery.Build(observation, options)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	changed, err := snapshot.Write(output, append(data, '\n'))
	if err != nil {
		return err
	}
	if changed {
		slog.Info("topology published", "nodes", len(topology.Nodes), "links", len(topology.Links), "output", output)
	}
	for _, warning := range topology.Warnings {
		slog.Warn(warning)
	}
	return nil
}
