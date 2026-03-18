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
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/service"
	"github.com/fhsinchy/bolt/internal/testutil"
)

// testEnv holds all the components needed for server tests.
type testEnv struct {
	cfg        *config.Config
	store      *db.Store
	svc        *service.Service
	srv        *Server
	handler    http.Handler
	fileServer *httptest.Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	tmp := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmp
	cfg.AuthToken = "test-token-0123456789abcdef"
	cfg.Notifications = false

	cfgPath := filepath.Join(tmp, "config.json")

	dbPath := filepath.Join(tmp, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Create a test file server that serves deterministic data.
	fileServer := testutil.NewTestServer(1024 * 100) // 100KB
	t.Cleanup(fileServer.Close)

	// Create service with deferred wiring
	svc := service.New(nil, nil, store, cfg, cfgPath)
	callbacks := svc.EngineCallbacks()

	eng := engine.NewWithClient(store, cfg, callbacks, fileServer.Client())

	queueMgr := queue.New(store, cfg.MaxConcurrent, func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	}, func(ctx context.Context, id string) error {
		return eng.PauseDownload(ctx, id)
	}, svc.OnResumedCallback())

	svc.SetEngine(eng)
	svc.SetQueue(queueMgr)

	srv := New(svc, cfg)
	handler := srv.Handler()

	return &testEnv{
		cfg:        cfg,
		store:      store,
		svc:        svc,
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

	// Give the WebSocket subscription a moment to register.
	time.Sleep(50 * time.Millisecond)

	// Broadcast a message directly through the service's client hub.
	id, ch := te.svc.RegisterClient()
	_ = id
	_ = ch
	te.svc.UnregisterClient(id)

	// Use the service to add a download which triggers OnAdded broadcast
	addBody := map[string]string{
		"url": te.fileServer.URL + "/ws-test.bin",
	}
	addRR := te.doRequest("POST", "/api/downloads", addBody, te.cfg.AuthToken)
	if addRR.Code != http.StatusCreated {
		t.Fatalf("add: expected 201, got %d: %s", addRR.Code, addRR.Body.String())
	}

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
