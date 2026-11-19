package snapshot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Write leaves the last good snapshot intact on failure. Skipping identical
// contents keeps file watchers from reloading a graph that did not change.
func Write(path string, data []byte) (bool, error) {
	previous, err := os.ReadFile(path)
	if err == nil && bytes.Equal(previous, data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read previous snapshot: %w", err)
	}
	// A sibling temp file keeps replacement on the same filesystem, where
	// readers can keep using the previous snapshot until the file is ready.
	file, err := os.CreateTemp(filepath.Dir(path), ".kns-snapshot-*")
	if err != nil {
		return false, err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	// Close before replacing so Windows can move the file. Preserve the
	// write or sync error if closing also fails.
	closeErr := file.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := replaceFile(temporary, path); err != nil {
		return false, err
	}
	return true, nil
}
