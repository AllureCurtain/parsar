//go:build !windows

package daemonize

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// IsAlive reports whether a PID exists and is signalable by this user.
func IsAlive(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("daemonize.IsAlive: invalid pid %d", pid)
	}
	if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("%w (pid=%d)", ErrNotRunning, pid)
		}
		return err
	}
	return nil
}

// RequestStop asks a Unix daemon to exit via SIGTERM.
func RequestStop(pid int, _ ...string) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("%w (pid=%d)", ErrNotRunning, pid)
		}
		return fmt.Errorf("daemonize: SIGTERM pid=%d: %w", pid, err)
	}
	return nil
}

// ForceKill terminates a Unix daemon with SIGKILL.
func ForceKill(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("daemonize: SIGKILL pid=%d: %w", pid, err)
	}
	return nil
}

// SignalAndWait requests graceful shutdown, then force-kills after timeout.
func SignalAndWait(pid int, timeout time.Duration, profile ...string) error {
	if err := RequestStop(pid, profile...); err != nil {
		if errors.Is(err, ErrNotRunning) {
			return nil
		}
		return err
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
	if err := ForceKill(pid); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return nil
}
