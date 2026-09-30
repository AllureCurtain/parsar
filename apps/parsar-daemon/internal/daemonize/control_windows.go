//go:build windows

package daemonize

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const stopPipeMessage = "stop\n"

// StartControlServer listens on a user-scoped named pipe and invokes cancel
// after receiving the exact local lifecycle message. It intentionally serves
// one request: stop is idempotent and the daemon exits immediately afterward.
func StartControlServer(ctx context.Context, profile string, cancel context.CancelFunc) (func(), error) {
	if strings.TrimSpace(profile) == "" {
		return nil, errors.New("daemonize: profile required for Windows stop pipe")
	}
	pipe, err := createControlPipe(profile)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	cleanup := func() { once.Do(func() { _ = windows.CloseHandle(pipe) }) }
	go func() {
		defer cleanup()
		if err := windows.ConnectNamedPipe(pipe, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			return
		}
		buf := make([]byte, len(stopPipeMessage)+16)
		var n uint32
		if err := windows.ReadFile(pipe, buf, &n, nil); err != nil {
			return
		}
		if strings.TrimSpace(string(buf[:n])) == "stop" {
			cancel()
		}
	}()
	go func() {
		<-ctx.Done()
		cleanup()
	}()
	return cleanup, nil
}

func createControlPipe(profile string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(ControlPipeName(profile))
	if err != nil {
		return 0, fmt.Errorf("daemonize: stop pipe name: %w", err)
	}
	sd, err := userOnlySecurityDescriptor()
	if err != nil {
		return 0, fmt.Errorf("daemonize: stop pipe security: %w", err)
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	h, err := windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_INBOUND,
		windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_WAIT,
		1,
		1024,
		1024,
		0,
		sa,
	)
	if err != nil {
		return 0, err
	}
	return h, nil
}

func userOnlySecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	// Grant Generic All only to the token owner. D:P makes the DACL
	// protected from inheriting broader machine-wide pipe permissions.
	return windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
}

func sendStopPipe(profile string) error {
	name, err := windows.UTF16PtrFromString(ControlPipeName(profile))
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var n uint32
	if err := windows.WriteFile(h, []byte(stopPipeMessage), &n, nil); err != nil {
		return err
	}
	return windows.FlushFileBuffers(h)
}
