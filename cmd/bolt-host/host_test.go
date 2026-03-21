package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

// encodeNativeMessage encodes a JSON value as a native messaging message.
func encodeNativeMessage(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}

// decodeNativeMessage decodes a native messaging message from a buffer.
func decodeNativeMessage(t *testing.T, buf *bytes.Buffer) response {
	t.Helper()
	msg, err := readMessage(buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var resp response
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestHostEndToEnd(t *testing.T) {
	// Set up a mock daemon
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/stats":
			json.NewEncoder(w).Encode(map[string]any{
				"version":      "0.4.0-dev",
				"active_count": 0,
			})
		case "/api/downloads":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"download": map[string]any{
					"id":       "01ABC",
					"filename": "test.zip",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	sockPath := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: handler}}
	srv.Start()
	defer srv.Close()

	// Build stdin with two commands: ping then add_download
	stdin := new(bytes.Buffer)
	stdin.Write(encodeNativeMessage(t, map[string]string{"command": "ping"}))
	stdin.Write(encodeNativeMessage(t, map[string]any{
		"command": "add_download",
		"data":    map[string]string{"url": "https://example.com/test.zip"},
	}))

	stdout := new(bytes.Buffer)
	var mu sync.Mutex

	h := &host{
		relay:  newRelay(sockPath),
		stdout: stdout,
		mu:     &mu,
		logger: &logger{path: filepath.Join(t.TempDir(), "test.log")},
	}
	h.run(stdin)

	// Decode two responses
	resp1 := decodeNativeMessage(t, stdout)
	if resp1.Command != "ping" || !resp1.Success {
		t.Fatalf("ping response: %+v", resp1)
	}

	resp2 := decodeNativeMessage(t, stdout)
	if resp2.Command != "add_download" || !resp2.Success {
		t.Fatalf("add_download response: %+v", resp2)
	}
}

func TestHostDaemonDown(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "nonexistent.sock")

	stdin := new(bytes.Buffer)
	stdin.Write(encodeNativeMessage(t, map[string]string{"command": "ping"}))

	stdout := new(bytes.Buffer)
	var mu sync.Mutex

	h := &host{
		relay:  newRelay(sockPath),
		stdout: stdout,
		mu:     &mu,
		logger: &logger{path: filepath.Join(t.TempDir(), "test.log")},
	}
	h.run(stdin)

	resp := decodeNativeMessage(t, stdout)
	if resp.Command != "ping" {
		t.Fatalf("expected ping command, got %q", resp.Command)
	}
	if resp.Success {
		t.Fatal("expected failure for unavailable daemon")
	}
	if resp.Error != "daemon_unavailable" {
		t.Fatalf("expected daemon_unavailable, got %q", resp.Error)
	}
}
