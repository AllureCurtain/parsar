package daemonize

import (
	"crypto/sha256"
	"encoding/hex"
)

// ControlPipeName returns a stable per-profile local control endpoint. The
// profile is hashed so validated profile names never become an object-name
// injection surface and the resulting name stays within Windows limits.
func ControlPipeName(profile string) string {
	digest := sha256.Sum256([]byte(profile))
	return `\\.\pipe\parsar-daemon-` + hex.EncodeToString(digest[:12])
}
