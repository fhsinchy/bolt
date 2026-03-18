# Bolt — Download Manager

Fast, segmented download manager daemon for **Linux**.

## Project Info

- **Module:** `github.com/fhsinchy/bolt`
- **Go version:** 1.23+
- **Author:** github.com/fhsinchy
- **SQLite driver:** `modernc.org/sqlite` (pure Go, no CGO)
- **ULID library:** `github.com/oklog/ulid/v2`
- **WebSocket:** `nhooyr.io/websocket`
- **Test framework:** stdlib `testing` + `net/http/httptest` (no external test deps)
- **Build:** `CGO_ENABLED=0` — fully static binary, no CGO required

## Architecture

Bolt runs as a standalone daemon process. No GUI, no Wails, no CGO.

The daemon owns all download state and exposes a local API over a **Unix socket** at `$XDG_RUNTIME_DIR/bolt/bolt.sock`. Filesystem permissions are the trust boundary — no auth tokens are needed.

### Package Layout

```
cmd/bolt/
  main.go                  Entry point (daemon / version / help)
internal/
  daemon/                  Daemon lifecycle (startup, shutdown, socket, sdnotify)
    daemon.go              Daemon struct, New(), Run(), shutdown()
    socket.go              Unix socket creation, instance detection
    sdnotify.go            Pure Go systemd notify (no CGO)
  service/                 Coordination layer (engine + queue + WebSocket fan-out)
    service.go             Service struct, all download/config operations, EngineCallbacks()
    clienthub.go           WebSocket client hub (register, unregister, broadcast)
  engine/                  Download engine (core business logic)
    engine.go              Engine struct, lifecycle orchestration
    callbacks.go           Callbacks struct (replaces event bus)
    segment.go             Per-segment goroutine with retry
    progress.go            Progress aggregator
    probe.go               HEAD/GET probing
    filename.go            Filename detection + dedup
    httpclient.go          HTTP client factory
    refresh.go             URL refresh
    checksum.go            File checksum verification
  queue/                   FIFO queue with concurrency limit
  server/                  HTTP server (REST API + WebSocket)
    server.go              Server struct, Handler() returns http.Handler
    handlers.go            REST endpoint handlers
    websocket.go           WebSocket handler (reads from ClientHub)
    middleware.go           recovery, logging
  config/                  config.json management
  db/                      SQLite data access layer
  model/                   Shared types, ID generation
  notify/                  Desktop notifications (notify-send)
  testutil/                Test helpers (httptest server)
bolt-qt/                   C++ Qt6 GUI (Phase 2 — not yet buildable)
  CMakeLists.txt           Qt6 project definition
  src/                     Qt source files
extensions/
  chrome/                  Chrome browser extension (Phase 2 — native messaging rewrite)
images/                    Source icons
packaging/
  bolt.service             Systemd user unit (Type=notify, hardened)
  bolt.desktop             Desktop entry
docs/                      PRD, TRD, specs, plans
```

### Key Design: Callbacks Replace Event Bus

The old `internal/event/` pub/sub bus is gone. The engine now takes a `*Callbacks` struct with optional function fields (`OnProgress`, `OnCompleted`, `OnFailed`, `OnPaused`, `OnResumed`, `OnAdded`, `OnRemoved`). The service layer provides these callbacks via `EngineCallbacks()`, wiring:
- Queue completion (`queue.OnDownloadComplete`) on completed/failed/paused
- WebSocket broadcast to all connected clients
- Desktop notifications on completed/failed

### Key Design: Service Layer

`internal/service/Service` is the coordination layer between engine, queue, store, and WebSocket clients. All HTTP handlers call service methods instead of directly calling engine/queue/store. The service owns the `ClientHub` for WebSocket fan-out.

### Key Design: Daemon Lifecycle

`internal/daemon/Daemon` owns the full lifecycle:
1. Load config → Open DB → Create service (get callbacks) → Create engine → Create queue → Wire service
2. Instance detection via Unix socket probe
3. Crash recovery (re-queue stale active downloads)
4. Serve HTTP on Unix socket
5. `sd_notify(READY=1)` for systemd
6. Block until SIGTERM/SIGINT
7. Graceful shutdown: stop accepting → pause active downloads → persist progress → close DB → remove socket

### Key Design: No CGO

The binary is built with `CGO_ENABLED=0`. The SQLite driver (`modernc.org/sqlite`) is pure Go. No Wails, no WebKit, no GTK bindings.

## Commands

```
make build       # CGO_ENABLED=0 go build → ./bolt
make test        # run all tests
make test-race   # run all tests with race detector
make test-v      # run all tests verbose
make test-stress # run all tests including stress tests (slower, ~2 min)
make test-cover  # run tests with coverage report
make build-qt    # build Qt GUI (not yet buildable)
make build-all   # build all components
make test-all    # test all components
make install     # build + install binary + systemd unit + .desktop + icon
make uninstall   # stop + disable + remove binary + unit + .desktop + icon
make clean       # remove binaries, build dirs, clear test cache
```

## Config

Located at `~/.config/bolt/config.json`.

| Field | Type | Default | Description |
|---|---|---|---|
| `download_dir` | string | `~/Downloads` | Default download directory |
| `max_concurrent` | int | 3 | Max concurrent downloads (1-10) |
| `default_segments` | int | 16 | Default segment count (1-32) |
| `global_speed_limit` | int64 | 0 | Bytes/sec, 0 = unlimited |
| `notifications` | bool | true | Desktop notifications |
| `max_retries` | int | 10 | Max retries per segment (0-100) |
| `min_segment_size` | int64 | 1048576 | Min segment size in bytes (≥64KB) |

## API

All endpoints are served over Unix socket only. No authentication is required — filesystem permissions on the socket are the trust boundary.

| Method | Path | Description |
|---|---|---|
| POST | `/api/downloads` | Add download |
| GET | `/api/downloads` | List downloads |
| GET | `/api/downloads/{id}` | Get download detail |
| DELETE | `/api/downloads/{id}` | Delete download |
| POST | `/api/downloads/{id}/pause` | Pause download |
| POST | `/api/downloads/{id}/resume` | Resume download |
| POST | `/api/downloads/{id}/retry` | Retry failed download |
| POST | `/api/downloads/{id}/refresh` | Refresh URL |
| POST | `/api/downloads/{id}/set-refresh` | Set refresh status |
| POST | `/api/downloads/{id}/checksum` | Update checksum |
| PUT | `/api/downloads/reorder` | Reorder queue |
| GET | `/api/config` | Get config |
| PUT | `/api/config` | Update config |
| GET | `/api/stats` | Get stats |
| POST | `/api/probe` | Probe URL |
| GET | `/ws` | WebSocket for real-time events |
