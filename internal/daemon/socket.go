package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// xdgRuntimeDir returns the XDG runtime directory, falling back to /tmp/bolt-<uid>.
func xdgRuntimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return fmt.Sprintf("/tmp/bolt-%d", os.Getuid())
}

// verifyDirOwnership checks that dir exists, is not a symlink, and is owned
// by the current user. Returns an error if any check fails.
func verifyDirOwnership(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("socket directory %s is a symlink", dir)
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if ok && stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("socket directory %s is not owned by current user (uid %d, want %d)", dir, stat.Uid, os.Getuid())
	}
	return nil
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

	// Verify the socket directory is not a symlink and is owned by us.
	if err := verifyDirOwnership(dir); err != nil {
		return nil, fmt.Errorf("socket directory verification: %w", err)
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
