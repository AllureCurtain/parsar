//go:build !windows

package clirunner

import (
	"context"
	"os/exec"
	"syscall"
)

type processPlatform struct{}

func newCommand(ctx context.Context, binary string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, binary, args...)
}

func newProcessPlatform(_ *exec.Cmd) (processPlatform, error) { return processPlatform{}, nil }

func (processPlatform) graceful(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

func (processPlatform) force(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGKILL)
}

func (processPlatform) close() {}
