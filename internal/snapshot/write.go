package snapshot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Write publishes a complete file and leaves the last good snapshot intact on
// failure. Identical contents do not trigger an unnecessary live update.
func Write(path string, data []byte) (bool, error) {
	previous, err := os.ReadFile(path)
	if err == nil && bytes.Equal(previous, data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read previous snapshot: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".kns-snapshot-*")
	if err != nil {
		return false, err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := replaceFile(temporary, path); err != nil {
		return false, err
	}
	return true, nil
}
