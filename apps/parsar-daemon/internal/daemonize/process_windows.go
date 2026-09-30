//go:build windows

package daemonize

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// IsAlive reports whether a Windows process exists and is accessible to the
// current user. Querying the exit code avoids treating a reused PID as live
// when the process has already exited.
func IsAlive(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("daemonize.IsAlive: invalid pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return fmt.Errorf("%w (pid=%d)", ErrNotRunning, pid)
		}
		return err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return err
	}
	if code != stillActive {
		return fmt.Errorf("%w (pid=%d)", ErrNotRunning, pid)
	}
	return nil
}

// RequestStop asks the daemon's per-profile named pipe to cancel its root
// context. A missing pipe is intentionally reported so SignalAndWait can
// fall back to native termination when the daemon is unhealthy.
func RequestStop(pid int, profile ...string) error {
	if len(profile) == 0 || profile[0] == "" {
		return errors.New("daemonize: Windows stop pipe profile unavailable")
	}
	if err := sendStopPipe(profile[0]); err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return fmt.Errorf("daemonize: stop pipe unavailable for pid=%d: %w", pid, err)
		}
		return fmt.Errorf("daemonize: stop pipe pid=%d: %w", pid, err)
	}
	return nil
}

// ForceKill terminates the process. Agent subprocess trees are owned by the
// runner's Job Object; the daemon itself has no parent tree to enumerate.
func ForceKill(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("daemonize.ForceKill: invalid pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("daemonize: open pid=%d for termination: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("daemonize: terminate pid=%d: %w", pid, err)
	}
	_, _ = windows.WaitForSingleObject(h, 50)
	return nil
}

// SignalAndWait requests the named-pipe graceful path, then force-kills when
// no control channel is available or the daemon does not exit in time.
func SignalAndWait(pid int, timeout time.Duration, profile ...string) error {
	if err := RequestStop(pid, profile...); err != nil {
		if errors.Is(err, ErrNotRunning) {
			return nil
		}
		return ForceKill(pid)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := IsAlive(pid); err != nil {
			if errors.Is(err, ErrNotRunning) {
				return nil
			}
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ForceKill(pid)
}
