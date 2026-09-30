package daemonize

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrStaleOrCorrupt is returned by ReadPIDFile when the file exists
// but its contents don't look like a live PID. Wraps the underlying
// parse error or syscall.ESRCH for errors.Is.
var ErrStaleOrCorrupt = errors.New("daemonize: pidfile stale or corrupt")

// ErrNotRunning is returned by IsAlive when the PID has no process.
var ErrNotRunning = errors.New("daemonize: process not running")

// WritePIDFile writes pid to path atomically. The temp file lives in
// the same directory so the rename is on the same filesystem
// (required by os.Rename for atomicity). 0o600 matches the profile
// dir's privacy.
func WritePIDFile(path string, pid int) error {
	if path == "" {
		return errors.New("daemonize.WritePIDFile: empty path")
	}
	if pid <= 0 {
		return fmt.Errorf("daemonize.WritePIDFile: invalid pid %d", pid)
	}
	tmp := path + ".tmp"
	body := []byte(strconv.Itoa(pid) + "\n")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("daemonize: write tmp pidfile: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("daemonize: rename pidfile: %w", err)
	}
	return nil
}

// ReadPIDFile reads, parses, and liveness-checks the pidfile.
// Missing file returns os.ErrNotExist verbatim; a file whose pid has
// no live process returns ErrStaleOrCorrupt wrapping ErrNotRunning.
func ReadPIDFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if parseErr != nil || pid <= 0 {
		return 0, fmt.Errorf("%w: %q", ErrStaleOrCorrupt, strings.TrimSpace(string(raw)))
	}
	if err := IsAlive(pid); err != nil {
		return pid, fmt.Errorf("%w: pid=%d: %w", ErrStaleOrCorrupt, pid, err)
	}
	return pid, nil
}

// RemovePIDFile deletes the pidfile. Missing files aren't an error so
// `parsar-daemon stop` is idempotent.
func RemovePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemonize: remove pidfile: %w", err)
	}
	return nil
}
