package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// xdgRuntimeDir returns the XDG runtime directory, falling back to /tmp/bolt-<uid>.
func xdgRuntimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return fmt.Sprintf("/tmp/bolt-%d", os.Getuid())
}

// socketPath returns the path for the daemon's Unix socket.
func socketPath() string {
	return filepath.Join(xdgRuntimeDir(), "bolt", "bolt.sock")
}

// isAlreadyRunning checks if another daemon is already listening on the given socket path.
func isAlreadyRunning(path string) bool {
	conn, err := net.DialTimeout("unix", path, 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// createSocketListener creates a Unix socket listener at the given path.
// It creates parent directories with mode 0700 and sets the socket to mode 0600.
func createSocketListener(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating socket directory: %w", err)
	}

	// Remove stale socket file
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on unix socket: %w", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("setting socket permissions: %w", err)
	}

	return ln, nil
}

// removeSocket removes the socket file at the given path.
func removeSocket(path string) {
	_ = os.Remove(path)
}
