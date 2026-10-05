package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"kns.local/discovery/internal/discovery"
)

func main() {
	if err := run(); err != nil {
		slog.Error("discovery stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	output := flag.String("output", "output/network.json", "KNS topology snapshot file")
	diffOutput := flag.String("diff-output", "", "optional structured diff JSON comparing each collection with the previous snapshot")
	hubURL := flag.String("hub-url", "http://localhost:3001", "Topology Hub base URL used with --hub-topology")
	hubTopology := flag.String("hub-topology", "", "optional existing Topology Hub topology ID to synchronize")
	interval := flag.Duration("watch", 0, "repeat interval, e.g. 5s; zero collects once")
	timeout := flag.Duration("timeout", 15*time.Second, "deadline for each OS collection")
	iface := flag.String("interface", "", "exact interface name; empty includes all active interfaces")
	resolveNames := flag.Bool("resolve-names", true, "resolve device names with cached, bounded DNS queries")
	inventory := flag.String("inventory", "", "JSON object mapping external_id to label/type overrides")
	bandwidth := flag.Float64("bandwidth", 100, "assumed simulated link bandwidth in Mbps")
	delay := flag.Float64("delay", 1, "assumed simulated link delay in milliseconds")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *interval < 0 || (*interval > 0 && *interval < time.Second) || *timeout <= 0 {
		return fmt.Errorf("watch must be zero or at least 1s; timeout must be positive")
	}
	if *output == "" {
		return fmt.Errorf("output must be a file path")
	}
	if *diffOutput != "" && filepath.Clean(*diffOutput) == filepath.Clean(*output) {
		return fmt.Errorf("diff-output must be different from output")
	}

	var remote remoteSnapshotPublisher
	if *hubTopology != "" {
		publisher, err := newTopologyHubPublisher(
			*hubURL,
			*hubTopology,
			os.Getenv("KNS_TOPOLOGY_HUB_TOKEN"),
		)
		if err != nil {
			return err
		}
		remote = publisher
	}

	options := discovery.Options{Interface: *iface, Bandwidth: *bandwidth, Delay: *delay}
	if err := discovery.ValidateOptions(options); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0700); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	identifier := discovery.NewIdentifier()
	collector := func(ctx context.Context) (discovery.Observation, error) {
		observation, err := discovery.Collect(ctx)
		if err == nil && *resolveNames {
			identifier.Enrich(ctx, &observation, *iface)
		}
		return observation, err
	}
	collect := func() error {
		attempt, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		return publishSnapshotWithDiffAndRemote(
			attempt,
			*output,
			*diffOutput,
			*inventory,
			options,
			collector,
			remote,
		)
	}
	if *interval == 0 {
		err := collect()
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	return watchSnapshots(ctx, ticker.C, collect)
}

// Attempt immediately, then retry on ticks even if the first collection fails.
// Keeping ticks injectable lets tests exercise recovery without real-time waits.
func watchSnapshots(ctx context.Context, ticks <-chan time.Time, collect func() error) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := collect(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("collection failed; snapshot unchanged; will retry", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case _, open := <-ticks:
			if !open {
				return nil
			}
		}
	}
}
