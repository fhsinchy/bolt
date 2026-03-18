# Bolt Rewrite — Technical Requirements Document

## 1. System Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     bolt daemon                         │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │  Engine   │  │  Queue   │  │  Notify  │              │
│  │          │  │ Manager  │  │          │              │
│  │ - probe   │  │ - FIFO   │  │ - send   │              │
│  │ - segment │  │ - slots  │  │          │              │
│  │ - progress│  │ - order  │  │          │              │
│  │ - checksum│  │          │  │          │              │
│  └─────┬─────┘  └─────┬────┘  └──────────┘              │
│        │              │                                  │
│  ┌─────┴──────────────┴─────┐                           │
│  │        Service Layer      │                           │
│  │  (direct method calls)    │                           │
│  └─────────────┬─────────────┘                           │
│                │                                         │
│  ┌─────────────┴─────────────┐                           │
│  │        SQLite (WAL)       │                           │
│  │   ~/.local/share/bolt/    │                           │
│  └───────────────────────────┘                           │
│                                                         │
│  ┌───────────────────────────┐                           │
│  │   Unix Socket Listener    │                           │
│  │  $XDG_RUNTIME_DIR/bolt/   │                           │
│  │  REST API + WebSocket     │                           │
│  └─────────┬─────────────────┘                           │
└────────────┼─────────────────────────────────────────────┘
             │
    ┌────────┴────────┐
    │   TUI / GTK /   │
    │   Qt clients    │
    │  (Unix socket)  │
    └─────────────────┘

    ┌─────────────────┐         ┌─────────────────┐
    │ Chrome Extension │◄──────►│   bolt-host     │───► Unix socket
    │ (native message) │ stdin/ │ (bridge process) │
    └─────────────────┘ stdout  └─────────────────┘
```

### Control Flow vs. Observation

**Control flow** (queue scheduling, download lifecycle) uses direct method calls within the daemon process. The service layer calls engine methods and queue methods synchronously. No pub/sub, no channels in the critical path. A missed message cannot stall a download or corrupt queue state.

**Client observation** initially uses a WebSocket stream over the daemon API on the Unix socket. Clients subscribe when connected and miss events when disconnected; this is by design. Clients can always recover full state via REST polling. The daemon never blocks on event delivery to clients.

If WebSocket over Unix socket proves unnecessarily awkward for the TUI, it may be replaced with a simpler local streaming transport later without changing the surrounding daemon architecture.

## 2. Module and Dependencies

```
module github.com/fhsinchy/bolt
go 1.25
```

### Runtime dependencies

| Package | Purpose | Notes |
|---------|---------|-------|
| `modernc.org/sqlite` | SQLite driver | Pure Go, no CGO |
| `github.com/oklog/ulid/v2` | ID generation | Monotonic, sortable ULIDs |
| `golang.org/x/time/rate` | Speed limiting | Token bucket rate limiter |
| `golang.org/x/net/publicsuffix` | Cookie jar | For HTTP client cookie handling |
| `nhooyr.io/websocket` | WebSocket | Event streaming to clients |

### Removed dependencies (from current `master`)

| Package | Reason |
|---------|--------|
| `github.com/wailsapp/wails/v2` | GUI framework, replaced by daemon |
| `github.com/energye/systray` | System tray, GUI-specific |

### Build requirements

- Go 1.25+
- No CGO (`CGO_ENABLED=0`)
- No system libraries required for the daemon binary
- Single static binary output

## 3. Directory Structure

```
cmd/bolt/
  main.go                  Daemon entry point, signal handling, instance detection
cmd/bolt-host/
  main.go                  Chrome native messaging bridge (stdin/stdout ↔ Unix socket)
internal/
  daemon/
    daemon.go              Daemon lifecycle (Start, Shutdown, signal handling)
    socket.go              Unix socket listener setup and cleanup
  service/
    service.go             Application service layer (coordinates engine, queue, db)
  engine/
    engine.go              Download orchestration (add, start, pause, resume, cancel)
    segment.go             Per-segment HTTP worker with retry
    progress.go            Progress aggregation (speed, ETA, batched persistence)
    probe.go               URL probing (HEAD with GET fallback)
    filename.go            Filename detection and deduplication
    checksum.go            Post-download verification
    httpclient.go          HTTP client factory
    refresh.go             URL refresh with size validation
  queue/
    queue.go               FIFO queue manager with concurrency enforcement
  db/
    db.go                  SQLite setup, pragmas, migrations
    downloads.go           Download CRUD operations
    segments.go            Segment CRUD operations
  model/
    model.go               Core types (Download, Segment, ProbeResult, etc.)
    id.go                  ULID generation
    format.go              Human-readable formatting
    errors.go              Sentinel errors
  config/
    config.go              Configuration management
  server/
    server.go              HTTP server, route registration
    handlers.go            REST endpoint handlers
    middleware.go          Recovery, logging
    websocket.go           WebSocket event streaming
  notify/
    notify.go              Desktop notifications (notify-send)
packaging/
  bolt.service             systemd user unit file
  bolt.desktop             Desktop entry (for future GUI phases)
extensions/
  chrome/                  Chrome extension (adapted for daemon)
```

### What changed from `master`

- `cmd/bolt/gui.go` → removed (no Wails)
- `internal/app/` → removed (Wails IPC bindings)
- `internal/tray/` → removed (system tray)
- `internal/event/` → removed (in-process pub/sub)
- `internal/daemon/` → new (daemon lifecycle)
- `internal/service/` → new (application service layer)
- `embed.go` → removed (no frontend assets to embed)
- `frontend/` → removed (Svelte GUI)
- `wails.json` → removed

## 4. Daemon Lifecycle

### 4.1 Entry Point

`cmd/bolt/main.go` is the sole entry point.

```
bolt              Start daemon in foreground
bolt version      Print version and exit
bolt help         Print usage and exit
```

No subcommands for download management — all control goes through the API. A future CLI mode (`bolt add <url>`, `bolt list`) may be added as a thin API client.

### 4.2 Startup Sequence

```
1. Parse flags (--config, --verbose)
2. Load config from ~/.config/bolt/config.json (create with defaults if missing)
3. Instance detection: try connecting to existing Unix socket
   → If connection succeeds: print "daemon already running", exit 1
   → If connection fails: proceed
4. Open SQLite database at ~/.local/share/bolt/bolt.db
5. Run migrations
6. Create engine (with db, config snapshot func, rate limiter)
7. Engine.Start(): reset "active" and "verifying" downloads to "queued"
8. Create queue manager (with engine callbacks)
9. Start queue scheduler goroutine
10. Create HTTP server (with service layer)
11. Start Unix socket listener at $XDG_RUNTIME_DIR/bolt/bolt.sock
12. Notify systemd: sd_notify("READY=1") (if $NOTIFY_SOCKET is set)
13. Block on signal (SIGTERM, SIGINT) or listener failure
```

### 4.3 Shutdown Sequence

```
1. Receive SIGTERM or SIGINT
2. Notify systemd: sd_notify("STOPPING=1")
3. Stop accepting new API requests (close listeners)
4. Pause all active downloads (engine.Shutdown with 10s timeout)
   - Each active download: persist segment progress, stop workers
5. Stop queue scheduler
6. Close database
7. Remove Unix socket file
8. Exit 0
```

### 4.4 Signal Handling

| Signal | Action |
|--------|--------|
| `SIGTERM` | Graceful shutdown |
| `SIGINT` | Graceful shutdown |
| `SIGHUP` | Reserved for future config reload |

### 4.5 Instance Detection

The daemon uses socket probing instead of PID files:

```go
func isAlreadyRunning(socketPath string) bool {
    conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
    if err != nil {
        return false
    }
    conn.Close()
    return true
}
```

If the probe fails, the socket directory is verified for ownership and symlink safety before removing any stale socket file and proceeding with startup.

## 5. Service Layer

The service layer (`internal/service/`) is the coordination point between the API handlers and the engine/queue/db internals. It replaces the Wails `internal/app/` bindings.

### Purpose

- Provide a single set of methods that API handlers call.
- Coordinate engine, queue, and database operations.
- Handle cross-cutting concerns (duplicate detection, notification triggers).
- Keep API handlers thin (parse request → call service → format response).

### Key Methods

```go
type Service struct {
    engine  *engine.Engine
    queue   *queue.Manager
    store   *db.Store
    cfg     *config.Config
    cfgMu   sync.RWMutex   // protects all cfg access
    cfgPath string
    clients *ClientHub     // WebSocket client management
}

// Download operations
func (s *Service) AddDownload(ctx context.Context, req model.AddRequest) (*model.Download, error)
func (s *Service) PauseDownload(ctx context.Context, id string) error
func (s *Service) ResumeDownload(ctx context.Context, id string) error
func (s *Service) RetryDownload(ctx context.Context, id string) error
func (s *Service) CancelDownload(ctx context.Context, id string, deleteFile bool) error
func (s *Service) RefreshURL(ctx context.Context, id string, newURL string, headers map[string]string) error
func (s *Service) UpdateChecksum(ctx context.Context, id string, checksum *model.Checksum) error

// Query operations
func (s *Service) GetDownload(ctx context.Context, id string) (*model.Download, []model.Segment, error)
func (s *Service) ListDownloads(ctx context.Context, filter model.ListFilter) ([]model.Download, error)
func (s *Service) GetStats(ctx context.Context) map[string]any

// Probe
func (s *Service) ProbeURL(ctx context.Context, url string, headers map[string]string) (*model.ProbeResult, error)

// Config (mutex-protected)
func (s *Service) GetConfig() config.Config       // returns value copy under RLock
func (s *Service) UpdateConfig(ctx context.Context, apply func(cfg *config.Config)) error

// Queue
func (s *Service) ReorderDownloads(ctx context.Context, ids []string) error
```

The engine receives `svc.GetConfig` as a `func() config.Config` and snapshots it at the start of each operation. This eliminates data races between config updates and concurrent download operations.

### Control Callbacks vs. Observer Hooks

The daemon uses two different integration styles:

- direct method calls for authoritative control flow
- best-effort observer hooks for progress and status fanout

Authoritative paths include:

- queue slot release
- scheduler wakeups
- lifecycle transitions
- config changes that affect active downloads

Observer hooks include:

- progress fanout to clients
- notifications
- status broadcasts to WebSocket subscribers

No observer path may be required for download correctness.

### Event Broadcasting

The service layer owns event broadcasting to WebSocket clients:

```go
type ClientHub struct {
    mu      sync.RWMutex
    clients map[int]chan []byte
    nextID  int
}

func (h *ClientHub) Broadcast(event []byte) {
    h.mu.RLock()
    defer h.mu.RUnlock()
    for _, ch := range h.clients {
        select {
        case ch <- event:
        default:
            // Client too slow, drop event. Client can poll for current state.
        }
    }
}
```

The engine calls service-provided callbacks on state changes (completion, failure, progress). The service then:
1. Sends notifications (if applicable).
2. Broadcasts events to connected WebSocket clients.
3. Performs any non-authoritative side effects.

This keeps the engine free of WebSocket and notification knowledge. Queue coordination remains direct and must not depend on this observer path.

## 6. Engine

### 6.1 Reuse from `master`

The engine package is the most valuable code to reuse. The following files from `internal/engine/` on `master` are carried forward with minimal changes:

| File | Changes |
|------|---------|
| `engine.go` | Remove `event.Bus` dependency. Accept callback interface instead. Remove Wails-specific event types. |
| `segment.go` | No changes. Retry logic, error classification, rate limiting all solid. |
| `progress.go` | Replace `bus.Publish(ProgressEvent)` with callback invocation. Keep 500ms emit interval and 2s DB persist interval. |
| `probe.go` | No changes. |
| `filename.go` | No changes. |
| `checksum.go` | No changes. |
| `httpclient.go` | No changes. |
| `refresh.go` | No changes. |

### 6.2 Callback Interface

Replace the event bus with a callback interface:

```go
type Callbacks struct {
    OnProgress       func(id string, update model.ProgressUpdate)
    OnCompleted      func(id string, dl model.Download)
    OnFailed         func(id string, dl model.Download, err error)
    OnPaused         func(id string)
    OnResumed        func(id string)
    OnAdded          func(dl model.Download)
    OnRemoved        func(id string)
}
```

The engine invokes these callbacks directly. The service layer implements them to broadcast events and trigger notifications. If no callbacks are set, the engine operates silently, which is useful in tests.

### 6.3 Download Lifecycle

```
     ┌─────────┐
     │ queued  │◄──────────────────────────────┐
     └────┬────┘                               │
          │ queue scheduler picks              │ retry
          ▼                                    │
     ┌─────────┐                          ┌────┴────┐
     │ active  │─────────────────────────►│  error  │
     └────┬────┘  segment/network error   └─────────┘
          │
     ┌────┼──────────────┐
     │    │              │
     │    ▼              ▼
     │ ┌──────────┐ ┌──────────┐
     │ │completed │ │ paused   │
     │ └──────────┘ └─────┬────┘
     │                    │ resume
     │                    ▼
     │               ┌─────────┐
     └──────────────►│ queued  │
                     └─────────┘

  Special states:
  - "verifying": checksum verification in progress (after all segments complete)
  - "refresh": download URL has expired, awaiting new URL
```

### 6.3.1 Status semantics

| Status | Meaning |
|--------|---------|
| `queued` | Eligible to run when a scheduler slot is available |
| `active` | Currently downloading |
| `paused` | Stopped intentionally and resumable |
| `error` | Failed and awaiting explicit retry or refresh |
| `refresh` | Waiting for a new URL before resuming |
| `verifying` | Transfer done, checksum verification in progress |
| `completed` | Finished successfully |

`resumed` is an event, not a persistent status.

### 6.4 Segment Worker

Each segment worker (goroutine) handles one byte range:

1. Open/create the output file.
2. Seek to `segment.StartByte + segment.Downloaded`.
3. Send HTTP GET with `Range: bytes=<start+downloaded>-<end>`.
4. Read response body in chunks, `file.WriteAt()` at the correct offset.
5. Report progress to the aggregator via channel.
6. On error: classify as permanent (404, 403, 410, 416) or transient (5xx, timeout, DNS, connection reset).
7. Transient errors: exponential backoff (1s → 60s), up to `max_retries`.
8. Permanent errors: fail immediately.

Segment workers write at non-overlapping file offsets — no mutex needed for file I/O.

### 6.5 Progress Aggregation

The progress aggregator runs as a goroutine per active download:

- Collects per-segment progress reports via channel.
- Maintains a shadow copy of segment state (prevents double-counting on restart).
- Every 500ms: calculates total progress, speed (EMA), ETA → invokes `OnProgress` callback.
- Every 2s: batch-persists segment progress to SQLite (prepared statement in transaction).
- On download completion/failure: final persist, invoke completion/failure callback.

### 6.6 Speed Limiting

Global speed limit uses `golang.org/x/time/rate.Limiter` shared across all segment workers:

```go
// In engine
limiter := rate.NewLimiter(rate.Limit(bytesPerSec), burstSize)

// In segment worker, after reading N bytes
if err := limiter.WaitN(ctx, n); err != nil {
    return err
}
```

`SetSpeedLimit(0)` sets `rate.Inf` (unlimited). Changes take effect immediately for all active segments.

## 7. Queue Manager

### 7.1 Reuse from `master`

`internal/queue/queue.go` is carried forward with minimal changes. Its design is already close to daemon-compatible:

- Callback-based (no direct engine import).
- Database is source of truth for slot counting.
- Internal wakeups use a buffered channel (non-blocking).
- No Wails or event bus dependency (one trivial event publish to remove).

### 7.2 Scheduling

The queue scheduler runs as a goroutine:

```
loop:
  wait for signal (new enqueue, completion, config change)
  count active downloads from DB
  while active < max_concurrent:
    get next queued download (by queue_order ASC)
    if none: break
    call StartFunc(download)
    active++
```

### 7.3 Concurrency Changes

When `max_concurrent` is reduced below current active count:

1. Query active downloads ordered by `created_at DESC` (newest first).
2. Pause excess downloads until active count equals new limit.
3. Paused downloads return to queued state and re-enter the queue.

## 8. Database

### 8.1 Reuse from `master`

`internal/db/` is carried forward with minimal changes. It already has no Wails or GUI dependencies.

### 8.2 SQLite Configuration

```go
pragmas := []string{
    "PRAGMA journal_mode=WAL",
    "PRAGMA synchronous=NORMAL",
    "PRAGMA busy_timeout=5000",
    "PRAGMA cache_size=-20000",  // 20MB
    "PRAGMA foreign_keys=ON",
}
```

Single connection (`MaxOpenConns=1`) to prevent SQLITE_BUSY. WAL mode allows concurrent reads.

### 8.3 Schema

```sql
CREATE TABLE downloads (
    id          TEXT PRIMARY KEY,
    url         TEXT NOT NULL,
    filename    TEXT NOT NULL,
    dir         TEXT NOT NULL,
    total_size  INTEGER NOT NULL DEFAULT 0,
    downloaded  INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'queued',
    segments    INTEGER NOT NULL DEFAULT 1,
    speed_limit INTEGER NOT NULL DEFAULT 0,
    headers     TEXT NOT NULL DEFAULT '{}',
    referer_url TEXT NOT NULL DEFAULT '',
    checksum    TEXT NOT NULL DEFAULT '',
    etag        TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    completed_at TEXT,
    queue_order INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_downloads_status ON downloads(status);
CREATE INDEX idx_downloads_created_at ON downloads(created_at DESC);
CREATE INDEX idx_downloads_queue_order ON downloads(queue_order ASC);

CREATE TABLE segments (
    download_id TEXT NOT NULL,
    idx         INTEGER NOT NULL,
    start_byte  INTEGER NOT NULL,
    end_byte    INTEGER NOT NULL,
    downloaded  INTEGER NOT NULL DEFAULT 0,
    done        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (download_id, idx),
    FOREIGN KEY (download_id) REFERENCES downloads(id) ON DELETE CASCADE
);
```

### 8.4 Data Storage

- **Database:** `~/.local/share/bolt/bolt.db`
- **Headers:** JSON-marshaled into `headers` column.
- **Checksum:** Stored as `"algorithm:value"` string (e.g., `"sha256:abc123"`).
- **Dates:** TEXT format `"2006-01-02 15:04:05"`, parsed with `time.Parse`.
- **Downloaded files:** stored in the configured download directory, not inside the XDG data directory.

### 8.5 Migrations

Version-based migration using `PRAGMA user_version`:

```go
func (s *Store) migrate() error {
    var version int
    s.db.QueryRow("PRAGMA user_version").Scan(&version)
    for i := version; i < len(migrations); i++ {
        migrations[i](s.db)
        s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1))
    }
    return nil
}
```

## 9. Networking

### 9.1 Unix Socket

Primary API listener. Used by TUI, future GTK/Qt clients, and any local tooling.

```go
socketDir := filepath.Join(xdgRuntimeDir(), "bolt")
os.MkdirAll(socketDir, 0700)
socketPath := filepath.Join(socketDir, "bolt.sock")

listener, err := net.Listen("unix", socketPath)
os.Chmod(socketPath, 0600)
```

Socket directory permissions are `0700` (user-only). Socket file permissions are `0600`.

`$XDG_RUNTIME_DIR` is typically `/run/user/<uid>`. Fallback: `/tmp/bolt-<uid>/`.

If the fallback path is used, Bolt must create `/tmp/bolt-<uid>/` with `0700` permissions before binding the socket.

On shutdown, the socket file is removed. On startup, stale socket files (from crashes) are detected via connection probe and removed if dead.

Clients connect with:
```
curl --unix-socket /run/user/1000/bolt/bolt.sock http://localhost/api/stats
```

### 9.2 HTTP-over-Unix-Socket Server

The server returns an `http.Handler` via `Handler()`. The daemon owns listener creation and `http.Server` lifecycle. No TCP listener exists — only the Unix socket.

```go
type Server struct {
    svc *service.Service
}

func (s *Server) Handler() http.Handler {
    mux := http.NewServeMux()
    // register routes...
    return s.recovery(s.logging(mux))
}
```

### 9.3 Native Messaging Bridge (`bolt-host`)

Chrome extensions cannot connect to Unix sockets. Instead, Chrome spawns `bolt-host` as a native messaging host. The bridge is a short-lived, stateless process:

1. Chrome spawns `bolt-host` when the extension opens a native messaging port.
2. `bolt-host` connects to the daemon's Unix socket.
3. It reads length-prefixed JSON command messages from stdin and translates each into an HTTP request over the Unix socket. Responses are written back to stdout as length-prefixed JSON.
4. When the extension disconnects (stdin closes), `bolt-host` exits.

V1 is strictly request/response — no WebSocket subscription or event streaming. Each incoming command produces exactly one response. The `id` field in the protocol allows future async event forwarding without a breaking change (unsolicited messages would have no `id`).

The bridge binary lives at `~/.local/bin/bolt-host`. A native messaging host manifest is installed at `~/.config/google-chrome/NativeMessagingHosts/com.fhsinchy.bolt.json` (or the Chromium/Brave equivalent) by `make install`.

The bridge imports no daemon internals — it passes JSON through opaquely and uses a thin Unix socket HTTP client.

### 9.4 WebSocket Events

Event format (JSON over WebSocket):

```json
{"type": "progress", "data": {"id": "d_...", "downloaded": 1048576, "total": 10485760, "speed": 524288, "eta": 18}}
{"type": "completed", "data": {"id": "d_...", "filename": "file.iso"}}
{"type": "failed", "data": {"id": "d_...", "error": "404 Not Found"}}
{"type": "paused", "data": {"id": "d_..."}}
{"type": "resumed", "data": {"id": "d_..."}}
{"type": "added", "data": {"id": "d_...", "url": "https://...", "filename": "file.iso"}}
{"type": "removed", "data": {"id": "d_..."}}
```

Progress events are throttled to one per 500ms per download. The daemon drops events for slow clients (non-blocking send to buffered channel). Clients that need guaranteed state accuracy should poll via REST.

## 10. Configuration

### 10.1 Reuse from `master`

`internal/config/` is reused with field changes.

### 10.2 Config File

Location: `~/.config/bolt/config.json`

```json
{
    "download_dir": "/home/user/Downloads",
    "max_concurrent": 3,
    "default_segments": 16,
    "global_speed_limit": 0,
    "max_retries": 10,
    "min_segment_size": 1048576,
    "notifications": true
}
```

File permissions: `0600`. Directory permissions: `0700`.

### 10.3 Removed Fields (from `master`)

| Field | Reason |
|-------|--------|
| `minimize_to_tray` | GUI-specific |
| `theme` | GUI-specific |
| `server_port` | No TCP listener; Unix socket is the only API surface |
| `loopback_port` | Removed; Chrome extension uses native messaging bridge instead |
| `auth_token` | Removed; Unix socket permissions are the trust boundary |

### 10.4 Runtime Config Changes

Config changes via `PUT /api/config` take effect immediately:

| Field | Effect |
|-------|--------|
| `global_speed_limit` | Rate limiter updated instantly for all active segments |
| `max_concurrent` | Queue manager adjusts (may pause excess downloads) |
| `download_dir` | Applied to new downloads only |
| `notifications` | Toggled immediately |
| `default_segments` | Applied to new downloads only |

Config is saved atomically on every update (write to temp file, fsync, rename into place).

## 11. Notifications

### 11.1 Ownership

The daemon owns all notifications. Clients do not send notifications. This prevents duplicate notifications when multiple clients are connected.

### 11.2 Implementation

```go
func Send(title, message string) error {
    return exec.Command("notify-send", "-a", "Bolt", title, message).Start()
}
```

Notifications are fire-and-forget (`Start()`, not `Run()`). If `notify-send` is not available or no notification daemon is running, the error is logged and silently ignored.

Notification failure is never allowed to affect queue progress, download status, or API behavior.

### 11.3 Notification Events

| Event | Title | Message |
|-------|-------|---------|
| Download completed | "Download Complete" | filename |
| Download failed | "Download Failed" | filename: error |
| Checksum verification failed | "Checksum Mismatch" | filename |

Notifications are sent only if `config.notifications` is `true`.

## 12. systemd Integration

### 12.1 Unit File

`packaging/bolt.service`:

```ini
[Unit]
Description=Bolt Download Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart=%h/.local/bin/bolt
Restart=on-failure
RestartSec=5

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
# Users with a non-default download_dir should add it via drop-in:
#   systemctl --user edit bolt  →  [Service]\nReadWritePaths=/custom/path
ReadWritePaths=%h/.config/bolt %h/.local/share/bolt %h/Downloads
PrivateTmp=true

[Install]
WantedBy=default.target
```

### 12.2 sd_notify

The daemon integrates with systemd's notification protocol:

```go
func sdNotify(state string) {
    addr := os.Getenv("NOTIFY_SOCKET")
    if addr == "" {
        return
    }
    conn, err := net.Dial("unixgram", addr)
    if err != nil {
        return
    }
    defer conn.Close()
    conn.Write([]byte(state))
}
```

- `READY=1` — sent after all listeners are up.
- `STOPPING=1` — sent on shutdown signal.

No external dependency. Pure Go implementation using the systemd notification socket protocol.

### 12.3 Installation

```makefile
install:
	go build -o bolt ./cmd/bolt
	install -Dm755 bolt $(HOME)/.local/bin/bolt
	install -Dm644 packaging/bolt.service $(HOME)/.config/systemd/user/bolt.service
	systemctl --user daemon-reload
	systemctl --user enable --now bolt.service

uninstall:
	systemctl --user disable --now bolt.service || true
	rm -f $(HOME)/.local/bin/bolt
	rm -f $(HOME)/.config/systemd/user/bolt.service
	systemctl --user daemon-reload
```

## 13. XDG Directory Layout

| Purpose | Path | Env Var |
|---------|------|---------|
| Config | `~/.config/bolt/config.json` | `$XDG_CONFIG_HOME/bolt/` |
| Database | `~/.local/share/bolt/bolt.db` | `$XDG_DATA_HOME/bolt/` |
| Download files | Configured `download_dir` (default `~/Downloads`) | - |
| Unix socket | `$XDG_RUNTIME_DIR/bolt/bolt.sock` | `$XDG_RUNTIME_DIR/bolt/` |

All directories are created on first run with `0700` permissions.

## 14. Error Handling

### 14.1 Segment Error Classification

Reused from `master` (`engine/segment.go`):

**Permanent errors** (fail immediately):
- HTTP 404 Not Found
- HTTP 403 Forbidden
- HTTP 410 Gone
- HTTP 416 Range Not Satisfiable

**Transient errors** (retry with backoff):
- HTTP 5xx
- DNS resolution failure
- TLS handshake errors
- Connection reset/refused
- `io.UnexpectedEOF`
- Timeout

**Retry strategy:** exponential backoff starting at 1s, capped at 60s, up to `config.max_retries` attempts per segment.

### 14.2 API Error Responses

```json
{
    "error": "download not found",
    "code": "NOT_FOUND"
}
```

The `error` field is a human-readable message. The `code` field is a machine-readable string constant (e.g., `NOT_FOUND`, `DUPLICATE_FILENAME`, `VALIDATION_ERROR`). HTTP status codes are conveyed via the response status line, not in the body.

| Scenario | HTTP Status |
|----------|-------------|
| Download not found | 404 |
| Duplicate URL detected | 409 (with existing download in response) |
| Invalid request body | 400 |
| Download already active | 409 |
| Download not pausable | 409 |
| Server error | 500 |

### 14.3 Crash Recovery

On daemon startup, `Engine.Start()` runs:

```go
func (e *Engine) Start(ctx context.Context) error {
    // Any download left in "active" or "verifying" state was interrupted
    // by a crash. Reset to "queued" so the queue scheduler can restart them.
    for _, status := range []model.Status{model.StatusActive, model.StatusVerifying} {
        downloads, _ := e.store.ListDownloads(ctx, string(status), 0, 0)
        for i := range downloads {
            _ = e.store.UpdateDownloadStatus(ctx, downloads[i].ID, model.StatusQueued, "")
        }
    }
    return nil
}
```

The queue scheduler then picks up these downloads respecting concurrency limits. Segment progress is preserved in the database, so downloads resume from where they left off.

## 15. Testing Strategy

### 15.1 Reuse from `master`

- `internal/testutil/` — httptest server for download simulation.
- All existing unit and integration tests — adapted to remove Wails build tags.

### 15.2 Test Categories

| Category | Scope | How |
|----------|-------|-----|
| Unit | Individual functions (filename detection, format, checksum) | Standard `go test` |
| Integration | Engine + DB + Queue (full download lifecycle) | `httptest` server serving test files |
| API | HTTP handler behavior | `httptest` + `net/http` client |
| Stress | Concurrency, large files, many segments | Build tag `stress`, longer timeout |

### 15.3 Test Commands

```
make test          # go test ./...
make test-race     # go test -race ./...
make test-v        # go test -v ./...
make test-stress   # go test -tags stress ./...
make test-cover    # go test -coverprofile=coverage.out ./...
```

No Wails build tags needed. No CGO needed. Tests run on any Linux system with Go installed.

## 16. Build

### 16.1 Makefile

```makefile
BINARY := bolt

build:
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/bolt

test:
	go test ./...

test-race:
	go test -race ./...

test-stress:
	go test -tags stress -timeout 5m ./...

clean:
	rm -f $(BINARY)
	go clean -testcache

install: build
	install -Dm755 $(BINARY) $(HOME)/.local/bin/$(BINARY)
	install -Dm644 packaging/bolt.service $(HOME)/.config/systemd/user/bolt.service
	systemctl --user daemon-reload
	systemctl --user enable --now bolt.service

uninstall:
	systemctl --user disable --now bolt.service || true
	rm -f $(HOME)/.local/bin/$(BINARY)
	rm -f $(HOME)/.config/systemd/user/bolt.service
	systemctl --user daemon-reload
```

### 16.2 Build Output

Single static binary: `./bolt` (~15-20 MB estimated, pure Go with SQLite).

No frontend build step. No `wails build`. No `pnpm install`. No WebKit dependency.

## 17. Chrome Extension — Native Messaging Bridge

### 17.1 Architecture

The Chrome extension communicates with the daemon through a native messaging bridge (`bolt-host`), not through a network listener. Chrome spawns `bolt-host` per-connection; it is short-lived and stateless.

```
Chrome Extension  ←stdin/stdout→  bolt-host  ←HTTP-over-Unix-socket→  daemon
```

### 17.2 Native Messaging Protocol

Chrome native messaging uses length-prefixed JSON over stdin/stdout:

- **Extension → host:** 4-byte little-endian length prefix, then JSON payload.
- **Host → extension:** Same format in reverse.

`bolt-host` translates each incoming command message into an HTTP request to the daemon's Unix socket and writes the response back as a native message. V1 is request/response only — no WebSocket subscription (see Section 9.3).

### 17.3 Host Manifest

Installed at `~/.config/google-chrome/NativeMessagingHosts/com.fhsinchy.bolt.json`:

```json
{
    "name": "com.fhsinchy.bolt",
    "description": "Bolt Download Manager bridge",
    "path": "/home/user/.local/bin/bolt-host",
    "type": "stdio",
    "allowed_origins": ["chrome-extension://<extension-id>/"]
}
```

`make install` generates this manifest with the correct absolute path and extension ID. It installs to user-level directories for Chrome (`~/.config/google-chrome/NativeMessagingHosts/`), Chromium (`~/.config/chromium/NativeMessagingHosts/`), and Brave (`~/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts/`). System-wide or other Chromium-based browsers require manual manifest installation.

### 17.4 Extension Changes from `master`

| Change | Detail |
|--------|--------|
| Transport | Replace `fetch()` HTTP calls with `chrome.runtime.connectNative()` |
| Auth | Remove Bearer token — native messaging is restricted by Chrome to the declared extension ID |
| Port config | Remove — no loopback port needed |
| WebSocket | Remove — V1 is request/response only; the extension keeps a persistent native messaging port for commands |

### 17.5 Design Constraints

- `bolt-host` imports no daemon internals beyond shared protocol types.
- `bolt-host` is a separate binary (`cmd/bolt-host/main.go`), built independently.
- The daemon requires no changes to support the extension — the bridge is a client of the existing Unix socket API.

## 18. Implementation Phases

### Phase 1a: Daemon Core

1. Scaffold `cmd/bolt/main.go` — flag parsing, signal handling.
2. Copy `internal/model/`, `internal/config/`, `internal/db/` from `master`.
3. Copy `internal/engine/` from `master`, replace event bus with callbacks.
4. Copy `internal/queue/` from `master`, remove event publish.
5. Create `internal/service/` — service layer coordinating engine + queue + db.
6. Create `internal/daemon/` — lifecycle, socket setup.
7. Integration tests: add download, pause, resume, crash recovery.

### Phase 1b: API Layer

1. Copy `internal/server/` from `master`, adapt to return `http.Handler`.
2. Add Unix socket listener (only listener — no TCP).
3. Create `internal/server/websocket.go` — event streaming via service callbacks.
4. API tests: all endpoints via Unix socket.

### Phase 1c: Packaging

1. Create `packaging/bolt.service`.
2. Makefile: `build`, `test`, `install`, `uninstall`, `clean`.
3. sd_notify integration.
4. Copy `internal/notify/` from `master`.
5. End-to-end test: install, start, add download via curl, verify completion.

### Phase 2: Chrome Extension + Native Messaging Bridge

1. Create `cmd/bolt-host/main.go` — native messaging bridge binary.
2. Adapt `extensions/chrome/` to use `chrome.runtime.connectNative()` instead of `fetch()`.
3. Remove auth token, loopback port config from extension popup.
4. Generate native messaging host manifest during `make install`.
5. Test: install extension, verify download handoff through bridge → daemon.

### Phase 3: TUI

Deferred. Design and framework selection happens after the daemon is stable.
