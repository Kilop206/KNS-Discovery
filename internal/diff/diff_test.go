package diff

import (
	"reflect"
	"testing"

	"kns.local/discovery/internal/discovery"
)

func TestCompareUsesStableExternalIdentity(t *testing.T) {
	before := discovery.Snapshot{
		Nodes: []discovery.Node{
			{ID: 0, ExternalID: "host:a", Label: "Host", Type: "computer", Evidence: "local"},
			{ID: 1, ExternalID: "device:r", Label: "Router", Type: "router", Evidence: "route"},
		},
		Links: []discovery.Link{
			{From: 0, To: 1, Bandwidth: 100, Delay: 1, Inferred: true, Evidence: "shared_segment"},
		},
	}
	after := discovery.Snapshot{
		Nodes: []discovery.Node{
			{ID: 0, ExternalID: "device:r", Label: "Router", Type: "router", Evidence: "route"},
			{ID: 1, ExternalID: "host:a", Label: "Workstation", Type: "computer", Evidence: "local"},
			{ID: 2, ExternalID: "device:s", Label: "Server", Type: "server", Evidence: "inventory"},
		},
		Links: []discovery.Link{
			{From: 0, To: 1, Bandwidth: 1000, Delay: 1, Inferred: true, Evidence: "shared_segment"},
			{From: 0, To: 2, Bandwidth: 100, Delay: 1, Inferred: true, Evidence: "shared_segment"},
		},
	}

	got := Compare(&before, after)
	if !got.BaselineAvailable {
		t.Fatal("expected a baseline")
	}
	if !reflect.DeepEqual(got.AddedNodes, []string{"device:s"}) {
		t.Fatalf("added nodes = %v", got.AddedNodes)
	}
	if !reflect.DeepEqual(got.ChangedNodes, []string{"host:a"}) {
		t.Fatalf("changed nodes = %v", got.ChangedNodes)
	}
	if len(got.RemovedNodes) != 0 {
		t.Fatalf("removed nodes = %v", got.RemovedNodes)
	}
	if !reflect.DeepEqual(got.ChangedLinks, []string{"device:r<->host:a"}) {
		t.Fatalf("changed links = %v", got.ChangedLinks)
	}
	if !reflect.DeepEqual(got.AddedLinks, []string{"device:r<->device:s"}) {
		t.Fatalf("added links = %v", got.AddedLinks)
	}
}

func TestCompareWithoutBaselineReportsCurrentSnapshotAsAdded(t *testing.T) {
	current := discovery.Snapshot{
		Nodes: []discovery.Node{
			{ID: 4, ExternalID: "host:z", Type: "computer"},
			{ID: 9, ExternalID: "device:a", Type: "router"},
		},
		Links: []discovery.Link{{From: 4, To: 9, Bandwidth: 100, Delay: 1}},
	}

	got := Compare(nil, current)
	if got.BaselineAvailable {
		t.Fatal("unexpected baseline")
	}
	if !reflect.DeepEqual(got.AddedNodes, []string{"device:a", "host:z"}) {
		t.Fatalf("added nodes = %v", got.AddedNodes)
	}
	if !reflect.DeepEqual(got.AddedLinks, []string{"device:a<->host:z"}) {
		t.Fatalf("added links = %v", got.AddedLinks)
	}
}

func TestEmptyDiffIgnoresNumericIdReordering(t *testing.T) {
	before := discovery.Snapshot{
		Nodes: []discovery.Node{
			{ID: 0, ExternalID: "a", Label: "A", Type: "computer"},
			{ID: 1, ExternalID: "b", Label: "B", Type: "router"},
		},
		Links: []discovery.Link{{From: 0, To: 1, Bandwidth: 100, Delay: 1}},
	}
	after := discovery.Snapshot{
		Nodes: []discovery.Node{
			{ID: 10, ExternalID: "b", Label: "B", Type: "router"},
			{ID: 11, ExternalID: "a", Label: "A", Type: "computer"},
		},
		Links: []discovery.Link{{From: 10, To: 11, Bandwidth: 100, Delay: 1}},
	}

	got := Compare(&before, after)
	if !got.Empty() {
		t.Fatalf("numeric ID reorder produced diff: %+v", got)
	}
}
