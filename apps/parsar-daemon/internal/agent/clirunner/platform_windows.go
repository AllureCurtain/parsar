//go:build windows

package clirunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processPlatform struct {
	job windows.Handle
}

func newCommand(_ context.Context, binary string, args ...string) *exec.Cmd {
	return exec.Command(binary, args...)
}

// newProcessPlatform puts every CLI process in a kill-on-close Job Object so
// grandchildren cannot survive daemon cancellation. Some hosts disallow
// nested jobs; those hosts retain the standard Process.Kill fallback.
func newProcessPlatform(cmd *exec.Cmd) (processPlatform, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return processPlatform{}, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return processPlatform{}, fmt.Errorf("configure job object: %w", err)
	}
	if cmd == nil || cmd.Process == nil {
		_ = windows.CloseHandle(job)
		return processPlatform{}, errors.New("process missing after start")
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return processPlatform{}, fmt.Errorf("open process for job assignment: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return processPlatform{}, fmt.Errorf("assign process to job object: %w", err)
	}
	return processPlatform{job: job}, nil
}

func (p processPlatform) graceful(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

func (p processPlatform) force(cmd *exec.Cmd) error {
	if p.job != 0 {
		if err := windows.TerminateJobObject(p.job, 1); err == nil {
			return nil
		}
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (p processPlatform) close() {
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
	}
}
