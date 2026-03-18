package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestServer creates an httptest.Server listening on a temp Unix socket.
// Returns the server and socket path.
func newTestServer(t *testing.T, handler http.Handler) (*httptest.Server, string) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &httptest.Server{
		Listener: ln,
		Config:   &http.Server{Handler: handler},
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, sockPath
}

func TestRelayPing(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/stats" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"version":      "0.4.0-dev",
			"active_count": 1,
		})
	})
	_, sockPath := newTestServer(t, handler)

	r := newRelay(sockPath)
	resp, err := r.execute(command{Command: "ping"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
}

func TestRelayAddDownload(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/downloads" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["url"] != "https://example.com/file.zip" {
			t.Errorf("unexpected url: %v", body["url"])
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"download": map[string]any{
				"id":       "01ABC",
				"filename": "file.zip",
			},
		})
	})
	_, sockPath := newTestServer(t, handler)

	r := newRelay(sockPath)
	data := json.RawMessage(`{"url":"https://example.com/file.zip"}`)
	resp, err := r.execute(command{Command: "add_download", Data: &data})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
}

func TestRelayDaemonUnavailable(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "nonexistent.sock")
	r := newRelay(sockPath)
	resp, err := r.execute(command{Command: "ping"})
	if err != nil {
		t.Fatalf("execute should not return error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure for unavailable daemon")
	}
	if resp.Error != "daemon_unavailable" {
		t.Fatalf("expected daemon_unavailable, got %q", resp.Error)
	}
}

func TestRelayDaemonError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"error": "file already exists",
			"code":  "DUPLICATE_FILENAME",
		})
	})
	_, sockPath := newTestServer(t, handler)

	r := newRelay(sockPath)
	data := json.RawMessage(`{"url":"https://example.com/dup.zip"}`)
	resp, err := r.execute(command{Command: "add_download", Data: &data})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure for duplicate")
	}
	if resp.Error != "duplicate_filename" {
		t.Fatalf("expected duplicate_filename, got %q", resp.Error)
	}
}

func TestRelayUnknownCommand(t *testing.T) {
	// Unknown commands should fail without hitting the daemon
	sockPath := filepath.Join(t.TempDir(), "nonexistent.sock")
	r := newRelay(sockPath)
	resp, err := r.execute(command{Command: "bogus"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Success {
		t.Fatal("expected failure for unknown command")
	}
	if resp.Error != "unknown_command" {
		t.Fatalf("expected unknown_command, got %q", resp.Error)
	}
}

func TestRelayProbe(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/probe" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"filename":   "file.zip",
			"total_size": 1048576,
		})
	})
	_, sockPath := newTestServer(t, handler)

	r := newRelay(sockPath)
	data := json.RawMessage(`{"url":"https://example.com/file.zip"}`)
	resp, err := r.execute(command{Command: "probe", Data: &data})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
}

func TestSocketPath(t *testing.T) {
	// Test with XDG_RUNTIME_DIR set
	orig := os.Getenv("XDG_RUNTIME_DIR")
	t.Cleanup(func() { os.Setenv("XDG_RUNTIME_DIR", orig) })

	os.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	got := socketPath()
	want := "/run/user/1000/bolt/bolt.sock"
	if got != want {
		t.Fatalf("socketPath() = %q, want %q", got, want)
	}
}

func TestSocketPathFallback(t *testing.T) {
	orig := os.Getenv("XDG_RUNTIME_DIR")
	t.Cleanup(func() { os.Setenv("XDG_RUNTIME_DIR", orig) })

	os.Unsetenv("XDG_RUNTIME_DIR")
	got := socketPath()
	// Should contain /tmp/bolt- prefix
	if len(got) == 0 {
		t.Fatal("socketPath() returned empty string")
	}
}
