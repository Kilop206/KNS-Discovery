package discovery

import "testing"

func TestParseWindowsTables(t *testing.T) {
	var observation Observation
	err := parseWindows([]byte(`{"Neighbors":[{"Interface":7,"Address":"fe80::1%7","MAC":"02-00-00-00-00-01","State":"Reachable"}],"Gateways":[{"Interface":7,"Address":"fe80::1%7"}]}`), &observation)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Neighbors) != 1 || observation.Neighbors[0].Address.String() != "fe80::1" || len(observation.Gateways) != 1 {
		t.Fatal(observation)
	}
	if err := parseWindows([]byte(`not json`), &Observation{}); err == nil {
		t.Fatal("accepted malformed tables")
	}
}
