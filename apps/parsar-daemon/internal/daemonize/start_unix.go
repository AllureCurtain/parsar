//go:build !windows

package daemonize

import (
	"io"
	"os/exec"
	"syscall"
)

// startDetached starts a re-execed daemon in a new session. The parent does
// not retain the child handle, so init (or the platform's equivalent) owns
// reaping after the parent exits.
func startDetached(executable string, args, env []string, stdin io.Reader, output io.Writer) (int, error) {
	cmd := exec.Command(executable, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
