//go:build windows

package daemonize

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureLogFileCreatesEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.log")
	if err := EnsureLogFile(path); err != nil {
		t.Fatalf("EnsureLogFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("size = %d, want 0", info.Size())
	}
}
