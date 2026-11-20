package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"kns.local/discovery/internal/diff"
	"kns.local/discovery/internal/discovery"
)

func TestPublishingDiscoveryReplacesLegacyHubGraphWithAccurateDiff(t *testing.T) {
	var received struct {
		Title   string             `json:"title"`
		Version int64              `json:"version"`
		Graph   discovery.Snapshot `json:"graph"`
		Diff    diff.Result        `json:"discoveryDiff"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"topology":{"title":"Office","visibility":"PRIVATE","version":7},"graph":{"nodes":2,"links":[{"from":0,"to":1,"bandwidth":100,"delay":1}]}}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	publisher, err := newTopologyStorePublisher(server.URL, "one", "test")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := hubSnapshot("Discovered")
	if err := publisher.Publish(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if received.Title != "Office" || received.Version != 7 || !reflect.DeepEqual(received.Graph, snapshot) {
		t.Fatalf("update lost topology metadata or snapshot: %+v", received)
	}
	if !received.Diff.BaselineAvailable || !reflect.DeepEqual(received.Diff.RemovedNodes, []string{"id:0", "id:1"}) ||
		len(received.Diff.AddedNodes) != 2 || len(received.Diff.RemovedLinks) != 1 || len(received.Diff.AddedLinks) != 1 {
		t.Fatalf("legacy identities were not recorded correctly: %+v", received.Diff)
	}
}

func TestStoredHubGraphsRejectAmbiguousIdentitiesAndMalformedEndpoints(t *testing.T) {
	for _, data := range []string{
		`{"nodes":0,"links":[]}`, `{"nodes":5001,"links":[]}`,
		`{"nodes":2.5,"links":[]}`, `{"nodes":null,"links":[]}`,
		`{"nodes":2}`, `{"nodes":2,"links":[{"from":0}]}`,
		`{"nodes":2,"links":[{"from":0,"to":2}]}`,
		`{"nodes":[{}],"links":[]}`,
		`{"nodes":[{"id":1},{"id":1}],"links":[]}`,
		`{"nodes":[{"id":0},{"id":1,"external_id":"id:0"}],"links":[]}`,
		`{"nodes":2,"links":[{"from":0,"to":1},{"from":1,"to":0}]}`,
	} {
		if _, err := decodeStoredHubGraph([]byte(data)); err == nil {
			t.Errorf("invalid graph accepted: %s", data)
		}
	}
	for _, data := range []string{
		`{"nodes":5000,"links":[]}`,
		`{"schema_version":"1.0","nodes":[{"id":30},{"id":10}],"links":[{"from":30,"to":10}]}`,
	} {
		if _, err := decodeStoredHubGraph([]byte(data)); err != nil {
			t.Errorf("valid graph rejected: %v", err)
		}
	}
}
