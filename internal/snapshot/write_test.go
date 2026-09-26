package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplaceAndUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.json")
	for _, value := range []string{"first", "second"} {
		changed, err := Write(path, []byte(value))
		if err != nil || !changed {
			t.Fatalf("write: changed=%v error=%v", changed, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != value {
			t.Fatalf("read: %q %v", data, err)
		}
	}
	if changed, err := Write(path, []byte("second")); err != nil || changed {
		t.Fatalf("unchanged: %v %v", changed, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v %v", entries, err)
	}
}
