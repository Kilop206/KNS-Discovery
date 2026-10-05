package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"kns.local/discovery/internal/diff"
	"kns.local/discovery/internal/discovery"
)

func hubSnapshot(label string) discovery.Snapshot {
	return discovery.Snapshot{
		SchemaVersion: "1.0",
		Name:          "Local network",
		Nodes: []discovery.Node{
			{ID: 0, ExternalID: "host:example", Label: label, Type: "computer", Evidence: "local_interface"},
			{ID: 1, ExternalID: "segment:1", Label: "Ethernet", Type: "network_segment", Evidence: "interface_prefix"},
		},
		Links: []discovery.Link{
			{From: 0, To: 1, Bandwidth: 100, Delay: 1, Loss: 0, Inferred: true, Evidence: "shared_segment"},
		},
	}
}

func TestTopologyHubPublisherUpdatesWithRemoteRelativeDiff(t *testing.T) {
	var puts atomic.Int32
	var received struct {
		Title         string             `json:"title"`
		Description   string             `json:"description"`
		Visibility    string             `json:"visibility"`
		Graph         discovery.Snapshot `json:"graph"`
		Version       int64              `json:"version"`
		DiscoveryDiff diff.Result        `json:"discoveryDiff"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/topologies/topology-1" {
			http.NotFound(response, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer knsh_test" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch request.Method {
		case http.MethodGet:
			json.NewEncoder(response).Encode(map[string]any{
				"topology": map[string]any{
					"title": "Office LAN", "description": "Discovered", "visibility": "PRIVATE", "version": 7,
				},
				"graph": hubSnapshot("Old name"),
			})
		case http.MethodPut:
			puts.Add(1)
			if request.Header.Get("X-Hub-Request") != "1" {
				t.Error("missing X-Hub-Request mutation header")
			}
			if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
				t.Errorf("decode update: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			response.Write([]byte("{}"))
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	publisher, err := newTopologyHubPublisher(server.URL, "topology-1", "knsh_test")
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), hubSnapshot("New name")); err != nil {
		t.Fatal(err)
	}
	if puts.Load() != 1 {
		t.Fatalf("expected one update, got %d", puts.Load())
	}
	if received.Version != 7 || received.Title != "Office LAN" ||
		received.Description != "Discovered" || received.Visibility != "PRIVATE" {
		t.Fatalf("Hub metadata was not preserved: %+v", received)
	}
	if received.Graph.Nodes[0].Label != "New name" {
		t.Fatalf("new snapshot was not sent: %+v", received.Graph)
	}
	if !received.DiscoveryDiff.BaselineAvailable ||
		len(received.DiscoveryDiff.ChangedNodes) != 1 ||
		received.DiscoveryDiff.ChangedNodes[0] != "host:example" {
		t.Fatalf("diff was not computed against Hub state: %+v", received.DiscoveryDiff)
	}
}

func TestTopologyHubPublisherSkipsUnchangedRemoteSnapshot(t *testing.T) {
	var puts atomic.Int32
	current := hubSnapshot("Same")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			json.NewEncoder(response).Encode(map[string]any{
				"topology": map[string]any{
					"title": "LAN", "description": "", "visibility": "PRIVATE", "version": 3,
				},
				"graph": current,
			})
		case http.MethodPut:
			puts.Add(1)
			response.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	publisher, err := newTopologyHubPublisher(server.URL, "same", "knsh_test")
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	if puts.Load() != 0 {
		t.Fatalf("unchanged snapshot created %d revisions", puts.Load())
	}
}

func TestTopologyHubPublisherReportsConflictsAndInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		base, topology, token string
	}{
		{"relative", "id", "token"},
		{"http://localhost:3001?x=1", "id", "token"},
		{"http://localhost:3001", "", "token"},
		{"http://localhost:3001", "id", ""},
	} {
		if _, err := newTopologyHubPublisher(test.base, test.topology, test.token); err == nil {
			t.Fatalf("accepted invalid Hub configuration: %+v", test)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			json.NewEncoder(response).Encode(map[string]any{
				"topology": map[string]any{
					"title": "LAN", "description": "", "visibility": "PRIVATE", "version": 9,
				},
				"graph": hubSnapshot("Old"),
			})
			return
		}
		response.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	publisher, err := newTopologyHubPublisher(server.URL, "conflict", "knsh_test")
	if err != nil {
		t.Fatal(err)
	}
	err = publisher.Publish(context.Background(), hubSnapshot("New"))
	if err == nil || !strings.Contains(err.Error(), "changed concurrently") {
		t.Fatalf("unexpected conflict result: %v", err)
	}
}

type recordingRemotePublisher struct {
	calls int
	err   error
}

func (publisher *recordingRemotePublisher) Publish(context.Context, discovery.Snapshot) error {
	publisher.calls++
	return publisher.err
}

func TestLocalPublicationRetriesRemoteEvenWhenLocalSnapshotIsUnchanged(t *testing.T) {
	directory := t.TempDir()
	output := directory + "/network.json"
	options := discovery.Options{Bandwidth: 100, Delay: 1}
	remote := &recordingRemotePublisher{err: context.DeadlineExceeded}

	err := publishSnapshotWithDiffAndRemote(
		context.Background(), output, "", "", options, fixtureCollector, remote,
	)
	if err == nil {
		t.Fatal("remote failure should be reported")
	}
	if _, statErr := os.Stat(output); statErr != nil {
		t.Fatalf("local snapshot was not preserved after remote failure: %v", statErr)
	}

	remote.err = nil
	if err := publishSnapshotWithDiffAndRemote(
		context.Background(), output, "", "", options, fixtureCollector, remote,
	); err != nil {
		t.Fatal(err)
	}
	if remote.calls != 2 {
		t.Fatalf("remote synchronization was not retried: %d calls", remote.calls)
	}
}
