# Test Suite Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add ~44 new tests covering cli, app, server integration, stress scenarios, and edge cases — bringing the suite from 91 to ~135 tests.

**Architecture:** Co-located test files following existing patterns (table-driven, `t.TempDir()`, `testutil.NewTestServer`). Two tiers: default (fast, no build tags) and stress (`//go:build stress`). Makefile gets `test-stress` and `test-cover` targets.

**Tech Stack:** Go stdlib `testing`, `net/http/httptest`, `nhooyr.io/websocket`, `testutil` package (existing).

---

### Task 1: Add Makefile Targets

**Files:**
- Modify: `Makefile`

**Step 1: Add test-stress and test-cover targets**

Add these lines after the existing `test-v` target in the Makefile:

```makefile
test-stress:
	go test -tags=stress ./... -count=1 -timeout 300s

test-cover:
	go test ./... -count=1 -coverprofile=coverage.out -timeout 120s
	go tool cover -func=coverage.out
```

**Step 2: Verify Makefile works**

Run: `make test`
Expected: All existing tests pass (no regression).

**Step 3: Commit**

```bash
git add Makefile
git commit -m "Add test-stress and test-cover Makefile targets"
```

---

### Task 2: CLI Unit Tests — HTTP Helpers and CheckDaemon

**Files:**
- Create: `internal/cli/cli_test.go`

**Step 1: Write the test file with helpers and first tests**

```go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fhsinchy/bolt/internal/model"
)

// newTestClient creates a Client pointing at a test server.
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &Client{
		baseURL: ts.URL,
		token:   "test-token",
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func TestCheckDaemon_Success(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stats" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth header")
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"active_count": 0})
	}))

	if err := c.CheckDaemon(); err != nil {
		t.Fatalf("CheckDaemon: %v", err)
	}
}

func TestCheckDaemon_NotRunning(t *testing.T) {
	c := &Client{
		baseURL: "http://127.0.0.1:1", // nothing listening
		token:   "test-token",
		http:    &http.Client{Timeout: 1 * time.Second},
	}

	err := c.CheckDaemon()
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/cli/ -run TestCheckDaemon -v -count=1`
Expected: 2 tests PASS.

**Step 3: Commit**

```bash
git add internal/cli/cli_test.go
git commit -m "Add CLI client tests: CheckDaemon success and failure"
```

---

### Task 3: CLI Unit Tests — Add, List, Status

**Files:**
- Modify: `internal/cli/cli_test.go`

**Step 1: Add tests for Add, List, and Status methods**

Append to `internal/cli/cli_test.go`:

```go
func TestAdd_Success(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody model.AddRequest

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"download": model.Download{
				ID:       "d_test123",
				Filename: "file.bin",
				TotalSize: 1024,
				SegmentCount: 4,
				Dir:      "/tmp",
			},
		})
	}))

	err := c.Add(context.Background(), AddOptions{
		URL:      "https://example.com/file.bin",
		Segments: 4,
		Dir:      "/tmp",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/downloads" {
		t.Errorf("path = %s, want /api/downloads", gotPath)
	}
	if gotBody.URL != "https://example.com/file.bin" {
		t.Errorf("body URL = %s, want https://example.com/file.bin", gotBody.URL)
	}
}

func TestAdd_ServerError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "probe failed",
			"code":  "INTERNAL_ERROR",
		})
	}))

	err := c.Add(context.Background(), AddOptions{URL: "https://example.com/file.bin"})
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestList_Success(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/downloads" {
			t.Errorf("path = %s, want /api/downloads", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"downloads": []model.Download{{ID: "d_1", Filename: "a.bin", Status: model.StatusActive}},
			"total":     1,
		})
	}))

	err := c.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
}

func TestList_WithStatusFilter(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("status")
		json.NewEncoder(w).Encode(map[string]any{"downloads": []model.Download{}, "total": 0})
	}))

	c.List(context.Background(), ListOptions{Status: "active"})
	if gotQuery != "active" {
		t.Errorf("status query = %q, want 'active'", gotQuery)
	}
}

func TestStatus_Success(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/downloads/d_abc123" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"download": model.Download{ID: "d_abc123", Filename: "file.bin"},
			"segments": []model.Segment{},
		})
	}))

	err := c.Status(context.Background(), "d_abc123")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
}

func TestStatus_NotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found", "code": "NOT_FOUND"})
	}))

	err := c.Status(context.Background(), "d_nonexistent")
	if err == nil {
		t.Fatal("expected error on 404")
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/cli/ -v -count=1`
Expected: All 8 tests PASS.

**Step 3: Commit**

```bash
git add internal/cli/cli_test.go
git commit -m "Add CLI tests: Add, List, Status with success and error cases"
```

---

### Task 4: CLI Unit Tests — Pause, Resume, Cancel, Refresh

**Files:**
- Modify: `internal/cli/cli_test.go`

**Step 1: Add remaining action tests**

Append to `internal/cli/cli_test.go`:

```go
func TestPause_Success(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "paused"})
	}))

	err := c.Pause(context.Background(), "d_test123")
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/downloads/d_test123/pause" {
		t.Errorf("path = %s", gotPath)
	}
}

func TestResume_Success(t *testing.T) {
	var gotPath string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "resumed"})
	}))

	err := c.Resume(context.Background(), "d_test123", false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if gotPath != "/api/downloads/d_test123/resume" {
		t.Errorf("path = %s", gotPath)
	}
}

func TestCancel_WithDeleteFile(t *testing.T) {
	var gotMethod, gotPath, gotDeleteParam string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotDeleteParam = r.URL.Query().Get("delete_file")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}))

	err := c.Cancel(context.Background(), "d_test123", true)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotDeleteParam != "true" {
		t.Errorf("delete_file = %s, want true", gotDeleteParam)
	}
}

func TestRefresh_Success(t *testing.T) {
	var gotBody map[string]string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "refreshed"})
	}))

	err := c.Refresh(context.Background(), "d_test123", "https://new-url.com/file.bin")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if gotBody["url"] != "https://new-url.com/file.bin" {
		t.Errorf("body url = %s", gotBody["url"])
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/cli/ -v -count=1`
Expected: All 12 tests PASS.

**Step 3: Commit**

```bash
git add internal/cli/cli_test.go
git commit -m "Add CLI tests: Pause, Resume, Cancel, Refresh"
```

---

### Task 5: App Unit Tests — Setup and Core Operations

**Files:**
- Create: `internal/app/app_test.go`

**Step 1: Write test file with helper and core tests**

```go
package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/testutil"
)

func setupTestApp(t *testing.T, fileServer ...*testutil.TestServer) *App {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmpDir
	cfg.MaxRetries = 3
	cfg.MinSegmentSize = 1024

	bus := event.NewBus()
	eng := engine.New(store, cfg, bus)

	startFn := func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	}
	queueMgr := queue.New(store, bus, cfg.MaxConcurrent, startFn)

	return New(eng, store, cfg, bus, queueMgr)
}

func TestAddDownload(t *testing.T) {
	ts := testutil.NewTestServer(1024 * 50)
	defer ts.Close()

	a := setupTestApp(t)
	a.engine = engine.NewWithClient(a.store, a.cfg, a.bus, ts.Client())

	dl, err := a.AddDownload(model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}
	if dl.ID == "" {
		t.Error("expected non-empty ID")
	}
	if dl.Filename == "" {
		t.Error("expected non-empty Filename")
	}
	if dl.TotalSize != 1024*50 {
		t.Errorf("TotalSize = %d, want %d", dl.TotalSize, 1024*50)
	}
}

func TestListDownloads_Empty(t *testing.T) {
	a := setupTestApp(t)

	downloads, err := a.ListDownloads("", 0, 0)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if downloads == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(downloads) != 0 {
		t.Errorf("len = %d, want 0", len(downloads))
	}
}

func TestGetDownload_NotFound(t *testing.T) {
	a := setupTestApp(t)

	_, err := a.GetDownload("d_nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent download")
	}
}
```

Note: `setupTestApp` creates a real engine with `engine.New(...)`. For tests that need to actually download, we override `a.engine` with `engine.NewWithClient(...)` passing the test server's client. The `engine` field on `App` is unexported so same-package tests can access it.

**Step 2: Run tests**

Run: `go test ./internal/app/ -v -count=1`
Expected: 3 tests PASS.

**Step 3: Commit**

```bash
git add internal/app/app_test.go
git commit -m "Add app tests: AddDownload, ListDownloads empty, GetDownload not found"
```

---

### Task 6: App Unit Tests — Config and Stats

**Files:**
- Modify: `internal/app/app_test.go`

**Step 1: Add config and stats tests**

Append:

```go
func TestGetConfig(t *testing.T) {
	a := setupTestApp(t)

	sc := a.GetConfig()
	if sc.MaxConcurrent != a.cfg.MaxConcurrent {
		t.Errorf("MaxConcurrent = %d, want %d", sc.MaxConcurrent, a.cfg.MaxConcurrent)
	}
	if sc.DownloadDir != a.cfg.DownloadDir {
		t.Errorf("DownloadDir = %s, want %s", sc.DownloadDir, a.cfg.DownloadDir)
	}
}

func TestUpdateConfig(t *testing.T) {
	a := setupTestApp(t)

	newMax := 5
	err := a.UpdateConfig(ConfigUpdate{
		MaxConcurrent: &newMax,
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if a.cfg.MaxConcurrent != 5 {
		t.Errorf("MaxConcurrent = %d, want 5", a.cfg.MaxConcurrent)
	}
}

func TestUpdateConfig_Invalid(t *testing.T) {
	a := setupTestApp(t)

	badMax := 0
	err := a.UpdateConfig(ConfigUpdate{
		MaxConcurrent: &badMax,
	})
	if err == nil {
		t.Fatal("expected validation error for MaxConcurrent=0")
	}
}

func TestGetStats(t *testing.T) {
	a := setupTestApp(t)

	stats := a.GetStats()
	if stats.Active != 0 || stats.Queued != 0 || stats.Completed != 0 {
		t.Errorf("expected all zeros, got active=%d queued=%d completed=%d",
			stats.Active, stats.Queued, stats.Completed)
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/app/ -v -count=1`
Expected: All 7 tests PASS.

**Step 3: Commit**

```bash
git add internal/app/app_test.go
git commit -m "Add app tests: GetConfig, UpdateConfig, GetStats"
```

---

### Task 7: App Unit Tests — Pause, Resume, Cancel, Bulk Operations

**Files:**
- Modify: `internal/app/app_test.go`

**Step 1: Add lifecycle and bulk operation tests**

Append:

```go
func TestPauseDownload_InvalidID(t *testing.T) {
	a := setupTestApp(t)

	err := a.PauseDownload("d_nonexistent")
	if err == nil {
		t.Fatal("expected error pausing nonexistent download")
	}
}

func TestResumeDownload_InvalidID(t *testing.T) {
	a := setupTestApp(t)

	err := a.ResumeDownload("d_nonexistent")
	if err == nil {
		t.Fatal("expected error resuming nonexistent download")
	}
}

func TestCancelDownload_InvalidID(t *testing.T) {
	a := setupTestApp(t)

	err := a.CancelDownload("d_nonexistent", false)
	if err == nil {
		t.Fatal("expected error cancelling nonexistent download")
	}
}

func TestPauseAll(t *testing.T) {
	ts := testutil.NewTestServer(1024*100, testutil.WithLatency(50*time.Millisecond))
	defer ts.Close()

	a := setupTestApp(t)
	a.engine = engine.NewWithClient(a.store, a.cfg, a.bus, ts.Client())

	// Add a download
	dl, err := a.AddDownload(model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 2,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	// Start it via queue manager
	ctx := context.Background()
	a.engine.StartDownload(ctx, dl.ID)
	time.Sleep(100 * time.Millisecond)

	// PauseAll should not error
	if err := a.PauseAll(); err != nil {
		t.Fatalf("PauseAll: %v", err)
	}
}

func TestClearCompleted(t *testing.T) {
	ts := testutil.NewTestServer(1024 * 10) // small, completes fast
	defer ts.Close()

	a := setupTestApp(t)
	a.engine = engine.NewWithClient(a.store, a.cfg, a.bus, ts.Client())

	ch, subID := a.bus.Subscribe()
	defer a.bus.Unsubscribe(subID)

	dl, err := a.AddDownload(model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := a.engine.StartDownload(context.Background(), dl.ID); err != nil {
		t.Fatal(err)
	}

	// Wait for completion
	timeout := time.After(15 * time.Second)
	for {
		select {
		case evt := <-ch:
			if _, ok := evt.(event.DownloadCompleted); ok {
				goto done
			}
		case <-timeout:
			t.Fatal("timed out waiting for completion")
		}
	}
done:

	// Now clear completed
	if err := a.ClearCompleted(); err != nil {
		t.Fatalf("ClearCompleted: %v", err)
	}

	// Verify download is gone
	downloads, _ := a.ListDownloads("", 0, 0)
	if len(downloads) != 0 {
		t.Errorf("expected 0 downloads after clear, got %d", len(downloads))
	}
}

func TestGetAuthToken(t *testing.T) {
	a := setupTestApp(t)
	token := a.GetAuthToken()
	if token == "" {
		t.Error("expected non-empty auth token")
	}
	if token != a.cfg.AuthToken {
		t.Error("token should match config auth token")
	}
}
```

Note: The `time` import and `event` import will need to be added to the import block.

**Step 2: Run tests**

Run: `go test ./internal/app/ -v -count=1 -timeout 60s`
Expected: All 13 tests PASS.

**Step 3: Commit**

```bash
git add internal/app/app_test.go
git commit -m "Add app tests: Pause/Resume/Cancel invalid, PauseAll, ClearCompleted, GetAuthToken"
```

---

### Task 8: Server Integration Tests — Setup and Basic Lifecycle

**Files:**
- Create: `internal/server/server_integration_test.go`

**Step 1: Write test file with real HTTP server helper and first tests**

The key difference from existing `server_test.go`: this starts a real `net/http` server listening on a random port and makes real HTTP calls.

```go
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
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

type integrationEnv struct {
	baseURL    string
	token      string
	bus        *event.Bus
	fileServer *httptest.Server
}

func startIntegrationServer(t *testing.T) *integrationEnv {
	t.Helper()

	tmp := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmp
	cfg.AuthToken = "integ-test-token-0123456789"

	store, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	bus := event.NewBus()

	fileServer := testutil.NewTestServer(1024 * 100) // 100KB
	t.Cleanup(fileServer.Close)

	eng := engine.NewWithClient(store, cfg, bus, fileServer.Client())

	startFn := func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	}
	queueMgr := queue.New(store, bus, cfg.MaxConcurrent, startFn)

	srv := New(eng, store, cfg, bus, queueMgr)

	// Build middleware-wrapped handler
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

	handler := srv.recovery(srv.logging(srv.cors(srv.auth(mux))))

	// Listen on random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	httpSrv := &http.Server{Handler: handler}
	go httpSrv.Serve(ln)
	t.Cleanup(func() {
		httpSrv.Close()
	})

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go queueMgr.Run(ctx)

	return &integrationEnv{
		baseURL:    baseURL,
		token:      cfg.AuthToken,
		bus:        bus,
		fileServer: fileServer,
	}
}

func (ie *integrationEnv) doJSON(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, ie.baseURL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ie.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP %s %s: %v", method, path, err)
	}
	return resp
}

func TestIntegration_AddAndList(t *testing.T) {
	ie := startIntegrationServer(t)

	// Add a download
	resp := ie.doJSON(t, "POST", "/api/downloads", map[string]string{
		"url": ie.fileServer.URL + "/test.bin",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add: expected 201, got %d", resp.StatusCode)
	}

	var addResp struct {
		Download model.Download `json:"download"`
	}
	json.NewDecoder(resp.Body).Decode(&addResp)

	if addResp.Download.ID == "" {
		t.Fatal("expected non-empty download ID")
	}

	// List downloads
	listResp := ie.doJSON(t, "GET", "/api/downloads", nil)
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", listResp.StatusCode)
	}

	var listResult struct {
		Downloads []model.Download `json:"downloads"`
		Total     int              `json:"total"`
	}
	json.NewDecoder(listResp.Body).Decode(&listResult)

	if listResult.Total < 1 {
		t.Errorf("total = %d, want >= 1", listResult.Total)
	}
}

func TestIntegration_Auth401(t *testing.T) {
	ie := startIntegrationServer(t)

	// Missing token
	req, _ := http.NewRequest("GET", ie.baseURL+"/api/stats", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing token: expected 401, got %d", resp.StatusCode)
	}

	// Wrong token
	req2, _ := http.NewRequest("GET", ie.baseURL+"/api/stats", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", resp2.StatusCode)
	}
}

func TestIntegration_NotFound(t *testing.T) {
	ie := startIntegrationServer(t)

	resp := ie.doJSON(t, "GET", "/api/downloads/d_nonexistent", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
```

Note: This file needs `"net/http/httptest"` imported for the `*httptest.Server` type in `integrationEnv`.

**Step 2: Run tests**

Run: `go test ./internal/server/ -run TestIntegration -v -count=1 -timeout 30s`
Expected: 3 tests PASS.

**Step 3: Commit**

```bash
git add internal/server/server_integration_test.go
git commit -m "Add server integration tests: AddAndList, Auth401, NotFound"
```

---

### Task 9: Server Integration Tests — CORS, Malformed JSON, Probe, WebSocket

**Files:**
- Modify: `internal/server/server_integration_test.go`

**Step 1: Add remaining integration tests**

Append:

```go
func TestIntegration_CORSPreflight(t *testing.T) {
	ie := startIntegrationServer(t)

	req, _ := http.NewRequest("OPTIONS", ie.baseURL+"/api/stats", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("ACAO = %q, want *", got)
	}
}

func TestIntegration_MalformedJSON(t *testing.T) {
	ie := startIntegrationServer(t)

	req, _ := http.NewRequest("POST", ie.baseURL+"/api/downloads", bytes.NewReader([]byte("{bad json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ie.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestIntegration_ProbeEndpoint(t *testing.T) {
	ie := startIntegrationServer(t)

	resp := ie.doJSON(t, "POST", "/api/probe", map[string]string{
		"url": ie.fileServer.URL + "/probe-test.bin",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("probe: expected 200, got %d", resp.StatusCode)
	}

	var result model.ProbeResult
	json.NewDecoder(resp.Body).Decode(&result)
	if result.TotalSize != 1024*100 {
		t.Errorf("TotalSize = %d, want %d", result.TotalSize, 1024*100)
	}
}

func TestIntegration_WebSocketEvents(t *testing.T) {
	ie := startIntegrationServer(t)

	wsURL := fmt.Sprintf("ws%s/ws?token=%s", ie.baseURL[4:], ie.token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Publish an event and verify it arrives
	ie.bus.Publish(event.DownloadAdded{
		DownloadID: "ws-test-id",
		Filename:   "ws-test.bin",
		TotalSize:  2048,
	})

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}

	var msg map[string]any
	json.Unmarshal(data, &msg)

	if msg["type"] != "download_added" {
		t.Errorf("type = %v, want download_added", msg["type"])
	}
	if msg["download_id"] != "ws-test-id" {
		t.Errorf("download_id = %v", msg["download_id"])
	}
}

func TestIntegration_FullLifecycle(t *testing.T) {
	ie := startIntegrationServer(t)

	// Add
	addResp := ie.doJSON(t, "POST", "/api/downloads", map[string]string{
		"url": ie.fileServer.URL + "/lifecycle.bin",
	})
	defer addResp.Body.Close()
	if addResp.StatusCode != http.StatusCreated {
		t.Fatalf("add: expected 201, got %d", addResp.StatusCode)
	}

	var added struct {
		Download model.Download `json:"download"`
	}
	json.NewDecoder(addResp.Body).Decode(&added)
	id := added.Download.ID

	// Pause
	pauseResp := ie.doJSON(t, "POST", fmt.Sprintf("/api/downloads/%s/pause", id), nil)
	defer pauseResp.Body.Close()
	if pauseResp.StatusCode != http.StatusOK {
		t.Errorf("pause: expected 200, got %d", pauseResp.StatusCode)
	}

	// Get — verify paused
	getResp := ie.doJSON(t, "GET", fmt.Sprintf("/api/downloads/%s", id), nil)
	defer getResp.Body.Close()
	var got struct {
		Download model.Download `json:"download"`
	}
	json.NewDecoder(getResp.Body).Decode(&got)
	if got.Download.Status != model.StatusPaused {
		t.Errorf("status = %s, want paused", got.Download.Status)
	}

	// Delete
	delResp := ie.doJSON(t, "DELETE", fmt.Sprintf("/api/downloads/%s", id), nil)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Errorf("delete: expected 200, got %d", delResp.StatusCode)
	}

	// Verify gone
	goneResp := ie.doJSON(t, "GET", fmt.Sprintf("/api/downloads/%s", id), nil)
	defer goneResp.Body.Close()
	if goneResp.StatusCode != http.StatusNotFound {
		t.Errorf("after delete: expected 404, got %d", goneResp.StatusCode)
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/server/ -run TestIntegration -v -count=1 -timeout 30s`
Expected: All 8 integration tests PASS.

**Step 3: Commit**

```bash
git add internal/server/server_integration_test.go
git commit -m "Add server integration tests: CORS, malformed JSON, probe, WebSocket, full lifecycle"
```

---

### Task 10: Edge Cases — DB Tests

**Files:**
- Modify: `internal/db/downloads_test.go`

**Step 1: Add concurrent writes and Unicode filename tests**

Append to `internal/db/downloads_test.go`:

```go
func TestConcurrentWrites(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			dl := newTestDownload(model.NewDownloadID())
			dl.Filename = fmt.Sprintf("concurrent-%d.bin", idx)
			if err := store.InsertDownload(ctx, dl); err != nil {
				errCh <- fmt.Errorf("insert %d: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	// Verify all 20 were inserted
	downloads, err := store.ListDownloads(ctx, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 20 {
		t.Errorf("len = %d, want 20", len(downloads))
	}
}

func TestUnicodeFilenames(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	tests := []string{
		"ファイル.zip",
		"档案.tar.gz",
		"файл_данных.bin",
		"αρχείο.pdf",
		"emoji_🎉_test.bin",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			dl := newTestDownload(model.NewDownloadID())
			dl.Filename = name
			if err := store.InsertDownload(ctx, dl); err != nil {
				t.Fatalf("insert: %v", err)
			}
			got, err := store.GetDownload(ctx, dl.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Filename != name {
				t.Errorf("filename = %q, want %q", got.Filename, name)
			}
		})
	}
}
```

Note: Imports needed — add `"fmt"` and `"sync"` to the import block. `model` is already imported.

**Step 2: Run tests**

Run: `go test ./internal/db/ -run "TestConcurrent|TestUnicode" -v -count=1`
Expected: PASS (WAL mode handles concurrent writes; SQLite supports Unicode natively).

**Step 3: Commit**

```bash
git add internal/db/downloads_test.go
git commit -m "Add db edge case tests: concurrent writes and Unicode filenames"
```

---

### Task 11: Edge Cases — Engine Tests

**Files:**
- Modify: `internal/engine/engine_test.go`

**Step 1: Add engine edge case tests**

Append to `internal/engine/engine_test.go`:

```go
func TestAddDownload_ZeroContentLength(t *testing.T) {
	// Server that returns Content-Length: 0 on HEAD
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	eng, _, _, _ := setupEngine(t)
	eng.client = ts.Client()

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/empty.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}
	// Zero/negative size should fallback to 1 segment
	if dl.SegmentCount != 1 {
		t.Errorf("SegmentCount = %d, want 1 for zero content-length", dl.SegmentCount)
	}
}

func TestAddDownload_RedirectChain(t *testing.T) {
	const fileSize = 1024 * 10
	fileTS := testutil.NewTestServer(fileSize)
	defer fileTS.Close()

	// Redirect server that redirects to the file server
	redirectTS := testutil.NewRedirectServer(fileTS.URL + "/final.bin")
	defer redirectTS.Close()

	eng, _, bus, _ := setupEngine(t)
	eng.client = fileTS.Client()

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      redirectTS.URL + "/redirect.bin",
		Segments: 2,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	// The probe should have followed the redirect
	if dl.TotalSize != fileSize {
		t.Errorf("TotalSize = %d, want %d", dl.TotalSize, fileSize)
	}
}
```

Note: Add `"net/http/httptest"` to the import block.

**Step 2: Run tests**

Run: `go test ./internal/engine/ -run "TestAddDownload_Zero|TestAddDownload_Redirect" -v -count=1`
Expected: PASS.

**Step 3: Commit**

```bash
git add internal/engine/engine_test.go
git commit -m "Add engine edge case tests: zero content-length and redirect chain"
```

---

### Task 12: Edge Cases — Server and Queue Tests

**Files:**
- Modify: `internal/server/server_test.go`
- Modify: `internal/queue/queue_test.go`

**Step 1: Add server concurrent requests test**

Append to `internal/server/server_test.go`:

```go
func TestHandleConcurrentRequests(t *testing.T) {
	te := newTestEnv(t)

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := te.doRequest("GET", "/api/stats", nil, te.cfg.AuthToken)
			if rr.Code != http.StatusOK {
				errors <- fmt.Errorf("expected 200, got %d", rr.Code)
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}
```

Note: Add `"fmt"` and `"sync"` to the import block.

**Step 2: Add queue SetMaxConcurrent mid-flight test**

Append to `internal/queue/queue_test.go`:

```go
func TestMaxConcurrentChanged_MidFlight(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	var mu sync.Mutex
	started := make([]string, 0)

	startFn := func(ctx context.Context, id string) error {
		mu.Lock()
		started = append(started, id)
		mu.Unlock()
		return nil
	}

	mgr := New(store, bus, 2, startFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	// Insert 5 downloads
	for i := 0; i < 5; i++ {
		id := model.NewDownloadID()
		insertQueuedDownload(t, store, id, i)
		mgr.Enqueue(id)
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count1 := len(started)
	mu.Unlock()

	if count1 != 2 {
		t.Fatalf("expected 2 started initially, got %d", count1)
	}

	// Increase max concurrent — should start more
	mgr.SetMaxConcurrent(5)

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count2 := len(started)
	mu.Unlock()

	// With 2 already "started" (counted in active), SetMaxConcurrent(5)
	// allows 3 more to start. But since startFn doesn't actually hold
	// active count, the queue tracks its own activeCount. After the first
	// 2, activeCount=2. SetMaxConcurrent(5) re-evaluates: 5-2=3 more slots.
	if count2 < 4 {
		t.Errorf("expected at least 4 started after increase, got %d", count2)
	}
}
```

**Step 3: Run tests**

Run: `go test ./internal/server/ -run TestHandleConcurrent -v -count=1 && go test ./internal/queue/ -run TestMaxConcurrent -v -count=1`
Expected: Both PASS.

**Step 4: Commit**

```bash
git add internal/server/server_test.go internal/queue/queue_test.go
git commit -m "Add edge case tests: concurrent server requests and queue MaxConcurrent mid-flight"
```

---

### Task 13: Stress Tests

**Files:**
- Create: `internal/engine/stress_test.go`

**Step 1: Write stress test file with build tag**

```go
//go:build stress

package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/testutil"
)

func TestStress_ConcurrentQueuePressure(t *testing.T) {
	const fileSize = 1024 * 50 // 50KB each — fast
	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(10*time.Millisecond))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "stress.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmpDir
	cfg.MaxRetries = 3
	cfg.MinSegmentSize = 1024
	cfg.MaxConcurrent = 3

	bus := event.NewBus()
	eng := NewWithClient(store, cfg, bus, ts.Client())

	// Track max concurrent active via events
	var currentActive, maxActive int64

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	go func() {
		for evt := range ch {
			switch evt.(type) {
			case event.DownloadAdded:
				cur := atomic.AddInt64(&currentActive, 1)
				for {
					max := atomic.LoadInt64(&maxActive)
					if cur <= max || atomic.CompareAndSwapInt64(&maxActive, max, cur) {
						break
					}
				}
			case event.DownloadCompleted, event.DownloadFailed:
				atomic.AddInt64(&currentActive, -1)
			}
		}
	}()

	startFn := func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	}
	queueMgr := queue.New(store, bus, cfg.MaxConcurrent, startFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go queueMgr.Run(ctx)

	// Add 20 downloads
	var ids []string
	for i := 0; i < 20; i++ {
		dl, err := eng.AddDownload(ctx, model.AddRequest{
			URL:      ts.URL + fmt.Sprintf("/file-%d.bin", i),
			Segments: 2,
		})
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		ids = append(ids, dl.ID)
		queueMgr.Enqueue(dl.ID)
	}

	// Wait for all to complete (generous timeout)
	completed := 0
	timeout := time.After(120 * time.Second)
	for completed < 20 {
		select {
		case evt := <-ch:
			if _, ok := evt.(event.DownloadCompleted); ok {
				completed++
				queueMgr.OnDownloadComplete("")
			}
		case <-timeout:
			t.Fatalf("timed out: only %d/20 completed", completed)
		}
	}
}

func TestStress_RapidPauseResume(t *testing.T) {
	const fileSize = 1024 * 200 // 200KB
	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(20*time.Millisecond))
	defer ts.Close()

	eng, store, bus, tmpDir := setupEngine(t)
	eng.client = ts.Client()

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/rapid.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatal(err)
	}

	// Rapid pause/resume 10 times
	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		_ = eng.PauseDownload(ctx, dl.ID)
		time.Sleep(20 * time.Millisecond)
		_ = eng.ResumeDownload(ctx, dl.ID)
	}

	// Wait for completion
	timeout := time.After(120 * time.Second)
	for {
		select {
		case evt := <-ch:
			if _, ok := evt.(event.DownloadCompleted); ok {
				goto verify
			}
		case <-timeout:
			t.Fatal("timed out waiting for completion after rapid pause/resume")
		}
	}

verify:
	// Verify file integrity
	got, err := store.GetDownload(ctx, dl.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, got.Filename))
	if err != nil {
		t.Fatal(err)
	}
	expected := testutil.GenerateData(fileSize)
	if len(data) != len(expected) {
		t.Fatalf("file size = %d, want %d", len(data), len(expected))
	}
	for i := range data {
		if data[i] != expected[i] {
			t.Fatalf("byte mismatch at %d", i)
		}
	}
}

func TestStress_LargeFileIntegrity(t *testing.T) {
	const fileSize = 50 * 1024 * 1024 // 50 MB
	ts := testutil.NewTestServer(fileSize)
	defer ts.Close()

	eng, _, bus, tmpDir := setupEngine(t)
	eng.client = ts.Client()

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/large.bin",
		Segments: 32,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(120 * time.Second)
	for {
		select {
		case evt := <-ch:
			if _, ok := evt.(event.DownloadCompleted); ok {
				goto verify
			}
		case <-timeout:
			t.Fatal("timed out on 50MB download")
		}
	}

verify:
	data, err := os.ReadFile(filepath.Join(tmpDir, dl.Filename))
	if err != nil {
		t.Fatal(err)
	}
	expected := testutil.GenerateData(fileSize)
	if len(data) != len(expected) {
		t.Fatalf("file size = %d, want %d", len(data), len(expected))
	}
	for i := range data {
		if data[i] != expected[i] {
			t.Fatalf("byte mismatch at %d in 50MB file", i)
		}
	}
}
```

Note: Add `"fmt"` to the import block for the `Sprintf` in `ConcurrentQueuePressure`.

**Step 2: Verify stress tests are NOT run by default**

Run: `go test ./internal/engine/ -v -count=1 -timeout 30s`
Expected: Only existing tests run. No stress tests visible.

**Step 3: Run stress tests explicitly**

Run: `go test -tags=stress ./internal/engine/ -run TestStress -v -count=1 -timeout 300s`
Expected: All 3 stress tests PASS (may take 1-2 minutes).

**Step 4: Commit**

```bash
git add internal/engine/stress_test.go
git commit -m "Add stress tests: concurrent queue, rapid pause/resume, 50MB file integrity"
```

---

### Task 14: Run Full Suite and Verify

**Step 1: Run default suite**

Run: `make test`
Expected: All tests pass in < 30 seconds.

**Step 2: Run with race detector**

Run: `make test-race`
Expected: No data races detected.

**Step 3: Run coverage**

Run: `make test-cover`
Expected: Coverage output showing improved percentages for cli, app, server, db, queue packages.

**Step 4: Run stress suite**

Run: `make test-stress`
Expected: All tests pass including stress tests.

**Step 5: Final commit — update CLAUDE.md with test info**

Add to the Commands section of `CLAUDE.md`:

```
make test-stress   # run all tests including stress tests (slower)
make test-cover    # run tests with coverage report
```

```bash
git add CLAUDE.md
git commit -m "Document test-stress and test-cover commands in CLAUDE.md"
```
