package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallScriptInSyncWithRepoCopy fails CI if either user-visible installer
// drifts from its go:embed copy here. Two copies exist because the repo-root
// scripts are browsable + runnable standalone while the embedded files are
// what the server serves at runtime.
func TestInstallScriptInSyncWithRepoCopy(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..", "..")
	repoCopy, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "install-parsar-daemon.sh"))
	if err != nil {
		t.Fatalf("read repo copy: %v", err)
	}
	embedCopy, err := os.ReadFile("install_parsar_daemon.sh")
	if err != nil {
		t.Fatalf("read embed copy: %v", err)
	}
	if string(repoCopy) != string(embedCopy) {
		t.Fatalf("scripts/install-parsar-daemon.sh and server/internal/api/install_parsar_daemon.sh drifted; copy one to the other and re-run the test")
	}

	powerShellRepoCopy, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "install-parsar-daemon.ps1"))
	if err != nil {
		t.Fatalf("read PowerShell repo copy: %v", err)
	}
	powerShellEmbedCopy, err := os.ReadFile("install_parsar_daemon.ps1")
	if err != nil {
		t.Fatalf("read PowerShell embed copy: %v", err)
	}
	if string(powerShellRepoCopy) != string(powerShellEmbedCopy) {
		t.Fatalf("scripts/install-parsar-daemon.ps1 and server/internal/api/install_parsar_daemon.ps1 drifted; copy one to the other and re-run the test")
	}
}
