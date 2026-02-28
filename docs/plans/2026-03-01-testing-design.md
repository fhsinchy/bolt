# Test Suite Design for Bolt Download Manager

**Date:** 2026-03-01
**Status:** Approved

## Goals

- Fast feedback during active development (`go test ./...` < 30s)
- Cover the untested cli and app packages
- Add real HTTP server integration tests for the server package
- Add light stress tests behind a build tag for CI/pre-release
- Fill edge case gaps in existing packages

## Test Tiers

### Default Tier (no build tags)

Runs with `go test ./...`. Includes all unit tests and fast integration tests. Target: < 30 seconds.

### Stress Tier (`//go:build stress`)

Runs with `go test -tags=stress ./...`. Adds concurrent, large-file, and rapid-lifecycle tests. Target: 1-2 minutes. Guarded by build tag so they never slow down the default suite.

### Makefile Targets

```makefile
test-stress:  go test -tags=stress ./... -count=1 -timeout 300s
test-cover:   go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out
```

Existing targets (`test`, `test-race`, `test-v`) remain unchanged.

## New Test Files

### 1. CLI Unit Tests — `internal/cli/cli_test.go`

**Package:** `package cli` (same-package access to unexported fields)

**Strategy:** Each test starts an `httptest.Server` with canned JSON responses, creates a `Client` with `baseURL`/`token` pointing at it, calls the method, and verifies the HTTP request and return value.

**Tests (~10):**

| Test | What it verifies |
|---|---|
| TestAdd_Success | POST /api/downloads → 201, parses response, returns nil |
| TestAdd_ServerError | POST /api/downloads → 500, returns parsed error |
| TestList_Success | GET /api/downloads → 200, parses download list |
| TestList_WithStatusFilter | GET /api/downloads?status=active, verifies query param |
| TestList_Empty | Empty list returns nil error, prints "No downloads found" |
| TestStatus_Success | GET /api/downloads/{id} → 200, parses download + segments |
| TestStatus_NotFound | GET /api/downloads/{id} → 404, returns error |
| TestPause_Success | POST /api/downloads/{id}/pause → 200 |
| TestResume_Success | POST /api/downloads/{id}/resume → 200 |
| TestCancel_WithDeleteFile | DELETE /api/downloads/{id}?delete_file=true |
| TestRefresh_Success | POST /api/downloads/{id}/refresh with body |
| TestCheckDaemon_NotRunning | Connection refused returns descriptive error |

**Skipped:** `watchProgress` (WebSocket terminal rendering — tested indirectly via server WebSocket tests).

**Output capture:** Methods that print to stdout will be tested by verifying HTTP request correctness and return values. Terminal output formatting is a presentation concern.

### 2. App Unit Tests — `internal/app/app_test.go`

**Package:** `package app`

**Strategy:** Create real engine, store, bus, and queue manager (same pattern as engine_test.go). Construct `App` via `app.New(...)`. Test IPC methods directly — no Wails runtime needed.

**Test helper:**

```go
func setupTestApp(t *testing.T) *App {
    t.Helper()
    tmpDir := t.TempDir()
    store, _ := db.Open(filepath.Join(tmpDir, "test.db"))
    t.Cleanup(func() { store.Close() })
    cfg := config.DefaultConfig()
    cfg.DownloadDir = tmpDir
    bus := event.NewBus()
    eng := engine.New(store, cfg, bus)
    queueMgr := queue.New(eng, store, bus, cfg.MaxConcurrent)
    return New(eng, store, cfg, bus, queueMgr)
}
```

**Tests (~12):**

| Test | What it verifies |
|---|---|
| TestAddDownload | Creates download, returns model, enqueues |
| TestListDownloads | Returns filtered results |
| TestListDownloads_Empty | Returns `[]` not nil |
| TestGetDownload | Found and not-found cases |
| TestPauseDownload | Delegates to engine, error on invalid ID |
| TestResumeDownload | Delegates to engine, error on invalid ID |
| TestCancelDownload | Removes download, optionally deletes file |
| TestGetConfig | Returns SafeConfig without auth token |
| TestUpdateConfig | Partial update, validates, saves, updates queue |
| TestUpdateConfig_Invalid | Validation errors propagated |
| TestGetStats | Counts by status |
| TestPauseAll_ResumeAll | Bulk operations on matching downloads |
| TestClearCompleted | Removes only completed downloads |

**Skipped:** `OnStartup` (requires Wails context for EventsEmit), `SelectDirectory` (native dialog), `OpenFile`/`OpenFolder` (exec.Command to OS).

### 3. Server Integration Tests — `internal/server/server_integration_test.go`

**Package:** `package server`

**Strategy:** Start a real HTTP server on `localhost:0` (random port). Make real HTTP requests through the full middleware stack. Tests validate the complete request lifecycle including TCP, middleware ordering, auth, and WebSocket upgrade.

**Test helper:**

```go
func startTestServer(t *testing.T) (baseURL, token string) {
    t.Helper()
    tmpDir := t.TempDir()
    store, _ := db.Open(filepath.Join(tmpDir, "test.db"))
    cfg := config.DefaultConfig()
    cfg.DownloadDir = tmpDir
    cfg.ServerPort = 0  // random port
    bus := event.NewBus()
    eng := engine.New(store, cfg, bus)
    queueMgr := queue.New(eng, store, bus, cfg.MaxConcurrent)
    srv := New(eng, store, cfg, bus, queueMgr)
    // Start on random port, extract address
    // Register t.Cleanup for shutdown
    return baseURL, cfg.AuthToken
}
```

**Tests (~8):**

| Test | What it verifies |
|---|---|
| TestIntegration_AddAndList | POST create → GET list shows it |
| TestIntegration_FullLifecycle | Add → Pause → Resume → Cancel |
| TestIntegration_Auth401 | Missing/wrong token → 401 through real HTTP |
| TestIntegration_CORSPreflight | OPTIONS request returns correct CORS headers |
| TestIntegration_WebSocketEvents | Connect WS, add download, receive progress events |
| TestIntegration_NotFound | GET /api/downloads/{bad-id} → 404 |
| TestIntegration_MalformedJSON | POST with bad body → 400 |
| TestIntegration_ProbeEndpoint | POST /api/probe with real testutil server URL |

### 4. Stress Tests — `internal/engine/stress_test.go`

**Build tag:** `//go:build stress`

**Tests (~3):**

| Test | What it verifies |
|---|---|
| TestStress_ConcurrentQueuePressure | Add 20 downloads with MaxConcurrent=3. Monitor via event bus that active count never exceeds 3. All eventually complete. |
| TestStress_RapidPauseResume | Start download with latency. Pause/resume 10 times rapidly. Download completes with correct file contents. |
| TestStress_LargeFileIntegrity | 50MB file, 32 segments. Byte-for-byte verification against deterministic testutil data. |

All use `testutil.NewTestServer` with appropriate options (latency, size). Timeouts: 60-120s per test.

### 5. Edge Case Additions to Existing Files

**engine_test.go (~3 new tests):**
- `TestAddDownload_ZeroContentLength` — server returns Content-Length: 0
- `TestAddDownload_NoRangeSupport` — falls back to single segment
- `TestAddDownload_RedirectChain` — follows redirects, downloads from final URL

**server_test.go (~2 new tests):**
- `TestHandleMalformedJSON` — invalid JSON body returns 400
- `TestHandleConcurrentRequests` — 10 goroutines hitting the same endpoint

**db_test.go (~2 new tests):**
- `TestConcurrentWrites` — multiple goroutines inserting simultaneously (WAL mode)
- `TestUnicodeFilenames` — non-ASCII filenames stored and retrieved correctly

**queue_test.go (~1 new test):**
- `TestMaxConcurrentChanged_MidFlight` — change MaxConcurrent while downloads are active

## Packages NOT Tested

| Package | Reason |
|---|---|
| `tray` | Pure OS interaction (energye/systray). No meaningful mock possible. Manual testing only. |
| `cmd/bolt` | Entry points with OS signals, Wails lifecycle. Tested via manual runs and integration tests. |
| `testutil` | Test helper package. Not business logic. |

## Test Count Summary

| Area | Existing | New | Total |
|---|---|---|---|
| model | 6 | 0 | 6 |
| config | 9 | 0 | 9 |
| db | 29 | 2 | 31 |
| event | 6 | 0 | 6 |
| engine | 22 | 3 (+3 stress) | 28 |
| queue | 3 | 1 | 4 |
| pid | 4 | 0 | 4 |
| server | 12 | 10 | 22 |
| cli | 0 | 12 | 12 |
| app | 0 | 13 | 13 |
| **Total** | **91** | **~44** | **~135** |

## Conventions

All new tests follow established project patterns:
- `t.TempDir()` for isolation
- `t.Helper()` on helper functions
- `t.Cleanup()` for teardown
- Table-driven tests where appropriate
- `testutil.NewTestServer` for mock downloads
- Timeout-guarded event waits (`select` with `time.After`)
- No external test dependencies (stdlib `testing` + `net/http/httptest`)
