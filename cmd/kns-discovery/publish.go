package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// Hub limits the entire update body to 1 MiB, including the revision metadata.
const maxSavedSnapshotBytes = 1 << 20

func publishSavedSnapshot(ctx context.Context, filename string, remote remoteSnapshotPublisher) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("inspect input snapshot: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxSavedSnapshotBytes {
		return fmt.Errorf("input snapshot must be a regular file of at most 1 MiB")
	}
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open input snapshot: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSavedSnapshotBytes+1))
	if err != nil {
		return fmt.Errorf("read input snapshot: %w", err)
	}
	if len(data) > maxSavedSnapshotBytes {
		return fmt.Errorf("input snapshot exceeds 1 MiB")
	}
	topology, err := discovery.DecodeSnapshot(data)
	if err != nil {
		return fmt.Errorf("invalid input snapshot: %w", err)
	}
	if err := remote.Publish(ctx, topology); err != nil {
		return fmt.Errorf("publish input snapshot to topology store: %w", err)
	}
	slog.Info("saved snapshot synchronized", "nodes", len(topology.Nodes), "links", len(topology.Links))
	return nil
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

	baseline, err := readDiffBaseline(output)
	if err != nil {
		return err
	}
	changeSet := diff.Compare(baseline, topology)
	// The published timestamp describes the last changed topology observation.
	// Preserve it when polling finds the same graph so watch mode does not
	// reload KNS or rewrite the local snapshot on every successful collection.
	if baseline != nil && changeSet.Empty() && baseline.ObservedAt != "" {
		topology.ObservedAt = baseline.ObservedAt
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
			return fmt.Errorf("publish snapshot to topology store: %w", err)
		}
		slog.Info("topology store synchronized", "nodes", len(topology.Nodes), "links", len(topology.Links))
	}
	return nil
}

func readDiffBaseline(path string) (*discovery.Snapshot, error) {
	previous, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		// Treat an unreadable file as an I/O failure so missing access cannot
		// produce a misleading diff containing only additions.
		return nil, fmt.Errorf("read previous snapshot for diff: %w", err)
	}
	baseline, err := discovery.DecodeSnapshot(previous)
	if err != nil {
		// A corrupt file is already unusable as evidence. The next valid
		// observation can replace it and establish a new baseline.
		slog.Warn("previous snapshot is invalid; diff baseline unavailable", "error", err)
		return nil, nil
	}
	return &baseline, nil
}
