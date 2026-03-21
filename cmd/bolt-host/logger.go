package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const maxLogSize = 256 * 1024 // 256KB

// logger writes timestamped, trace-tagged lines to a log file.
type logger struct {
	path string
}

func newLogger() *logger {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return &logger{
		path: filepath.Join(dir, "bolt", "logs", "bolt-host.log"),
	}
}

// Log writes a formatted log line with timestamp and trace ID.
func (l *logger) Log(traceID, format string, args ...any) {
	if traceID == "" {
		traceID = "--"
	}
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), traceID, msg)

	_ = os.MkdirAll(filepath.Dir(l.path), 0o755)

	// Check size and truncate if over limit before opening for append
	if info, err := os.Stat(l.path); err == nil && info.Size() > maxLogSize {
		_ = os.Truncate(l.path, 0)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	f.WriteString(line)
}
