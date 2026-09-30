//go:build windows

package daemonize

import (
	"context"
	"os"
	"os/signal"
)

// InstallShutdownHandler wires the profile named pipe and Ctrl+C handling.
// Windows has no portable SIGTERM delivery, so stop uses the named pipe and
// console interruption remains available for foreground runs.
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

// InstallInterruptHandler wires Ctrl+C without opening a daemon control
// endpoint. It is used by foreground commands such as `logs -f`.
func InstallInterruptHandler(ctx context.Context, cancel context.CancelFunc) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
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
