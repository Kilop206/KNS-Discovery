//go:build !windows && !linux

package discovery

import (
	"context"
	"fmt"
	"runtime"
)

func collectNeighbors(context.Context, *Observation) error {
	return fmt.Errorf("network discovery on %s is not supported; use Windows or Linux", runtime.GOOS)
}
