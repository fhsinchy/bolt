package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
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

// testEnv holds all the components needed for server tests.
type testEnv struct {
	cfg       *config.Config
	store     *db.Store
	bus       *event.Bus
	eng       *engine.Engine
	queueMgr  *queue.Manager
	srv       *Server
	handler   http.Handler
	fileServer *httptest.Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	tmp := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmp
	cfg.AuthToken = "test-token-0123456789abcdef"

	dbPath := filepath.Join(tmp, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	bus := event.NewBus()

	// Create a test file server that serves deterministic data.
	fileServer := testutil.NewTestServer(1024 * 100) // 100KB
	t.Cleanup(fileServer.Close)

	eng := engine.NewWithClient(store, cfg, bus, fileServer.Client())

	var queueMgr *queue.Manager
	queueMgr = queue.New(store, bus, cfg.MaxConcurrent, func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	}, func(ctx context.Context, id string) error {
		return eng.PauseDownload(ctx, id)
	})

	srv := New(eng, store, cfg, bus, queueMgr)

	// Build the middleware-wrapped handler for use with httptest.
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

	return &testEnv{
		cfg:        cfg,
		store:      store,
		bus:        bus,
		eng:        eng,
		queueMgr:   queueMgr,
		srv:        srv,
		handler:    handler,
		fileServer: fileServer,
	}
}

func (te *testEnv) doRequest(method, path string, body any, token string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	te.handler.ServeHTTP(rr, req)
	return rr
}

func TestAuth_MissingToken(t *testing.T) {
	te := newTestEnv(t)
	rr := te.doRequest("GET", "/api/stats", nil, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuth_WrongToken(t *testing.T) {
	te := newTestEnv(t)
	rr := te.doRequest("GET", "/api/stats", nil, "wrong-token")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuth_CorrectToken(t *testing.T) {
	te := newTestEnv(t)
	rr := te.doRequest("GET", "/api/stats", nil, te.cfg.AuthToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAddDownload(t *testing.T) {
	te := newTestEnv(t)

	body := map[string]string{
		"url": te.fileServer.URL + "/testfile.bin",
	}
	rr := te.doRequest("POST", "/api/downloads", body, te.cfg.AuthToken)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["download"]; !ok {
		t.Fatal("response missing 'download' key")
	}
}

func TestAddDownload_InvalidURL(t *testing.T) {
	te := newTestEnv(t)

	body := map[string]string{
		"url": "not-a-url",
	}
	rr := te.doRequest("POST", "/api/downloads", body, te.cfg.AuthToken)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListDownloads(t *testing.T) {
	te := newTestEnv(t)

	rr := te.doRequest("GET", "/api/downloads", nil, te.cfg.AuthToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if _, ok := resp["downloads"]; !ok {
		t.Fatal("response missing 'downloads' key")
	}
	if _, ok := resp["total"]; !ok {
		t.Fatal("response missing 'total' key")
	}
}

func TestGetDownload_NotFound(t *testing.T) {
	te := newTestEnv(t)

	rr := te.doRequest("GET", "/api/downloads/nonexistent", nil, te.cfg.AuthToken)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetStats(t *testing.T) {
	te := newTestEnv(t)

	rr := te.doRequest("GET", "/api/stats", nil, te.cfg.AuthToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)

	for _, key := range []string{"active_count", "queued_count", "completed_count", "version"} {
		if _, ok := resp[key]; !ok {
			t.Fatalf("response missing %q key", key)
		}
	}
}

func TestProbe(t *testing.T) {
	te := newTestEnv(t)

	body := map[string]string{
		"url": te.fileServer.URL + "/testfile.bin",
	}
	rr := te.doRequest("POST", "/api/probe", body, te.cfg.AuthToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result model.ProbeResult
	json.Unmarshal(rr.Body.Bytes(), &result)
	if result.TotalSize <= 0 {
		t.Fatalf("expected positive total_size, got %d", result.TotalSize)
	}
}

func TestPauseDownload(t *testing.T) {
	te := newTestEnv(t)

	// Add a download first.
	addBody := map[string]string{
		"url": te.fileServer.URL + "/testfile.bin",
	}
	addRR := te.doRequest("POST", "/api/downloads", addBody, te.cfg.AuthToken)
	if addRR.Code != http.StatusCreated {
		t.Fatalf("add: expected 201, got %d: %s", addRR.Code, addRR.Body.String())
	}

	var addResp struct {
		Download model.Download `json:"download"`
	}
	json.Unmarshal(addRR.Body.Bytes(), &addResp)

	// Pause the queued download.
	rr := te.doRequest("POST", fmt.Sprintf("/api/downloads/%s/pause", addResp.Download.ID), nil, te.cfg.AuthToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("pause: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebSocket(t *testing.T) {
	te := newTestEnv(t)

	ts := httptest.NewServer(te.handler)
	t.Cleanup(ts.Close)

	// Connect to WebSocket with auth token as query param.
	wsURL := "ws" + ts.URL[4:] + "/ws?token=" + te.cfg.AuthToken
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Wait for the server-side WebSocket handler to subscribe to the event bus.
	// Without this, the event may be published before the subscription exists.
	for te.bus.SubscriberCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	// Publish an event and verify it arrives over WebSocket.
	te.bus.Publish(event.DownloadAdded{
		DownloadID: "test-id",
		Filename:   "test.bin",
		TotalSize:  1024,
	})

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}

	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal ws msg: %v", err)
	}

	if msg["type"] != "download_added" {
		t.Fatalf("expected type download_added, got %v", msg["type"])
	}
	if msg["download_id"] != "test-id" {
		t.Fatalf("expected download_id test-id, got %v", msg["download_id"])
	}
}

func TestHandleConcurrentRequests(t *testing.T) {
	te := newTestEnv(t)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			rr := te.doRequest("GET", "/api/stats", nil, te.cfg.AuthToken)
			if rr.Code != http.StatusOK {
				errs <- fmt.Errorf("goroutine %d: expected 200, got %d", n, rr.Code)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
