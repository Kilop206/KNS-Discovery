package discovery

import (
	"math"
	"testing"
)

func TestValidateOptionsBeforeCollection(t *testing.T) {
	for _, options := range []Options{
		{Bandwidth: 0}, {Bandwidth: -1}, {Bandwidth: math.NaN()}, {Bandwidth: math.Inf(1)},
		{Bandwidth: 100, Delay: -1}, {Bandwidth: 100, Delay: math.NaN()}, {Bandwidth: 100, Delay: math.Inf(1)},
		{Bandwidth: 100, Inventory: map[string]Override{"host:a": {Type: "magic"}}},
	} {
		if err := ValidateOptions(options); err == nil {
			t.Fatalf("accepted invalid configuration: %+v", options)
		}
	}
	if err := ValidateOptions(Options{Bandwidth: 100, Delay: 0, Interface: "currently-offline"}); err != nil {
		t.Fatalf("static validation depends on interface availability: %v", err)
	}
}
