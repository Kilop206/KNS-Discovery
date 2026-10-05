package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"kns.local/discovery/internal/diff"
	"kns.local/discovery/internal/discovery"
	"kns.local/discovery/internal/snapshot"
)

type collector func(context.Context) (discovery.Observation, error)

type remoteSnapshotPublisher interface {
	Publish(context.Context, discovery.Snapshot) error
}

// Each attempt reads a fresh inventory. Invalid or partially written input never
// publishes a graph and removed overrides do not survive in an old map.
func publishSnapshot(ctx context.Context, output, inventoryPath string, options discovery.Options, collect collector) error {
	return publishSnapshotWithDiff(ctx, output, "", inventoryPath, options, collect)
}

func publishSnapshotWithDiff(ctx context.Context, output, diffOutput, inventoryPath string, options discovery.Options, collect collector) error {
	return publishSnapshotWithDiffAndRemote(ctx, output, diffOutput, inventoryPath, options, collect, nil)
}

func publishSnapshotWithDiffAndRemote(
	ctx context.Context,
	output, diffOutput, inventoryPath string,
	options discovery.Options,
	collect collector,
	remote remoteSnapshotPublisher,
) error {
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

	var baseline *discovery.Snapshot
	if previous, readErr := os.ReadFile(output); readErr == nil {
		var decoded discovery.Snapshot
		if unmarshalErr := json.Unmarshal(previous, &decoded); unmarshalErr != nil {
			slog.Warn("previous snapshot is invalid; diff baseline unavailable", "error", unmarshalErr)
		} else {
			baseline = &decoded
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read previous snapshot for diff: %w", readErr)
	}
	changeSet := diff.Compare(baseline, topology)

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
	if diffOutput != "" {
		if err := os.MkdirAll(filepath.Dir(diffOutput), 0700); err != nil {
			return err
		}
		diffData, err := json.MarshalIndent(changeSet, "", "  ")
		if err != nil {
			return err
		}
		if _, err := snapshot.Write(diffOutput, append(diffData, '\n')); err != nil {
			return fmt.Errorf("write snapshot diff: %w", err)
		}
	}

	if changed {
		slog.Info(
			"topology published",
			"nodes", len(topology.Nodes),
			"links", len(topology.Links),
			"added_nodes", len(changeSet.AddedNodes),
			"removed_nodes", len(changeSet.RemovedNodes),
			"changed_nodes", len(changeSet.ChangedNodes),
			"added_links", len(changeSet.AddedLinks),
			"removed_links", len(changeSet.RemovedLinks),
			"changed_links", len(changeSet.ChangedLinks),
			"output", output,
		)
	}
	for _, warning := range topology.Warnings {
		slog.Warn(warning)
	}
	if remote != nil {
		if err := remote.Publish(ctx, topology); err != nil {
			return fmt.Errorf("publish snapshot to Topology Hub: %w", err)
		}
		slog.Info("Topology Hub synchronized", "nodes", len(topology.Nodes), "links", len(topology.Links))
	}
	return nil
}
