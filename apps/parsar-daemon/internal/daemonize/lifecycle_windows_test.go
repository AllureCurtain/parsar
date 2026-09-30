//go:build windows

package daemonize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const windowsSpawnChildEnv = "PARSAR_DAEMON_WINDOWS_SPAWN_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(windowsSpawnChildEnv) == "1" {
		fmt.Fprintln(os.Stdout, "windows-spawn-child:stdout")
		fmt.Fprintln(os.Stderr, "windows-spawn-child:stderr")
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWindowsSpawnWritesPIDAndLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "connect.log")
	pidPath := filepath.Join(dir, "connect.pid")
	pid, err := Spawn([]string{"daemonize.test", "child-mode"}, ReExecOptions{
		LogPath:  logPath,
		PIDPath:  pidPath,
		ExtraEnv: []string{windowsSpawnChildEnv + "=1"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = ForceKill(pid) }()
	if got, err := ReadPIDFile(pidPath); err != nil || got != pid {
		t.Fatalf("ReadPIDFile = %d, %v; want pid %d", got, err, pid)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body, readErr := os.ReadFile(logPath)
		if readErr == nil && strings.Contains(string(body), "windows-spawn-child:stdout") && strings.Contains(string(body), "windows-spawn-child:stderr") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	body, _ := os.ReadFile(logPath)
	t.Fatalf("spawn log missing child markers: %q", string(body))
}

func TestWindowsControlPipeCancelsProfile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	profile := fmt.Sprintf("windows-pipe-test-%d", time.Now().UnixNano())
	cleanup, err := StartControlServer(ctx, profile, cancel)
	if err != nil {
		t.Fatalf("StartControlServer: %v", err)
	}
	defer cleanup()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := sendStopPipe(profile); err == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				t.Fatal("stop pipe did not cancel context")
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stop pipe never became available")
}

func TestWindowsReadPIDFileStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.pid")
	if err := os.WriteFile(path, []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadPIDFile(path)
	if !errors.Is(err, ErrStaleOrCorrupt) {
		t.Fatalf("ReadPIDFile error = %v, want ErrStaleOrCorrupt", err)
	}
}

func TestWindowsControlPipeNamesAreProfileIsolated(t *testing.T) {
	if ControlPipeName("alpha") == ControlPipeName("beta") {
		t.Fatal("different profiles must not share a control pipe")
	}
	if !strings.HasPrefix(ControlPipeName("alpha"), `\\.\pipe\parsar-daemon-`) {
		t.Fatalf("unexpected pipe name %q", ControlPipeName("alpha"))
	}
}

func TestWindowsForceKillChild(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestWindowsForceKillHelper", "--", "sleep")
	cmd.Env = append(os.Environ(), "PARSAR_DAEMON_WINDOWS_FORCE_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := SignalAndWait(pid, 100*time.Millisecond); err != nil {
		t.Fatalf("SignalAndWait: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child exited successfully; want force termination")
	}
}

func TestWindowsForceKillHelper(t *testing.T) {
	if os.Getenv("PARSAR_DAEMON_WINDOWS_FORCE_CHILD") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}
