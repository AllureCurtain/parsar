//go:build windows

package daemonize

import (
	"io"
	"os/exec"

	"golang.org/x/sys/windows"
)

// startDetached starts the daemon without inheriting the caller's console.
// DETACHED_PROCESS keeps the child alive when the PowerShell session exits;
// HideWindow avoids briefly flashing a console for a .exe invocation.
func startDetached(executable string, args, env []string, stdin io.Reader, output io.Writer) (int, error) {
	cmd := exec.Command(executable, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
