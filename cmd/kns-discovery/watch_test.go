package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"kns.local/discovery/internal/discovery"
)

func TestWatchRecoversFromInitialFailureAndPreservesLastSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := filepath.Join(t.TempDir(), "network.json")
	options := discovery.Options{Bandwidth: 100, Delay: 1}
	if err := publishSnapshot(ctx, output, "", options, fixtureCollector); err != nil {
		t.Fatal(err)
	}
	original := readBytes(t, output)
	ticks := make(chan time.Time, 1)
	ticks <- time.Time{}
	attempts := 0
	err := watchSnapshots(ctx, ticks, func() error {
		attempts++
		err := publishSnapshot(ctx, output, "", options, func(ctx context.Context) (discovery.Observation, error) {
			if attempts == 1 {
				return discovery.Observation{}, errors.New("interface unavailable")
			}
			observation, err := fixtureCollector(ctx)
			observation.Hostname = "recovered"
			return observation, err
		})
		if attempts == 1 && !bytes.Equal(original, readBytes(t, output)) {
			t.Fatal("failed first attempt replaced the previous snapshot")
		}
		if attempts == 2 {
			cancel()
		}
		return err
	})
	if err != nil || attempts != 2 {
		t.Fatalf("watch did not recover: attempts=%d error=%v", attempts, err)
	}
	if !bytes.Contains(readBytes(t, output), []byte("host:recovered")) {
		t.Fatal("recovered collection was not published")
	}
}

func TestWatchRetriesTimeoutsWithoutAnExistingSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := filepath.Join(t.TempDir(), "network.json")
	ticks := make(chan time.Time, 1)
	ticks <- time.Time{}
	attempts := 0
	err := watchSnapshots(ctx, ticks, func() error {
		attempts++
		if attempts == 1 {
			return context.DeadlineExceeded
		}
		err := publishSnapshot(ctx, output, "", discovery.Options{Bandwidth: 100}, fixtureCollector)
		cancel()
		return err
	})
	if err != nil || attempts != 2 || len(readBytes(t, output)) == 0 {
		t.Fatalf("failed to publish after timeout: attempts=%d error=%v", attempts, err)
	}
}

func TestWatchStopsCleanlyOnCancellation(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelBefore {
			cancel()
		}
		attempts := 0
		err := watchSnapshots(ctx, make(chan time.Time), func() error {
			attempts++
			cancel()
			return context.Canceled
		})
		cancel()
		if err != nil || (cancelBefore && attempts != 0) || (!cancelBefore && attempts != 1) {
			t.Fatalf("unexpected cancellation: before=%v attempts=%d error=%v", cancelBefore, attempts, err)
		}
	}
}

func TestWatchDoesNotSpinOnClosedTicker(t *testing.T) {
	ticks := make(chan time.Time)
	close(ticks)
	attempts := 0
	if err := watchSnapshots(context.Background(), ticks, func() error {
		attempts++
		return nil
	}); err != nil || attempts != 1 {
		t.Fatalf("unexpected closed ticker behavior: attempts=%d error=%v", attempts, err)
	}
}
