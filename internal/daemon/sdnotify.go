package daemon

import (
	"net"
	"os"
)

// sdNotify sends a notification to systemd via the NOTIFY_SOCKET.
// It is a no-op if NOTIFY_SOCKET is not set (i.e., not running under systemd).
func sdNotify(state string) {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	if socketPath == "" {
		return
	}

	conn, err := net.Dial("unixgram", socketPath)
	if err != nil {
		return
	}
	defer conn.Close()

	_, _ = conn.Write([]byte(state))
}
