package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// runStop requests graceful shutdown through the platform lifecycle channel,
// escalates to native termination after killTimeout, then removes the pidfile.
// It is idempotent: missing or stale pidfiles are cleaned up and exit 0.
func runStop(ctx *runContext, args []string) error {
	fs := newFlagSet("stop")
	profile := fs.String("profile", paths.DefaultProfile, "profile name to stop")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("stop: parse flags: %w", err)
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	pidPath, err := paths.PIDFile(*profile)
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	pid, err := daemonize.ReadPIDFile(pidPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintln(ctx.stdout, "parsar-daemon: no background daemon running (no pidfile)")
		return nil
	case errors.Is(err, daemonize.ErrStaleOrCorrupt):
		fmt.Fprintf(ctx.stdout, "parsar-daemon: stale pidfile detected (%v); removing\n", err)
		if rmErr := daemonize.RemovePIDFile(pidPath); rmErr != nil {
			return fmt.Errorf("stop: %w", rmErr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("stop: read pidfile: %w", err)
	}

	fmt.Fprintf(ctx.stdout, "parsar-daemon: requesting stop for pid=%d\n", pid)
	if err := daemonize.SignalAndWait(pid, killTimeout, *profile); err != nil {
		return fmt.Errorf("stop: signal: %w", err)
	}
	if err := daemonize.RemovePIDFile(pidPath); err != nil {
		return fmt.Errorf("stop: remove pidfile: %w", err)
	}
	fmt.Fprintln(ctx.stdout, "parsar-daemon: stopped")
	return nil
}
