package main

import (
	"context"
	"errors"
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
	interval := flag.Duration("watch", 0, "repeat interval, e.g. 5s; zero collects once")
	timeout := flag.Duration("timeout", 15*time.Second, "deadline for each OS collection")
	iface := flag.String("interface", "", "exact interface name; empty includes all active interfaces")
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
	options := discovery.Options{Interface: *iface, Bandwidth: *bandwidth, Delay: *delay}
	if err := os.MkdirAll(filepath.Dir(*output), 0700); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	collect := func() error {
		attempt, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		return publishSnapshot(attempt, *output, *inventory, options, discovery.Collect)
	}
	if err := collect(); err != nil {
		return err
	}
	if *interval == 0 {
		return nil
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := collect(); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("collection failed; retaining previous snapshot", "error", err)
			}
		}
	}
}
