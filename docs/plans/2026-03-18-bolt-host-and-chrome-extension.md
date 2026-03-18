# bolt-host and Chrome Extension Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a native messaging bridge (`bolt-host`) and a fresh Chrome extension so the browser can hand downloads to the Bolt daemon.

**Architecture:** bolt-host is a Go binary (`cmd/bolt-host/`) that reads Chrome native messaging commands from stdin, relays them to the daemon over its Unix socket, and writes responses to stdout. The Chrome extension (Manifest V3) captures download intent and forwards it through bolt-host. V1 is request/response only — no WebSocket event forwarding.

**Tech Stack:** Go (bolt-host), JavaScript (Chrome extension, Manifest V3), Chrome Native Messaging protocol

**Spec:** `docs/specs/2026-03-18-bolt-host-and-chrome-extension.md`

---

## File Structure

### bolt-host (Go)

```
cmd/bolt-host/
  main.go              Entry point — open HTTP client, run command loop, exit on stdin close
  host.go              Host struct, command handler goroutine, stdout mutex
  host_test.go         Unit tests for command routing and error mapping
  nativemsg.go         Native messaging read/write (length-prefixed JSON)
  nativemsg_test.go    Unit tests for encode/decode
  relay.go             HTTP relay to daemon Unix socket
  relay_test.go        Integration tests with httptest on Unix socket
  socketpath.go        Socket path resolution (duplicates daemon's simple logic)
```

### Chrome Extension

```
extensions/chrome/
  manifest.json        Manifest V3 — permissions, service worker, content script
  background.js        Service worker — port management, interception, context menu, fallback
  content.js           Content script — link click interception (rewrite)
  popup/
    popup.html         Status + settings UI (rewrite)
    popup.js           Popup logic (rewrite)
    popup.css          Styling (rewrite)
  icons/               Existing icons (keep as-is)
```

### Modified Files

```
Makefile               Add build-host, update install/uninstall/build-all/clean
CLAUDE.md              Add bolt-host to package layout and commands
```

---

### Task 1: Native messaging read/write

**Files:**
- Create: `cmd/bolt-host/nativemsg.go`
- Create: `cmd/bolt-host/nativemsg_test.go`

- [ ] **Step 1: Write failing tests for native messaging encode/decode**

Create `cmd/bolt-host/nativemsg_test.go`:

```go
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestReadMessage(t *testing.T) {
	payload := []byte(`{"command":"ping"}`)
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint32(len(payload)))
	buf.Write(payload)

	msg, err := readMessage(buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if !bytes.Equal(msg, payload) {
		t.Fatalf("got %q, want %q", msg, payload)
	}
}

func TestReadMessageEOF(t *testing.T) {
	buf := new(bytes.Buffer)
	_, err := readMessage(buf)
	if err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestWriteMessage(t *testing.T) {
	payload := []byte(`{"command":"ping","success":true}`)
	buf := new(bytes.Buffer)

	err := writeMessage(buf, payload)
	if err != nil {
		t.Fatalf("writeMessage: %v", err)
	}

	// Read back length prefix
	var length uint32
	binary.Read(buf, binary.LittleEndian, &length)
	if int(length) != len(payload) {
		t.Fatalf("length prefix: got %d, want %d", length, len(payload))
	}

	got := buf.Bytes()
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestWriteMessageTooLarge(t *testing.T) {
	// Chrome native messaging has a 1 MB limit
	large := make([]byte, 1024*1024+1)
	buf := new(bytes.Buffer)
	err := writeMessage(buf, large)
	if err == nil {
		t.Fatal("expected error for message exceeding 1 MB")
	}
}

func TestRoundTrip(t *testing.T) {
	original := map[string]any{"command": "ping"}
	data, _ := json.Marshal(original)

	buf := new(bytes.Buffer)
	writeMessage(buf, data)

	got, err := readMessage(buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}

	var decoded map[string]any
	json.Unmarshal(got, &decoded)
	if decoded["command"] != "ping" {
		t.Fatalf("round-trip failed: %v", decoded)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/bolt-host/ -run TestReadMessage -v`
Expected: FAIL — `readMessage` not defined

- [ ] **Step 3: Implement native messaging read/write**

Create `cmd/bolt-host/nativemsg.go`:

```go
package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

const maxMessageSize = 1024 * 1024 // 1 MB Chrome native messaging limit

// readMessage reads a Chrome native messaging length-prefixed JSON message.
func readMessage(r io.Reader) ([]byte, error) {
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return nil, fmt.Errorf("reading message length: %w", err)
	}
	if length > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", length, maxMessageSize)
	}
	msg := make([]byte, length)
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, fmt.Errorf("reading message body: %w", err)
	}
	return msg, nil
}

// writeMessage writes a Chrome native messaging length-prefixed JSON message.
func writeMessage(w io.Writer, msg []byte) error {
	if len(msg) > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max %d)", len(msg), maxMessageSize)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(msg))); err != nil {
		return fmt.Errorf("writing message length: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("writing message body: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/bolt-host/ -v -count=1`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/bolt-host/nativemsg.go cmd/bolt-host/nativemsg_test.go
git commit -m "feat(bolt-host): add native messaging read/write with tests"
```

---

### Task 2: Socket path and HTTP relay

**Files:**
- Create: `cmd/bolt-host/socketpath.go`
- Create: `cmd/bolt-host/relay.go`
- Create: `cmd/bolt-host/relay_test.go`

- [ ] **Step 1: Create socket path helper**

Create `cmd/bolt-host/socketpath.go`:

```go
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
```

- [ ] **Step 2: Write failing tests for HTTP relay**

Create `cmd/bolt-host/relay_test.go`:

```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./cmd/bolt-host/ -run TestRelay -v`
Expected: FAIL — `newRelay`, `command`, `response` not defined

- [ ] **Step 4: Implement the HTTP relay**

Create `cmd/bolt-host/relay.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// command is the incoming request from the Chrome extension.
type command struct {
	Command string           `json:"command"`
	Data    *json.RawMessage `json:"data,omitempty"`
}

// response is the outgoing response to the Chrome extension.
type response struct {
	Command string           `json:"command"`
	Success bool             `json:"success"`
	Error   string           `json:"error,omitempty"`
	Data    *json.RawMessage `json:"data,omitempty"`
}

// relay handles HTTP communication with the Bolt daemon over a Unix socket.
type relay struct {
	client *http.Client
	sock   string
}

func newRelay(socketPath string) *relay {
	return &relay{
		sock: socketPath,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// execute dispatches a command to the daemon and returns a response.
func (r *relay) execute(cmd command) (response, error) {
	switch cmd.Command {
	case "ping":
		return r.doRequest(cmd, http.MethodGet, "/api/stats", nil)
	case "add_download":
		return r.doRequest(cmd, http.MethodPost, "/api/downloads", cmd.Data)
	case "probe":
		return r.doRequest(cmd, http.MethodPost, "/api/probe", cmd.Data)
	default:
		return response{
			Command: cmd.Command,
			Success: false,
			Error:   "unknown_command",
		}, nil
	}
}

func (r *relay) doRequest(cmd command, method, path string, body *json.RawMessage) (response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(*body)
	}

	req, err := http.NewRequest(method, "http://localhost"+path, bodyReader)
	if err != nil {
		return response{Command: cmd.Command, Success: false, Error: "internal_error"}, nil
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return response{Command: cmd.Command, Success: false, Error: "daemon_unavailable"}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{Command: cmd.Command, Success: false, Error: "read_error"}, nil
	}

	data := json.RawMessage(respBody)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return response{
			Command: cmd.Command,
			Success: true,
			Data:    &data,
		}, nil
	}

	// Extract error code from daemon response
	var errResp struct {
		Code string `json:"code"`
	}
	json.Unmarshal(respBody, &errResp)

	errCode := "request_failed"
	if errResp.Code != "" {
		errCode = strings.ToLower(errResp.Code)
	}

	return response{
		Command: cmd.Command,
		Success: false,
		Error:   errCode,
		Data:    &data,
	}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/bolt-host/ -v -count=1`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/bolt-host/socketpath.go cmd/bolt-host/relay.go cmd/bolt-host/relay_test.go
git commit -m "feat(bolt-host): add Unix socket HTTP relay with tests"
```

---

### Task 3: Host command loop and entry point

**Files:**
- Create: `cmd/bolt-host/host.go`
- Create: `cmd/bolt-host/host_test.go`
- Create: `cmd/bolt-host/main.go`

- [ ] **Step 1: Write failing tests for the host command loop**

Create `cmd/bolt-host/host_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/bolt-host/ -run TestHost -v`
Expected: FAIL — `host` type not defined

- [ ] **Step 3: Implement the host command loop**

Create `cmd/bolt-host/host.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"log"
	"sync"
)

// host manages the native messaging command loop.
type host struct {
	relay  *relay
	stdout io.Writer
	mu     *sync.Mutex
}

// run reads commands from r until EOF, relays each to the daemon, and writes
// responses to stdout. This is the main command handler goroutine.
func (h *host) run(r io.Reader) {
	for {
		msg, err := readMessage(r)
		if err != nil {
			// EOF means the extension disconnected — exit cleanly.
			return
		}

		var cmd command
		if err := json.Unmarshal(msg, &cmd); err != nil {
			h.writeResponse(response{
				Command: "",
				Success: false,
				Error:   "invalid_json",
			})
			continue
		}

		resp, err := h.relay.execute(cmd)
		if err != nil {
			log.Printf("relay error: %v", err)
			resp = response{
				Command: cmd.Command,
				Success: false,
				Error:   "internal_error",
			}
		}

		h.writeResponse(resp)
	}
}

func (h *host) writeResponse(resp response) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := writeMessage(h.stdout, data); err != nil {
		log.Printf("write error: %v", err)
	}
}
```

- [ ] **Step 4: Create the entry point**

Create `cmd/bolt-host/main.go`:

```go
package main

import (
	"fmt"
	"os"
	"sync"
)

var version = "dev"

func main() {
	args := os.Args[1:]

	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			fmt.Printf("bolt-host %s\n", version)
			return
		case "help", "--help", "-h":
			fmt.Print("bolt-host - Chrome native messaging bridge for Bolt\n\nThis binary is spawned by Chrome. Do not run it directly.\n")
			return
		}
	}

	sock := socketPath()
	r := newRelay(sock)

	var mu sync.Mutex
	h := &host{
		relay:  r,
		stdout: os.Stdout,
		mu:     &mu,
	}
	h.run(os.Stdin)
}
```

- [ ] **Step 5: Run all bolt-host tests**

Run: `go test ./cmd/bolt-host/ -v -count=1`
Expected: All PASS

- [ ] **Step 6: Build bolt-host to verify it compiles**

Run: `CGO_ENABLED=0 go build -o bolt-host ./cmd/bolt-host/`
Expected: Binary created

- [ ] **Step 7: Verify existing daemon tests still pass**

Run: `go test ./... -count=1 -timeout 120s`
Expected: All PASS (bolt-host tests included, daemon tests unaffected)

- [ ] **Step 8: Commit**

```bash
git add cmd/bolt-host/
git commit -m "feat(bolt-host): add command loop and entry point with end-to-end tests"
```

---

### Task 4: Update Makefile and CLAUDE.md

**Files:**
- Modify: `Makefile`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add bolt-host targets to Makefile**

Add `build-host` to `.PHONY` line. Add the following sections:

After the `build:` target, add:

```makefile
build-host:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bolt-host ./cmd/bolt-host/
```

Update `install:` — after `cp $(BINARY) ~/.local/bin/`, add:

```makefile
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bolt-host ./cmd/bolt-host/
	cp bolt-host ~/.local/bin/
	@for dir in ~/.config/google-chrome/NativeMessagingHosts ~/.config/chromium/NativeMessagingHosts; do \
		if [ -d "$$(dirname $$dir)" ]; then \
			mkdir -p $$dir; \
			sed 's|BOLT_HOST_PATH|$(HOME)/.local/bin/bolt-host|' packaging/com.fhsinchy.bolt.json > $$dir/com.fhsinchy.bolt.json; \
		fi; \
	done
```

Update `uninstall:` — after `rm -f ~/.local/bin/$(BINARY)`, add:

```makefile
	rm -f ~/.local/bin/bolt-host
	rm -f ~/.config/google-chrome/NativeMessagingHosts/com.fhsinchy.bolt.json
	rm -f ~/.config/chromium/NativeMessagingHosts/com.fhsinchy.bolt.json
```

Update `clean:` — after `rm -f $(BINARY)`, add:

```makefile
	rm -f bolt-host
```

Update `build-all:`:

```makefile
build-all: build build-host build-qt
```

Update `.PHONY`:

```makefile
.PHONY: build build-host test test-race test-v test-stress test-cover install uninstall clean \
        build-qt test-qt build-all test-all
```

- [ ] **Step 2: Create native messaging manifest template**

Create `packaging/com.fhsinchy.bolt.json`:

```json
{
    "name": "com.fhsinchy.bolt",
    "description": "Bolt Download Manager bridge",
    "path": "BOLT_HOST_PATH",
    "type": "stdio",
    "allowed_origins": ["chrome-extension://EXTENSION_ID_PLACEHOLDER/"]
}
```

Note: `EXTENSION_ID_PLACEHOLDER` will be replaced with the real extension ID once a `key` is set in `manifest.json` and the deterministic ID is known.

- [ ] **Step 3: Update CLAUDE.md package layout**

In `### Package Layout`, after the `bolt-qt/` section, add:

```
cmd/bolt-host/
  main.go                  Chrome native messaging bridge entry point
  host.go                  Command handler goroutine
  nativemsg.go             Native messaging read/write (length-prefixed JSON)
  relay.go                 HTTP relay to daemon Unix socket
  socketpath.go            Socket path resolution
```

In `## Commands`, add after `make build`:

```
make build-host  # CGO_ENABLED=0 go build → ./bolt-host
```

Update `make build-all` description:

```
make build-all   # build all components (daemon + bolt-host + Qt stub)
```

- [ ] **Step 4: Verify Makefile targets work**

Run: `make build && make build-host && make build-all && make clean`
Expected: Both binaries build. `make clean` removes both. `make build-all` builds both plus prints Qt stub.

- [ ] **Step 5: Verify all tests pass**

Run: `make test`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add Makefile packaging/com.fhsinchy.bolt.json CLAUDE.md
git commit -m "chore: add bolt-host build targets and native messaging manifest template"
```

---

### Task 5: Chrome extension — manifest and background service worker

**Files:**
- Delete: `extensions/chrome/background.js`, `extensions/chrome/content.js`, `extensions/chrome/manifest.json`, `extensions/chrome/popup/`, `extensions/chrome/welcome/`, `extensions/chrome/PRIVACY.md`
- Keep: `extensions/chrome/icons/`
- Create: `extensions/chrome/manifest.json`
- Create: `extensions/chrome/background.js`

- [ ] **Step 1: Delete existing extension files (keep icons)**

```bash
rm extensions/chrome/background.js extensions/chrome/content.js extensions/chrome/manifest.json extensions/chrome/PRIVACY.md
rm -rf extensions/chrome/popup extensions/chrome/welcome
```

- [ ] **Step 2: Create manifest.json**

Create `extensions/chrome/manifest.json`:

```json
{
  "manifest_version": 3,
  "name": "Bolt Download Manager",
  "version": "2.0.0",
  "description": "Hand off browser downloads to Bolt",
  "permissions": [
    "downloads",
    "contextMenus",
    "storage",
    "cookies",
    "nativeMessaging",
    "notifications"
  ],
  "host_permissions": ["<all_urls>"],
  "background": {
    "service_worker": "background.js"
  },
  "content_scripts": [
    {
      "matches": ["<all_urls>"],
      "js": ["content.js"],
      "run_at": "document_idle"
    }
  ],
  "action": {
    "default_popup": "popup/popup.html",
    "default_icon": {
      "16": "icons/icon-16.png",
      "48": "icons/icon-48.png",
      "128": "icons/icon-128.png"
    }
  },
  "icons": {
    "16": "icons/icon-16.png",
    "48": "icons/icon-48.png",
    "128": "icons/icon-128.png"
  }
}
```

- [ ] **Step 3: Create background.js**

Create `extensions/chrome/background.js`:

```javascript
const HOST_NAME = "com.fhsinchy.bolt";

// --- Connection state ---
// "host_unavailable" | "daemon_unavailable" | "ready"
let connectionState = "host_unavailable";
let port = null;
let pendingCallbacks = [];
let failureNotified = false;
let reinitiatedUrls = new Set();

// --- Port management ---

function ensurePort() {
  return new Promise((resolve) => {
    if (port) {
      resolve(port);
      return;
    }
    try {
      port = chrome.runtime.connectNative(HOST_NAME);
    } catch {
      connectionState = "host_unavailable";
      resolve(null);
      return;
    }

    port.onDisconnect.addListener(() => {
      connectionState = "host_unavailable";
      port = null;
      // Reject all pending callbacks
      const cbs = pendingCallbacks.splice(0);
      cbs.forEach((cb) => cb(null));
    });

    port.onMessage.addListener((msg) => {
      const cb = pendingCallbacks.shift();
      if (cb) cb(msg);
    });

    // Ping to determine state
    sendCommand({ command: "ping" }).then((resp) => {
      // Guard: if onDisconnect fired during the ping, port is null
      // and connectionState is already "host_unavailable" — don't overwrite.
      if (!port) {
        resolve(null);
        return;
      }
      if (resp && resp.success) {
        connectionState = "ready";
        failureNotified = false;
      } else {
        connectionState = "daemon_unavailable";
      }
      resolve(port);
    });
  });
}

function sendCommand(cmd) {
  return new Promise((resolve) => {
    if (!port) {
      resolve(null);
      return;
    }
    pendingCallbacks.push(resolve);
    port.postMessage(cmd);
  });
}

async function sendWithConnection(cmd) {
  await ensurePort();
  if (connectionState === "host_unavailable") return null;
  return sendCommand(cmd);
}

// --- Context menu ---

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.create({
    id: "download-with-bolt",
    title: "Download with Bolt",
    contexts: ["link"],
  });
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  if (info.menuItemId !== "download-with-bolt") return;

  const url = info.linkUrl;
  if (!url) return;

  const headers = await collectHeaders(url, tab?.url);

  const resp = await sendWithConnection({
    command: "add_download",
    data: { url, headers, referer_url: tab?.url || "" },
  });

  if (resp && resp.success) {
    const filename = resp.data?.download?.filename || url.split("/").pop();
    showNotification("Sent to Bolt", filename);
  } else {
    chrome.downloads.download({ url });
    showNotification("Bolt unavailable", "Downloading normally");
  }
});

// --- Automatic capture ---

chrome.downloads.onCreated.addListener(async (downloadItem) => {
  const url = downloadItem.url;

  // Skip data: URLs, blob: URLs, and chrome: URLs
  if (!url || !url.startsWith("http")) return;

  // Re-interception prevention
  if (reinitiatedUrls.has(url)) {
    reinitiatedUrls.delete(url);
    return;
  }

  // Check if capture is enabled
  const settings = await chrome.storage.local.get({
    captureEnabled: false,
    minFileSize: 0,
    extensionWhitelist: [],
    extensionBlacklist: [],
    domainBlocklist: [],
  });

  if (!settings.captureEnabled) return;

  // Apply filters
  if (!passesFilters(url, downloadItem.totalBytes, settings)) return;

  // Check connection
  if (connectionState !== "ready") {
    await ensurePort();
    if (connectionState !== "ready") return;
  }

  // Cancel browser download
  chrome.downloads.cancel(downloadItem.id);

  const headers = await collectHeaders(url, downloadItem.referrer);

  const resp = await sendCommand({
    command: "add_download",
    data: {
      url,
      headers,
      referer_url: downloadItem.referrer || "",
      filename: downloadItem.filename || "",  // daemon's AddRequest accepts filename as a hint
    },
  });

  if (!resp || !resp.success) {
    // Fallback: re-initiate browser download
    reinitiatedUrls.add(url);
    chrome.downloads.download({ url });
    if (!failureNotified) {
      showNotification("Bolt unavailable", "Downloading normally");
      failureNotified = true;
    }
  }
});

// --- Content script messages ---

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type !== "download-link") return;

  (async () => {
    const headers = await collectHeaders(msg.url, msg.pageUrl);

    const resp = await sendWithConnection({
      command: "add_download",
      data: { url: msg.url, headers, referer_url: msg.pageUrl || "" },
    });

    if (resp && resp.success) {
      const filename =
        resp.data?.download?.filename || msg.url.split("/").pop();
      showNotification("Sent to Bolt", filename);
    } else {
      chrome.downloads.download({ url: msg.url });
      showNotification("Bolt unavailable", "Downloading normally");
    }

    sendResponse({ ok: true });
  })();

  return true; // async response
});

// --- Filters ---

function passesFilters(url, totalBytes, settings) {
  // Min file size (only when Chrome provides it)
  if (settings.minFileSize > 0 && totalBytes > 0) {
    if (totalBytes < settings.minFileSize) return false;
  }

  // Domain blocklist
  if (settings.domainBlocklist.length > 0) {
    try {
      const domain = new URL(url).hostname;
      if (settings.domainBlocklist.some((d) => domain.endsWith(d.trim())))
        return false;
    } catch {
      /* invalid URL, let it through */
    }
  }

  // Extension whitelist/blacklist
  const ext = getFileExtension(url);
  if (settings.extensionWhitelist.length > 0) {
    if (!settings.extensionWhitelist.some((e) => e.trim() === ext))
      return false;
  }
  if (settings.extensionBlacklist.length > 0) {
    if (settings.extensionBlacklist.some((e) => e.trim() === ext))
      return false;
  }

  return true;
}

function getFileExtension(url) {
  try {
    const pathname = new URL(url).pathname;
    const last = pathname.split("/").pop();
    const dotIdx = last.lastIndexOf(".");
    if (dotIdx === -1) return "";
    return last.slice(dotIdx).toLowerCase();
  } catch {
    return "";
  }
}

// --- Headers/cookies ---

async function collectHeaders(url, pageUrl) {
  const headers = {};

  // Cookies
  try {
    const cookies = await chrome.cookies.getAll({ url });
    if (cookies.length > 0) {
      headers["Cookie"] = cookies.map((c) => `${c.name}=${c.value}`).join("; ");
    }
  } catch {
    /* cookies unavailable */
  }

  // Referrer
  if (pageUrl) {
    headers["Referer"] = pageUrl;
  }

  // User-Agent for compatibility with servers that check it
  headers["User-Agent"] = navigator.userAgent;

  return headers;
}

// --- Notifications ---

function showNotification(title, message) {
  chrome.notifications.create({
    type: "basic",
    iconUrl: "icons/icon-128.png",
    title,
    message,
  });
}

// --- State query for popup ---

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === "get-state") {
    (async () => {
      // Re-ping if state is stale
      if (connectionState !== "ready") {
        await ensurePort();
      }
      sendResponse({ connectionState });
    })();
    return true;
  }
});
```

- [ ] **Step 4: Verify Go tooling ignores extension files**

Run: `go vet ./...`
Expected: Clean

- [ ] **Step 5: Commit**

```bash
git add -A extensions/chrome/
git commit -m "feat(extension): add Manifest V3 background service worker with native messaging"
```

This stages all changes under `extensions/chrome/`: new files (manifest.json, background.js) and deletions (old background.js, content.js, manifest.json, popup/, welcome/, PRIVACY.md). The icons directory is kept.

---

### Task 6: Chrome extension — content script

**Files:**
- Create: `extensions/chrome/content.js`

- [ ] **Step 1: Create content.js**

Create `extensions/chrome/content.js`:

```javascript
// Content script: intercept clicks on download links when capture is enabled.
// Injected into all pages via manifest.json content_scripts declaration.

const DOWNLOAD_EXTENSIONS = new Set([
  ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tar.xz",
  ".gz", ".bz2", ".xz", ".7z", ".rar",
  ".iso", ".img",
  ".deb", ".rpm", ".AppImage", ".flatpak", ".snap",
  ".exe", ".msi", ".dmg", ".pkg",
  ".pdf",
  ".bin", ".run", ".sh",
]);

function getExtension(url) {
  try {
    const pathname = new URL(url).pathname;
    const filename = pathname.split("/").pop();
    if (!filename) return "";

    // Handle .tar.gz, .tar.bz2, .tar.xz
    for (const ext of [".tar.gz", ".tar.bz2", ".tar.xz"]) {
      if (filename.toLowerCase().endsWith(ext)) return ext;
    }

    const dotIdx = filename.lastIndexOf(".");
    if (dotIdx === -1) return "";
    return filename.slice(dotIdx).toLowerCase();
  } catch {
    return "";
  }
}

function isDownloadLink(url) {
  if (!url || !url.startsWith("http")) return false;
  const ext = getExtension(url);
  return DOWNLOAD_EXTENSIONS.has(ext);
}

document.addEventListener("click", async (e) => {
  // Only intercept left clicks without modifiers
  if (e.button !== 0 || e.ctrlKey || e.shiftKey || e.altKey || e.metaKey) return;

  const link = e.target.closest("a[href]");
  if (!link) return;

  const url = link.href;
  if (!isDownloadLink(url)) return;

  // Check if capture is enabled
  const settings = await chrome.storage.local.get({ captureEnabled: false });
  if (!settings.captureEnabled) return;

  e.preventDefault();
  e.stopPropagation();

  chrome.runtime.sendMessage({
    type: "download-link",
    url,
    pageUrl: window.location.href,
  });
}, true);
```

- [ ] **Step 2: Commit**

```bash
git add extensions/chrome/content.js
git commit -m "feat(extension): add content script for download link interception"
```

---

### Task 7: Chrome extension — popup UI

**Files:**
- Create: `extensions/chrome/popup/popup.html`
- Create: `extensions/chrome/popup/popup.css`
- Create: `extensions/chrome/popup/popup.js`

- [ ] **Step 1: Create popup.html**

Create `extensions/chrome/popup/popup.html`:

```html
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <link rel="stylesheet" href="popup.css">
</head>
<body>
  <div class="container">
    <div class="status">
      <span class="status-dot" id="status-dot"></span>
      <span id="status-text">Checking...</span>
    </div>

    <label class="toggle">
      <input type="checkbox" id="capture-toggle">
      <span>Capture downloads automatically</span>
    </label>

    <details class="filters">
      <summary>Filters</summary>
      <div class="filter-group">
        <label>
          Minimum file size
          <div class="size-input">
            <input type="number" id="min-size" min="0" value="0">
            <select id="size-unit">
              <option value="1024">KB</option>
              <option value="1048576" selected>MB</option>
            </select>
          </div>
        </label>

        <label>
          Extension whitelist
          <input type="text" id="ext-whitelist" placeholder=".zip, .iso, .tar.gz">
        </label>

        <label>
          Extension blacklist
          <input type="text" id="ext-blacklist" placeholder=".exe, .msi">
        </label>

        <label>
          Domain blocklist
          <input type="text" id="domain-blocklist" placeholder="ads.example.com">
        </label>

        <button id="save-filters">Save</button>
      </div>
    </details>
  </div>

  <script src="popup.js"></script>
</body>
</html>
```

- [ ] **Step 2: Create popup.css**

Create `extensions/chrome/popup/popup.css`:

```css
* {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

body {
  font-family: system-ui, -apple-system, sans-serif;
  font-size: 13px;
  width: 300px;
  color: #1a1a1a;
}

.container {
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px;
  background: #f5f5f5;
  border-radius: 6px;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.status-dot.ready { background: #22c55e; }
.status-dot.daemon_unavailable { background: #eab308; }
.status-dot.host_unavailable { background: #ef4444; }

#status-text {
  font-weight: 500;
}

.toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}

.toggle input {
  width: 16px;
  height: 16px;
}

.filters {
  border: 1px solid #e5e5e5;
  border-radius: 6px;
}

.filters summary {
  padding: 8px;
  cursor: pointer;
  font-weight: 500;
}

.filter-group {
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.filter-group label {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12px;
  color: #666;
}

.filter-group input[type="text"],
.filter-group input[type="number"] {
  padding: 4px 6px;
  border: 1px solid #ddd;
  border-radius: 4px;
  font-size: 13px;
}

.size-input {
  display: flex;
  gap: 4px;
}

.size-input input {
  flex: 1;
}

.size-input select {
  padding: 4px;
  border: 1px solid #ddd;
  border-radius: 4px;
}

#save-filters {
  padding: 6px 12px;
  background: #2563eb;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}

#save-filters:hover {
  background: #1d4ed8;
}
```

- [ ] **Step 3: Create popup.js**

Create `extensions/chrome/popup/popup.js`:

```javascript
const statusDot = document.getElementById("status-dot");
const statusText = document.getElementById("status-text");
const captureToggle = document.getElementById("capture-toggle");
const minSize = document.getElementById("min-size");
const sizeUnit = document.getElementById("size-unit");
const extWhitelist = document.getElementById("ext-whitelist");
const extBlacklist = document.getElementById("ext-blacklist");
const domainBlocklist = document.getElementById("domain-blocklist");
const saveBtn = document.getElementById("save-filters");

const STATUS_LABELS = {
  ready: "Connected",
  daemon_unavailable: "Daemon not running",
  host_unavailable: "Host not installed",
};

// --- State ---

async function updateStatus() {
  const resp = await chrome.runtime.sendMessage({ type: "get-state" });
  const state = resp?.connectionState || "host_unavailable";
  statusDot.className = "status-dot " + state;
  statusText.textContent = STATUS_LABELS[state] || "Unknown";
}

// --- Settings ---

async function loadSettings() {
  const s = await chrome.storage.local.get({
    captureEnabled: false,
    minFileSize: 0,
    extensionWhitelist: [],
    extensionBlacklist: [],
    domainBlocklist: [],
  });

  captureToggle.checked = s.captureEnabled;

  // Convert bytes to display value
  if (s.minFileSize >= 1048576 && s.minFileSize % 1048576 === 0) {
    minSize.value = s.minFileSize / 1048576;
    sizeUnit.value = "1048576";
  } else if (s.minFileSize >= 1024) {
    minSize.value = Math.round(s.minFileSize / 1024);
    sizeUnit.value = "1024";
  } else {
    minSize.value = s.minFileSize;
    sizeUnit.value = "1024";
  }

  extWhitelist.value = s.extensionWhitelist.join(", ");
  extBlacklist.value = s.extensionBlacklist.join(", ");
  domainBlocklist.value = s.domainBlocklist.join(", ");
}

function parseList(value) {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

// --- Event listeners ---

captureToggle.addEventListener("change", () => {
  chrome.storage.local.set({ captureEnabled: captureToggle.checked });
});

saveBtn.addEventListener("click", () => {
  const sizeBytes = (parseInt(minSize.value, 10) || 0) * parseInt(sizeUnit.value, 10);
  chrome.storage.local.set({
    minFileSize: sizeBytes,
    extensionWhitelist: parseList(extWhitelist.value),
    extensionBlacklist: parseList(extBlacklist.value),
    domainBlocklist: parseList(domainBlocklist.value),
  });
  saveBtn.textContent = "Saved";
  setTimeout(() => (saveBtn.textContent = "Save"), 1500);
});

// --- Init ---
updateStatus();
loadSettings();
```

- [ ] **Step 4: Commit**

```bash
git add extensions/chrome/popup/
git commit -m "feat(extension): add popup UI with status, capture toggle, and filters"
```

---

### Task 8: Final verification and cleanup

**Files:**
- Verify all

- [ ] **Step 1: Run all Go tests**

Run: `go test ./... -count=1 -timeout 120s`
Expected: All PASS (daemon tests + bolt-host tests)

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: Clean

- [ ] **Step 3: Build both binaries**

Run: `make build && make build-host`
Expected: Both `bolt` and `bolt-host` binaries created

- [ ] **Step 4: Verify build-all**

Run: `make build-all`
Expected: Builds daemon, bolt-host, prints Qt stub

- [ ] **Step 5: Verify clean**

Run: `make clean && ls bolt bolt-host 2>&1`
Expected: Both binaries removed

- [ ] **Step 6: Verify extension files are complete**

Run: `ls extensions/chrome/manifest.json extensions/chrome/background.js extensions/chrome/content.js extensions/chrome/popup/popup.html extensions/chrome/popup/popup.js extensions/chrome/popup/popup.css extensions/chrome/icons/icon-16.png`
Expected: All files exist

- [ ] **Step 7: Verify git status**

Run: `git status`
Expected: Clean working tree (only untracked `docs/` if not committed)

- [ ] **Step 8: Manual extension load test**

Load the unpacked extension in Chrome:
1. Open `chrome://extensions`
2. Enable "Developer mode"
3. Click "Load unpacked" and select `extensions/chrome/`
4. Verify: no manifest errors, extension icon appears, popup opens without console errors

Note: The native messaging manifest (`packaging/com.fhsinchy.bolt.json`) still has `EXTENSION_ID_PLACEHOLDER`. After loading the extension unpacked, Chrome assigns an extension ID. To make native messaging work:
1. Note the extension ID from `chrome://extensions`
2. Generate an RSA key for deterministic ID: `openssl genrsa 2048 | openssl rsa -pubout -outform DER | openssl base64 -A`
3. Add the resulting base64 string as the `"key"` field in `extensions/chrome/manifest.json`
4. Reload the extension and verify the ID is stable
5. Replace `EXTENSION_ID_PLACEHOLDER` in `packaging/com.fhsinchy.bolt.json` with the actual ID
6. Run `make install` to install the native messaging manifest

This is a one-time setup step. The key and extension ID should be committed once determined.

- [ ] **Step 9: Commit extension ID configuration (if resolved)**

```bash
git add extensions/chrome/manifest.json packaging/com.fhsinchy.bolt.json
git commit -m "chore: set deterministic extension ID for native messaging"
```
