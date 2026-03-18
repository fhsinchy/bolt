package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"path/filepath"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/fhsinchy/bolt/internal/testutil"
)

func TestDaemon_StartAndShutdown(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")

	// Create a config that points to our temp dir
	cfgJSON := fmt.Sprintf(`{
		"download_dir": %q,
		"notifications": false
	}`, tmp)
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Override XDG_RUNTIME_DIR for test isolation
	socketDir := filepath.Join(tmp, "run")
	t.Setenv("XDG_RUNTIME_DIR", socketDir)

	// Override data directory by creating it in temp
	dataDir := filepath.Join(tmp, ".local", "share", "bolt")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	d, err := New(cfgPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for daemon to be ready
	time.Sleep(500 * time.Millisecond)

	// Test: connect via Unix socket
	sockAddr := socketPath()
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sockAddr)
			},
		},
		Timeout: 5 * time.Second,
	}

	req, _ := http.NewRequest("GET", "http://localhost/api/stats", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/stats via unix socket: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var stats map[string]any
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	if _, ok := stats["version"]; !ok {
		t.Fatal("stats missing 'version' key")
	}

	// Shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("daemon.Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for daemon shutdown")
	}

	// Verify socket was cleaned up
	if _, err := os.Stat(sockAddr); !os.IsNotExist(err) {
		t.Error("socket file should have been removed after shutdown")
	}
}

func TestDaemon_InstanceDetection(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")

	cfgJSON := fmt.Sprintf(`{
		"download_dir": %q,
		"notifications": false
	}`, tmp)
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	socketDir := filepath.Join(tmp, "run")
	t.Setenv("XDG_RUNTIME_DIR", socketDir)

	dataDir := filepath.Join(tmp, ".local", "share", "bolt")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	d1, err := New(cfgPath)
	if err != nil {
		t.Fatalf("New d1: %v", err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d1.Run(ctx1)
	}()

	// Wait for first daemon to start
	time.Sleep(500 * time.Millisecond)

	// Second daemon should fail with instance detection
	d2, err := New(cfgPath)
	if err != nil {
		t.Fatalf("New d2: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	err = d2.Run(ctx2)
	if err == nil {
		t.Fatal("expected error from second daemon (instance detection)")
	}

	// Shutdown first daemon
	cancel1()
	select {
	case <-errCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}

func TestDaemon_DownloadViaAPI(t *testing.T) {
	tmp := t.TempDir()

	// Start a file server
	const fileSize = 1024 * 50 // 50KB
	fileServer := testutil.NewTestServer(fileSize)
	defer fileServer.Close()

	cfgPath := filepath.Join(tmp, "config.json")
	cfgJSON := fmt.Sprintf(`{
		"download_dir": %q,
		"notifications": false
	}`, tmp)
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	socketDir := filepath.Join(tmp, "run")
	t.Setenv("XDG_RUNTIME_DIR", socketDir)

	dataDir := filepath.Join(tmp, ".local", "share", "bolt")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	d, err := New(cfgPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for daemon to be ready
	time.Sleep(500 * time.Millisecond)

	sockAddr := socketPath()
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sockAddr)
			},
		},
		Timeout: 30 * time.Second,
	}

	// Connect WebSocket for progress
	wsDialer := websocket.DialOptions{
		HTTPClient: httpClient,
	}
	wsCtx, wsCancel := context.WithTimeout(ctx, 30*time.Second)
	defer wsCancel()

	wsConn, _, err := websocket.Dial(wsCtx, "ws://localhost/ws", &wsDialer)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Add download
	addBody := fmt.Sprintf(`{"url": "%s/testfile.bin", "segments": 4}`, fileServer.URL)
	req, _ := http.NewRequest("POST", "http://localhost/api/downloads", strings.NewReader(addBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/downloads: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}

	// Wait for completion via WebSocket
	timeout := time.After(30 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for download completion")
		default:
		}

		_, data, err := wsConn.Read(wsCtx)
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}

		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if msg["type"] == "completed" {
			break
		}
	}

	// Shutdown
	cancel()
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}
}

