//go:build !windows

package daemonize

import "context"

// StartControlServer is a no-op on Unix; SIGTERM remains the native graceful
// lifecycle mechanism there.
func StartControlServer(_ context.Context, _ string, _ context.CancelFunc) (func(), error) {
	return func() {}, nil
}
