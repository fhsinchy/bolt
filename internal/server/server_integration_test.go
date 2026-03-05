package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/testutil"
)

// integrationEnv holds everything needed for a real-server integration test.
type integrationEnv struct {
	baseURL    string
	token      string
	bus        *event.Bus
	fileServer *httptest.Server
}

type integrationOpt func(*integrationCfg)

type integrationCfg struct {
	skipQueue  bool
	fileServer *httptest.Server
}

// withoutQueue prevents the queue manager loop from starting.
// Downloads stay in "queued" status, which is useful for lifecycle tests
// that need to control state transitions without races.
func withoutQueue() integrationOpt {
	return func(c *integrationCfg) { c.skipQueue = true }
}

// withFileServer overrides the default file server.
func withFileServer(fs *httptest.Server) integrationOpt {
	return func(c *integrationCfg) { c.fileServer = fs }
}

// startIntegrationServer spins up a real HTTP server on a random port and
// returns an integrationEnv. All resources are cleaned up via t.Cleanup.
func startIntegrationServer(t *testing.T, opts ...integrationOpt) *integrationEnv {
	t.Helper()

	icfg := &integrationCfg{}
	for _, o := range opts {
		o(icfg)
	}

	tmp := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmp
	cfg.AuthToken = "test-token-0123456789abcdef"

	dbPath := filepath.Join(tmp, "integration.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	bus := event.NewBus()

	fileServer := icfg.fileServer
	if fileServer == nil {
		fileServer = testutil.NewTestServer(1024 * 100) // 100KB
		t.Cleanup(fileServer.Close)
	}

	eng := engine.NewWithClient(store, cfg, bus, fileServer.Client())

	queueMgr := queue.New(store, bus, cfg.MaxConcurrent, func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	})

	srv := New(eng, store, cfg, bus, queueMgr)

	// Build the same mux + middleware chain the server uses internally.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/downloads", srv.handleAddDownload)
	mux.HandleFunc("GET /api/downloads", srv.handleListDownloads)
	mux.HandleFunc("GET /api/downloads/{id}", srv.handleGetDownload)
	mux.HandleFunc("DELETE /api/downloads/{id}", srv.handleDeleteDownload)
	mux.HandleFunc("POST /api/downloads/{id}/pause", srv.handlePauseDownload)
	mux.HandleFunc("POST /api/downloads/{id}/resume", srv.handleResumeDownload)
	mux.HandleFunc("POST /api/downloads/{id}/retry", srv.handleRetryDownload)
	mux.HandleFunc("POST /api/downloads/{id}/refresh", srv.handleRefreshURL)
	mux.HandleFunc("GET /api/config", srv.handleGetConfig)
	mux.HandleFunc("PUT /api/config", srv.handleUpdateConfig)
	mux.HandleFunc("GET /api/stats", srv.handleGetStats)
	mux.HandleFunc("POST /api/probe", srv.handleProbe)
	mux.HandleFunc("GET /ws", srv.handleWebSocket)

	handler := srv.recovery(srv.logging(srv.auth(mux)))

	// Listen on a random port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	httpSrv := &http.Server{
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go httpSrv.Serve(ln)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(ctx)
	})

	if !icfg.skipQueue {
		queueCtx, queueCancel := context.WithCancel(context.Background())
		go queueMgr.Run(queueCtx)
		t.Cleanup(queueCancel)
	}

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())

	return &integrationEnv{
		baseURL:    baseURL,
		token:      cfg.AuthToken,
		bus:        bus,
		fileServer: fileServer,
	}
}

// doJSON makes a real HTTP request with JSON body and auth header, returning
// the status code and the response body parsed as a generic JSON map.
func (ie *integrationEnv) doJSON(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := ie.baseURL + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ie.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	var result map[string]any
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &result)
	}

	return resp.StatusCode, result
}

// doRaw makes a real HTTP request with no auth and a raw string body.
// Useful for testing auth failures and malformed input.
func (ie *integrationEnv) doRaw(t *testing.T, method, path, rawBody string, headers map[string]string) (int, map[string]any) {
	t.Helper()

	var reqBody io.Reader
	if rawBody != "" {
		reqBody = strings.NewReader(rawBody)
	}

	url := ie.baseURL + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	var result map[string]any
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &result)
	}

	return resp.StatusCode, result
}

func TestIntegration_AddAndList(t *testing.T) {
	ie := startIntegrationServer(t)

	// Add a download.
	addBody := map[string]string{
		"url": ie.fileServer.URL + "/testfile.bin",
	}
	status, resp := ie.doJSON(t, "POST", "/api/downloads", addBody)
	if status != http.StatusCreated {
		t.Fatalf("POST /api/downloads: expected 201, got %d: %v", status, resp)
	}
	dl, ok := resp["download"].(map[string]any)
	if !ok {
		t.Fatal("response missing 'download' key or wrong type")
	}
	dlID, ok := dl["id"].(string)
	if !ok || dlID == "" {
		t.Fatal("download missing 'id'")
	}

	// List downloads and verify our new download is present.
	status, resp = ie.doJSON(t, "GET", "/api/downloads", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/downloads: expected 200, got %d", status)
	}
	downloads, ok := resp["downloads"].([]any)
	if !ok {
		t.Fatal("response missing 'downloads' key")
	}
	if len(downloads) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloads))
	}
	total, ok := resp["total"].(float64)
	if !ok || int(total) != 1 {
		t.Fatalf("expected total=1, got %v", resp["total"])
	}
}

func TestIntegration_Auth401(t *testing.T) {
	ie := startIntegrationServer(t)

	// Missing token.
	status, _ := ie.doRaw(t, "GET", "/api/stats", "", map[string]string{
		"Content-Type": "application/json",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("missing token: expected 401, got %d", status)
	}

	// Wrong token.
	status, _ = ie.doRaw(t, "GET", "/api/stats", "", map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer totally-wrong-token-here",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong token: expected 401, got %d", status)
	}
}

func TestIntegration_NotFound(t *testing.T) {
	ie := startIntegrationServer(t)

	status, resp := ie.doJSON(t, "GET", "/api/downloads/d_nonexistent", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %v", status, resp)
	}
	if code, ok := resp["code"].(string); !ok || code != "NOT_FOUND" {
		t.Fatalf("expected code NOT_FOUND, got %v", resp["code"])
	}
}

func TestIntegration_MalformedJSON(t *testing.T) {
	ie := startIntegrationServer(t)

	status, resp := ie.doRaw(t, "POST", "/api/downloads", "{bad json", map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + ie.token,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, resp)
	}
	if code, ok := resp["code"].(string); !ok || code != "VALIDATION_ERROR" {
		t.Fatalf("expected code VALIDATION_ERROR, got %v", resp["code"])
	}
}

func TestIntegration_ProbeEndpoint(t *testing.T) {
	ie := startIntegrationServer(t)

	probeBody := map[string]string{
		"url": ie.fileServer.URL + "/testfile.bin",
	}
	status, resp := ie.doJSON(t, "POST", "/api/probe", probeBody)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, resp)
	}

	totalSize, ok := resp["total_size"].(float64)
	if !ok || totalSize <= 0 {
		t.Fatalf("expected positive total_size, got %v", resp["total_size"])
	}
	if int64(totalSize) != 1024*100 {
		t.Fatalf("expected total_size 102400, got %d", int64(totalSize))
	}
	acceptsRanges, ok := resp["accepts_ranges"].(bool)
	if !ok || !acceptsRanges {
		t.Fatalf("expected accepts_ranges true, got %v", resp["accepts_ranges"])
	}
}

func TestIntegration_WebSocketEvents(t *testing.T) {
	ie := startIntegrationServer(t)

	// Connect via WebSocket with token as query param.
	wsURL := "ws" + ie.baseURL[4:] + "/ws?token=" + ie.token
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Give the WebSocket subscription a moment to register.
	time.Sleep(50 * time.Millisecond)

	// Publish an event on the bus.
	ie.bus.Publish(event.DownloadAdded{
		DownloadID: "test-ws-id",
		Filename:   "test-ws.bin",
		TotalSize:  2048,
	})

	// Read the event from the WebSocket.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}

	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal ws message: %v", err)
	}

	if msg["type"] != "download_added" {
		t.Fatalf("expected type download_added, got %v", msg["type"])
	}
	if msg["download_id"] != "test-ws-id" {
		t.Fatalf("expected download_id test-ws-id, got %v", msg["download_id"])
	}
	if msg["filename"] != "test-ws.bin" {
		t.Fatalf("expected filename test-ws.bin, got %v", msg["filename"])
	}
	if totalSize, ok := msg["total_size"].(float64); !ok || int64(totalSize) != 2048 {
		t.Fatalf("expected total_size 2048, got %v", msg["total_size"])
	}
}

func TestIntegration_FullLifecycle(t *testing.T) {
	// Use withoutQueue so downloads stay in "queued" status and we can
	// control state transitions without races from the queue loop.
	ie := startIntegrationServer(t, withoutQueue())

	// Step 1: Add a download (stays queued because queue is not running).
	addBody := map[string]string{
		"url": ie.fileServer.URL + "/lifecycle-test.bin",
	}
	status, resp := ie.doJSON(t, "POST", "/api/downloads", addBody)
	if status != http.StatusCreated {
		t.Fatalf("add: expected 201, got %d: %v", status, resp)
	}
	dl := resp["download"].(map[string]any)
	dlID := dl["id"].(string)

	// Step 2: Verify the download is queued.
	status, resp = ie.doJSON(t, "GET", fmt.Sprintf("/api/downloads/%s", dlID), nil)
	if status != http.StatusOK {
		t.Fatalf("get after add: expected 200, got %d: %v", status, resp)
	}
	dlData := resp["download"].(map[string]any)
	if dlData["status"] != string(model.StatusQueued) {
		t.Fatalf("expected status queued, got %v", dlData["status"])
	}

	// Step 3: Pause the queued download.
	status, resp = ie.doJSON(t, "POST", fmt.Sprintf("/api/downloads/%s/pause", dlID), nil)
	if status != http.StatusOK {
		t.Fatalf("pause: expected 200, got %d: %v", status, resp)
	}

	// Step 4: Verify it is paused.
	status, resp = ie.doJSON(t, "GET", fmt.Sprintf("/api/downloads/%s", dlID), nil)
	if status != http.StatusOK {
		t.Fatalf("get after pause: expected 200, got %d: %v", status, resp)
	}
	dlAfterPause := resp["download"].(map[string]any)
	if dlAfterPause["status"] != string(model.StatusPaused) {
		t.Fatalf("expected status paused, got %v", dlAfterPause["status"])
	}

	// Step 5: Delete the download.
	status, resp = ie.doJSON(t, "DELETE", fmt.Sprintf("/api/downloads/%s?delete_file=true", dlID), nil)
	if status != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %v", status, resp)
	}

	// Step 6: Verify the download is gone (404).
	status, resp = ie.doJSON(t, "GET", fmt.Sprintf("/api/downloads/%s", dlID), nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after delete: expected 404, got %d: %v", status, resp)
	}
}
