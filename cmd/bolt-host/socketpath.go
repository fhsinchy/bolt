package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketPath returns the Bolt daemon Unix socket path.
// Mirrors internal/daemon/socket.go logic — kept separate to avoid
// importing daemon internals.
func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/tmp/bolt-%d", os.Getuid())
	}
	return filepath.Join(dir, "bolt", "bolt.sock")
}
