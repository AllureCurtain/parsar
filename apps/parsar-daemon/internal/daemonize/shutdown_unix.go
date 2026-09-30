//go:build !windows

package daemonize

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// InstallShutdownHandler wires the existing SIGINT/SIGTERM lifecycle on Unix.
func InstallShutdownHandler(ctx context.Context, profile string, cancel context.CancelFunc) (func(), error) {
	stopCleanup, err := StartControlServer(ctx, profile, cancel)
	if err != nil {
		return nil, err
	}
	interruptCleanup := InstallInterruptHandler(ctx, cancel)
	return func() {
		interruptCleanup()
		stopCleanup()
	}, nil
}

// InstallInterruptHandler wires SIGINT/SIGTERM without opening a daemon
// control endpoint. It is used by foreground commands such as `logs -f`.
func InstallInterruptHandler(ctx context.Context, cancel context.CancelFunc) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
	return func() {
		signal.Stop(sigCh)
	}
}
