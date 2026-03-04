# Bolt — Technical Requirements Document

**Companion to:** bolt-prd.md
**Version:** 1.0
**Author:** Farhan Hasin Chowdhury
**Date:** February 27, 2026
**Status:** Active

---

## Table of Contents

1. [Decisions & Constraints](#1-decisions--constraints)
2. [Architecture Overview](#2-architecture-overview)
3. [Go Module & Package Layout](#3-go-module--package-layout)
4. [Download Engine](#4-download-engine)
5. [Queue Manager](#5-queue-manager)
6. [Speed Limiter](#6-speed-limiter)
7. [Dead Link Refresh](#7-dead-link-refresh)
8. [Database Layer](#8-database-layer)
9. [Configuration Management](#9-configuration-management)
10. [HTTP Server & REST API](#10-http-server--rest-api)
11. [WebSocket](#11-websocket)
12. [CLI](#12-cli)
13. [Wails GUI Application](#13-wails-gui-application)
14. [Svelte Frontend](#14-svelte-frontend)
15. [Browser Extension](#15-browser-extension)
16. [Concurrency Model](#16-concurrency-model)
17. [Error Handling](#17-error-handling)
18. [Security](#18-security)
19. [Testing Strategy](#19-testing-strategy)
20. [Build & Distribution](#20-build--distribution)
21. [Systemd Integration](#21-systemd-integration)
22. [Appendix](#22-appendix)

---

## 1. Decisions & Constraints

Decisions made during TRD authoring that refine ambiguities in the PRD.

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Frontend framework | Svelte | Smaller bundle (~20 KB vs ~140 KB for React), simpler reactivity, recommended by PRD |
| Config storage | `config.json` file only | Human-editable, easy to back up, no SQLite `config` table needed |
| Process model (CLI without daemon) | Error with instructions | Print clear message: "Bolt is not running. Start with `bolt` or `bolt start`." |
| Systemd support | Yes | Ship a `bolt.service` user unit file; daemon mode is systemd-friendly |
| Go version | 1.23+ | Required for `net/http` routing enhancements (method + path patterns) |
| Download ID format | ULID (Universally Unique Lexicographically Sortable Identifier) | Sortable by creation time, URL-safe, no hyphens; prefixed with `d_` for display (e.g., `d_01JAXYZ...`) |
| Logging | `log/slog` (stdlib) | Structured logging, zero dependencies, JSON output for daemon mode |
| Frontend package manager | pnpm | Fast, disk-efficient, strict dependency resolution |
| CSS framework | Tailwind CSS v4 | Utility-first, small production builds, good Svelte integration |
| SQLite driver | `modernc.org/sqlite` | Pure Go, no CGO, cross-compiles cleanly |
| WebSocket library | `nhooyr.io/websocket` | Stdlib-compatible, context-aware, well-maintained |
| Wails version | v2 (latest stable) | Mature, good Linux support via GTK/WebKit |
| Test framework | stdlib `testing` + `net/http/httptest` | No external test dependencies; table-driven tests |
| ULID library | `github.com/oklog/ulid/v2` | Battle-tested, small, well-maintained |

---

## 2. Architecture Overview

### 2.1 Process Architecture

Bolt has two runtime modes sharing the same binary:

```
┌─────────────────────────────────────────────────────────┐
│                    bolt (single binary)                   │
│                                                           │
│  Mode 1: GUI (default)          Mode 2: Headless          │
│  ┌─────────────────────┐        ┌──────────────────────┐  │
│  │  Wails Window        │        │  No window            │  │
│  │  + System Tray       │        │  + Signal handling    │  │
│  │  + HTTP Server       │        │  + HTTP Server        │  │
│  │  + Download Engine   │        │  + Download Engine    │  │
│  │  + SQLite DB         │        │  + SQLite DB          │  │
│  └─────────────────────┘        └──────────────────────┘  │
│                                                           │
│  Mode 3: CLI Client                                       │
│  ┌─────────────────────┐                                  │
│  │  HTTP client only    │── Talks to running daemon ──►   │
│  │  No engine, no DB    │                                  │
│  └─────────────────────┘                                  │
└─────────────────────────────────────────────────────────┘
```

**Mode detection logic in `main.go`:**

```
if no subcommand or subcommand == "gui"  → Mode 1 (GUI)
if subcommand == "start"                 → Mode 2 (Headless)
if subcommand in {add, list, status, pause, resume, cancel, config, stop, refresh} → Mode 3 (CLI client)
```

### 2.2 Component Dependency Graph

```
main.go
  ├── cmd/          (CLI argument parsing, mode selection)
  │
  ├── internal/engine/    (download engine — core business logic)
  │     ├── uses: internal/db/
  │     ├── uses: internal/config/
  │     └── uses: internal/event/
  │
  ├── internal/server/    (HTTP + WebSocket server)
  │     ├── uses: internal/engine/
  │     ├── uses: internal/config/
  │     └── uses: internal/event/
  │
  ├── internal/db/        (SQLite data access layer)
  │     └── uses: modernc.org/sqlite
  │
  ├── internal/config/    (config.json management)
  │
  ├── internal/event/     (event bus for progress/status updates)
  │
  └── internal/app/       (Wails bindings — bridges engine to frontend)
        ├── uses: internal/engine/
        ├── uses: internal/config/
        └── uses: internal/event/
```

### 2.3 Data Flow: Adding a Download

```
Browser Extension                CLI                    GUI
      │                           │                      │
      │ POST /api/downloads       │ POST /api/downloads  │ Wails IPC bind
      ▼                           ▼                      ▼
┌──────────────────────────────────────────────────────────┐
│                    HTTP Handler / Wails Bind              │
│  1. Validate input                                        │
│  2. Send HEAD request to resolve filename + size          │
│  3. Check for range support                               │
│  4. Check for duplicate URL                               │
│  5. Insert download record into SQLite                    │
│  6. Enqueue download in queue manager                     │
│  7. Return download ID + metadata                         │
└──────────────────────┬───────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────┐
│                    Queue Manager                          │
│  1. Check active count < max_concurrent                   │
│  2. If slot available: start download                     │
│  3. If no slot: remain in queue (status: "queued")        │
└──────────────────────┬───────────────────────────────────┘
                       │ (when slot available)
                       ▼
┌──────────────────────────────────────────────────────────┐
│                    Download Engine                         │
│  1. Pre-allocate file with Truncate(totalSize)            │
│  2. Create N segment goroutines                           │
│  3. Each segment: GET with Range header, WriteAt offset   │
│  4. Progress reported via channels → event bus            │
│  5. Segment state persisted to SQLite every 2s            │
│  6. On completion: update DB, emit event, notify queue    │
└──────────────────────────────────────────────────────────┘
```

---

## 3. Go Module & Package Layout

### 3.1 Module Path

```
module github.com/fhsinchy/bolt
```

### 3.2 Directory Structure

```
bolt/
├── cmd/
│   └── bolt/
│       └── main.go                 # Entry point: mode detection, startup
├── internal/
│   ├── engine/
│   │   ├── engine.go               # Engine struct, Start/Stop, download lifecycle
│   │   ├── download.go             # Download struct, state machine
│   │   ├── segment.go              # Segment downloader goroutine
│   │   ├── probe.go                # HEAD request: filename, size, range detection
│   │   ├── filename.go             # Filename detection + deduplication
│   │   ├── limiter.go              # Speed limiter (token bucket wrapper)
│   │   ├── refresh.go              # Dead link refresh (Tier 1: auto)
│   │   └── progress.go             # Progress aggregator, speed/ETA calculation
│   ├── queue/
│   │   └── queue.go                # Queue manager: scheduling, concurrency control
│   ├── server/
│   │   ├── server.go               # HTTP server setup, middleware, routing
│   │   ├── handlers.go             # REST endpoint handlers
│   │   ├── websocket.go            # WebSocket upgrade + push loop
│   │   └── middleware.go           # Auth, CORS, logging, recovery middleware
│   ├── db/
│   │   ├── db.go                   # Open, Close, migrations, pragmas
│   │   ├── downloads.go            # Download CRUD operations
│   │   └── segments.go             # Segment CRUD operations
│   ├── config/
│   │   └── config.go               # Load, Save, defaults, validation
│   ├── event/
│   │   └── event.go                # Event bus: pub/sub for progress, status changes
│   ├── app/
│   │   └── app.go                  # Wails bindings (methods exposed to Svelte)
│   ├── cli/
│   │   └── cli.go                  # CLI client: HTTP calls to daemon, output formatting
│   ├── pid/
│   │   └── pid.go                  # PID file management (create, check, remove)
│   └── model/
│       └── model.go                # Shared types: Download, Segment, Status, etc.
├── frontend/                       # Svelte app (Wails frontend)
│   ├── src/
│   │   ├── App.svelte
│   │   ├── main.ts
│   │   ├── lib/
│   │   │   ├── components/         # Svelte components
│   │   │   ├── stores/             # Svelte stores (state management)
│   │   │   ├── types/              # TypeScript types (generated by Wails)
│   │   │   └── utils/              # Formatting helpers (bytes, speed, ETA)
│   │   └── views/
│   │       ├── DownloadList.svelte  # Main view
│   │       ├── AddDownload.svelte   # Add download dialog
│   │       └── Settings.svelte      # Settings panel
│   ├── index.html
│   ├── package.json
│   ├── pnpm-lock.yaml
│   ├── svelte.config.js
│   ├── tailwind.config.js
│   ├── tsconfig.json
│   └── vite.config.ts
├── extension/                      # Browser extension (Manifest V3)
│   ├── manifest.json
│   ├── background.js               # Service worker
│   ├── popup/
│   │   ├── popup.html
│   │   ├── popup.js
│   │   └── popup.css
│   ├── options/
│   │   ├── options.html
│   │   ├── options.js
│   │   └── options.css
│   ├── content.js                  # Content script (optional, for link detection)
│   └── icons/
│       ├── icon-16.png
│       ├── icon-32.png
│       ├── icon-48.png
│       └── icon-128.png
├── build/                          # Wails build config
│   └── linux/
├── dist/                           # Build output (gitignored)
├── testdata/                       # Test fixtures
├── bolt.service                    # Systemd user service file
├── go.mod
├── go.sum
├── wails.json
├── Makefile
├── bolt-prd.md
├── bolt-trd.md
└── .gitignore
```

### 3.3 External Dependencies

```go
// go.mod
require (
    github.com/wailsapp/wails/v2 v2.x.x
    nhooyr.io/websocket           v1.x.x
    modernc.org/sqlite            v1.x.x
    golang.org/x/time             v0.x.x  // rate.Limiter for speed limiting
    github.com/oklog/ulid/v2      v2.x.x  // ULID generation
)
```

No other external Go dependencies. Everything else uses stdlib.

---

## 4. Download Engine

### 4.1 Core Types

```go
// internal/model/model.go

type Status string

const (
    StatusQueued    Status = "queued"
    StatusActive    Status = "active"
    StatusPaused    Status = "paused"
    StatusCompleted Status = "completed"
    StatusError     Status = "error"
    StatusScheduled Status = "scheduled"
    StatusRefresh   Status = "refresh" // waiting for link refresh
)

type Download struct {
    ID           string            `json:"id"`
    URL          string            `json:"url"`
    Filename     string            `json:"filename"`
    Dir          string            `json:"dir"`
    TotalSize    int64             `json:"total_size"`    // -1 if unknown
    Downloaded   int64             `json:"downloaded"`
    Status       Status            `json:"status"`
    SegmentCount int               `json:"segments"`
    SpeedLimit   int64             `json:"speed_limit"`   // bytes/sec, 0 = unlimited
    Headers      map[string]string `json:"headers"`       // cookies, referer, UA
    RefererURL   string            `json:"referer_url"`   // original page URL for link refresh
    Checksum     *Checksum         `json:"checksum"`
    Error        string            `json:"error"`
    ETag         string            `json:"etag"`          // for resume validation
    LastModified string            `json:"last_modified"` // for resume validation
    CreatedAt    time.Time         `json:"created_at"`
    CompletedAt  *time.Time        `json:"completed_at"`
    ScheduledAt  *time.Time        `json:"scheduled_at"`
    QueueOrder   int               `json:"queue_order"`
}

type Checksum struct {
    Algorithm string `json:"algorithm"` // md5, sha1, sha256
    Value     string `json:"value"`
}

type Segment struct {
    DownloadID string `json:"download_id"`
    Index      int    `json:"index"`
    StartByte  int64  `json:"start_byte"`
    EndByte    int64  `json:"end_byte"`  // inclusive
    Downloaded int64  `json:"downloaded"`
    Done       bool   `json:"done"`
}

type ProbeResult struct {
    Filename       string
    TotalSize      int64  // -1 if Content-Length absent
    AcceptsRanges  bool
    ETag           string
    LastModified   string
    FinalURL       string // after redirects
    ContentType    string
}

type ProgressUpdate struct {
    DownloadID string  `json:"id"`
    Downloaded int64   `json:"downloaded"`
    Speed      int64   `json:"speed"`      // bytes/sec
    ETA        int     `json:"eta"`        // seconds remaining, -1 if unknown
    Status     Status  `json:"status"`
    Segments   []SegmentProgress `json:"segments,omitempty"`
}

type SegmentProgress struct {
    Index      int   `json:"index"`
    Downloaded int64 `json:"downloaded"`
    Done       bool  `json:"done"`
}
```

### 4.2 Engine Interface

```go
// internal/engine/engine.go

type Engine struct {
    db        *db.Store
    cfg       *config.Config
    bus       *event.Bus
    queue     *queue.Manager
    client    *http.Client

    mu        sync.Mutex
    active    map[string]*activeDownload // download ID → running download
}

type activeDownload struct {
    download   *model.Download
    segments   []*segmentWorker
    cancel     context.CancelFunc
    pauseCh    chan struct{}
    progressCh chan segmentReport
    limiter    *Limiter          // per-download speed limiter
}

// Public API (called by server handlers and Wails bindings)
func New(db *db.Store, cfg *config.Config, bus *event.Bus) *Engine
func (e *Engine) AddDownload(ctx context.Context, req AddRequest) (*model.Download, error)
func (e *Engine) PauseDownload(ctx context.Context, id string) error
func (e *Engine) ResumeDownload(ctx context.Context, id string) error
func (e *Engine) CancelDownload(ctx context.Context, id string, deleteFile bool) error
func (e *Engine) RetryDownload(ctx context.Context, id string) error
func (e *Engine) RefreshURL(ctx context.Context, id string, newURL string) error
func (e *Engine) GetDownload(ctx context.Context, id string) (*model.Download, []model.Segment, error)
func (e *Engine) ListDownloads(ctx context.Context, filter ListFilter) ([]model.Download, error)
func (e *Engine) Probe(ctx context.Context, url string, headers map[string]string) (*model.ProbeResult, error)
func (e *Engine) Start(ctx context.Context) error  // resume incomplete downloads from DB
func (e *Engine) Shutdown(ctx context.Context) error // graceful: pause all, persist state

type AddRequest struct {
    URL        string            `json:"url"`
    Filename   string            `json:"filename"`    // optional, auto-detected if empty
    Dir        string            `json:"dir"`         // optional, uses default from config
    Segments   int               `json:"segments"`    // optional, uses default from config
    Headers    map[string]string `json:"headers"`     // optional, cookies/referer/UA
    RefererURL string            `json:"referer_url"` // optional, page URL for link refresh
    SpeedLimit int64             `json:"speed_limit"` // optional, 0 = unlimited
    Checksum   *model.Checksum   `json:"checksum"`    // optional
}

type ListFilter struct {
    Status string `json:"status"` // empty = all
    Limit  int    `json:"limit"`
    Offset int    `json:"offset"`
}
```

### 4.3 Probe (HEAD Request)

The `Probe` function resolves download metadata before adding:

```go
// internal/engine/probe.go

func (e *Engine) Probe(ctx context.Context, rawURL string, headers map[string]string) (*model.ProbeResult, error)
```

**Procedure:**

1. Build `http.Request` with method `HEAD`.
2. Apply forwarded headers (Cookie, Referer, User-Agent) from the request.
3. Execute with `e.client.Do(req)` — follows redirects (up to 10).
4. Read response headers:
   - `Content-Length` → `TotalSize` (set to `-1` if absent or `0`).
   - `Accept-Ranges: bytes` → `AcceptsRanges = true`.
   - `Content-Disposition` → parse `filename=` / `filename*=` for `Filename`.
   - `ETag` → store for resume validation.
   - `Last-Modified` → store for resume validation.
   - `Content-Type` → store for file type detection.
5. If `Content-Disposition` didn't yield a filename, extract from the final URL's path (URL-decoded, last segment).
6. If still empty, generate `download_<ULID>`.
7. Record `FinalURL` (after redirects) — this becomes the actual download URL.

**Edge cases:**
- Some servers return `200` for HEAD but don't include `Content-Length`. In this case, `TotalSize = -1` and we fall back to single-connection mode (can't compute ranges without knowing total size).
- Some servers reject HEAD requests with `405`. Fallback: send a `GET` with `Range: bytes=0-0`, read `Content-Range` header for total size, then abort the body.

### 4.4 Filename Detection & Deduplication

```go
// internal/engine/filename.go

func DetectFilename(userProvided string, contentDisposition string, finalURL string) string
func DeduplicateFilename(dir string, filename string) string
```

**DetectFilename priority:**

1. `userProvided` — if non-empty, use as-is.
2. `contentDisposition` — parse RFC 6266. Handle `filename*=UTF-8''...` (RFC 5987 encoding) and plain `filename="..."`.
3. `finalURL` — `url.Parse`, take `path.Base`, `url.PathUnescape`. Strip query parameters. If result is empty or `/`, fall back.
4. Fallback: `download_<ULID-short>` (first 10 chars of ULID for brevity).

**DeduplicateFilename:**

If `dir/filename` exists, try `filename(1).ext`, `filename(2).ext`, etc., up to 999. The duplicate counter is inserted before the final extension: `archive.tar.gz` → `archive(1).tar.gz`.

### 4.5 Segment Downloader

```go
// internal/engine/segment.go

type segmentWorker struct {
    download   *model.Download
    segment    *model.Segment
    client     *http.Client
    limiter    *Limiter
    globalLim  *Limiter
    reportCh   chan<- segmentReport
    file       *os.File // shared file handle, writes via WriteAt
}

type segmentReport struct {
    Index      int
    BytesRead  int64 // bytes read in this report
    Done       bool
    Err        error
}

func (w *segmentWorker) Run(ctx context.Context)
```

**Segment download procedure:**

1. Compute resume point: `startByte = segment.StartByte + segment.Downloaded`.
2. If `startByte > segment.EndByte`, mark done and return.
3. Build `GET` request with `Range: bytes=<startByte>-<segment.EndByte>`.
4. Apply download headers (cookies, referer, UA).
5. Execute request. Expect `206 Partial Content`.
6. Wrap response body reader:
   - If per-download limiter exists: `limiter.Reader(body)`.
   - If global limiter exists: `globalLimiter.Reader(wrappedBody)`.
7. Read in a loop with a `32 KB` buffer:
   - `n, err := reader.Read(buf)`
   - `file.WriteAt(buf[:n], startByte + segment.Downloaded)`
   - `segment.Downloaded += int64(n)`
   - Send `segmentReport{Index, n, false, nil}` to `reportCh`.
   - Check `ctx.Done()` for cancellation (pause/cancel).
8. On `io.EOF` or `segment.Downloaded >= (segment.EndByte - segment.StartByte + 1)`: mark done.
9. On error: send `segmentReport{Index, 0, false, err}`.

**Retry logic (per-segment):**

Wraps the segment `Run` in a retry loop:

```go
func (w *segmentWorker) RunWithRetry(ctx context.Context) {
    maxRetries := 10  // configurable
    backoff := 1 * time.Second

    for attempt := 0; attempt <= maxRetries; attempt++ {
        err := w.Run(ctx)
        if err == nil {
            return // success
        }
        if isPermanentError(err) {
            w.reportCh <- segmentReport{w.segment.Index, 0, false, err}
            return
        }
        if attempt < maxRetries {
            select {
            case <-time.After(backoff):
                backoff = min(backoff*2, 60*time.Second)
            case <-ctx.Done():
                return
            }
        }
    }
    w.reportCh <- segmentReport{w.segment.Index, 0, false, ErrMaxRetriesExceeded}
}
```

**Permanent vs. transient errors:**

| Permanent (fail immediately) | Transient (retry) |
|-----|-----|
| 404 Not Found | Timeout (context deadline) |
| 403 Forbidden (after refresh attempts) | Connection reset |
| 410 Gone (after refresh attempts) | 5xx Server Error |
| 416 Range Not Satisfiable | DNS resolution failure |
| | TLS handshake timeout |
| | io.UnexpectedEOF |

Note: 403/410 are initially treated as potential expiry signals for dead link refresh (see Section 7). They only become permanent after refresh attempts are exhausted.

### 4.6 Progress Aggregator

```go
// internal/engine/progress.go

type progressAggregator struct {
    downloadID string
    segments   []model.Segment
    reportCh   <-chan segmentReport
    bus        *event.Bus
    db         *db.Store

    mu         sync.Mutex
    speeds     []int64 // rolling window for speed calculation (5 samples)
    lastBytes  int64
    lastTime   time.Time
}

func (p *progressAggregator) Run(ctx context.Context)
```

**Procedure:**

1. Start a ticker at `500ms` interval (progress emission rate).
2. Listen on `reportCh` for segment updates. Accumulate `Downloaded` per segment.
3. On each tick:
   - Compute `totalDownloaded` = sum of all segment `Downloaded` values.
   - Compute speed: `(totalDownloaded - lastBytes) / elapsed` using a rolling 5-sample average for smoothness.
   - Compute ETA: `(totalSize - totalDownloaded) / speed`. If speed is 0 or totalSize is unknown, ETA = -1.
   - Emit `ProgressUpdate` to `event.Bus`.
4. Every 2 seconds: batch-write segment progress to SQLite (see Section 8.3).
5. On completion (all segments done): emit final progress, update download status in DB.

**Speed calculation (rolling average):**

```
speeds = circular buffer of 5 most recent speed samples (each sample = bytes/500ms, scaled to bytes/sec)
reported speed = average(speeds)
```

This prevents jittery speed display while still being responsive.

### 4.7 Download Lifecycle State Machine

```
                ┌──────────┐
                │  queued   │
                └─────┬────┘
                      │ (slot available)
                      ▼
   ┌──────────► ┌──────────┐ ──────────► ┌───────────┐
   │            │  active   │             │ completed  │
   │            └──┬───┬───┘             └───────────┘
   │               │   │
   │  (resume)     │   │ (error)
   │               │   ▼
   │            ┌──┴───────┐    (retry)   ┌──────────┐
   │            │  paused   │ ◄─────────── │  error    │
   │            └──────────┘              └──────────┘
   │               │
   └───────────────┘
                      │ (cancel)
                      ▼
               ┌──────────┐
               │ (removed) │  ← not a DB status; row is deleted
               └──────────┘
```

**State transitions:**

| From | To | Trigger |
|------|----|---------|
| queued | active | Queue manager assigns slot |
| active | completed | All segments done |
| active | paused | User pauses |
| active | error | Segment retries exhausted |
| active | refresh | Dead link expiry detected |
| paused | active | User resumes (if slot available) or queued |
| paused | queued | User resumes but no slot available |
| error | active | User retries (if slot available) |
| error | queued | User retries but no slot available |
| refresh | active | URL refreshed successfully |
| any | (deleted) | User cancels/removes |
| scheduled | queued | Scheduled time reached |

### 4.8 HTTP Client Configuration

```go
func newHTTPClient(cfg *config.Config) *http.Client {
    transport := &http.Transport{
        MaxIdleConnsPerHost:   32,
        MaxConnsPerHost:       0, // unlimited (we control via segment count)
        TLSHandshakeTimeout:  10 * time.Second,
        ResponseHeaderTimeout: 15 * time.Second,
        IdleConnTimeout:       90 * time.Second,
        DisableCompression:    true, // we want raw bytes, not gzipped
        // Proxy set from config if configured
    }

    if cfg.Proxy != "" {
        proxyURL, _ := url.Parse(cfg.Proxy)
        transport.Proxy = http.ProxyURL(proxyURL)
    }

    jar, _ := cookiejar.New(&cookiejar.Options{
        PublicSuffixList: publicsuffix.List,
    })

    return &http.Client{
        Transport: transport,
        Jar:       jar,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            if len(via) >= 10 {
                return errors.New("too many redirects (max 10)")
            }
            return nil
        },
    }
}
```

**Why `DisableCompression: true`:**

The HTTP transport by default adds `Accept-Encoding: gzip` and transparently decompresses. This breaks byte range requests because `Content-Length` would reflect uncompressed size but the server might serve compressed data. Disabling compression ensures we get raw bytes matching the ranges we request.

### 4.9 File Pre-allocation

Before downloading starts:

```go
file, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY, 0644)
if err != nil { return err }
if totalSize > 0 {
    err = file.Truncate(totalSize)
    if err != nil { return err }
}
```

This pre-allocates disk space, reducing fragmentation during parallel writes. Each segment goroutine receives this shared `*os.File` handle and writes via `file.WriteAt(buf, offset)` — no mutex needed because byte ranges are non-overlapping.

### 4.10 Single-Connection Fallback

When `ProbeResult.AcceptsRanges == false` or `TotalSize == -1`:

- Use 1 segment spanning the entire file.
- Download sequentially without `Range` header.
- File size unknown: write to a temp file, rename on completion.
- Resume is not possible (no range support), so segment `Downloaded` is not meaningful for resume — a restart means re-downloading from scratch.
- Progress shows downloaded bytes but no percentage or ETA (unknown total).

### 4.11 Graceful Shutdown

When `Engine.Shutdown(ctx)` is called (on app quit or SIGTERM):

1. Set a shutdown flag to prevent new downloads from starting.
2. For each active download:
   a. Cancel the download's context (signals all segment goroutines to stop).
   b. Wait for segment goroutines to exit (they check `ctx.Done()` in their read loop).
   c. Persist final segment progress to SQLite.
   d. Update download status to `paused` in DB.
3. Close the shared file handles.
4. Close the SQLite database.
5. Remove the PID file.

Shutdown timeout: 10 seconds. If goroutines don't exit within this time, they are abandoned (the process exits).

---

## 5. Queue Manager

```go
// internal/queue/queue.go

type Manager struct {
    engine       *engine.Engine // back-reference for starting downloads
    db           *db.Store
    bus          *event.Bus
    maxConcurrent int

    mu           sync.Mutex
    activeCount  int
    queue        []*model.Download // ordered by QueueOrder
    notify       chan struct{}     // signal to re-evaluate queue
}

func New(db *db.Store, bus *event.Bus, maxConcurrent int) *Manager
func (m *Manager) Enqueue(d *model.Download) error
func (m *Manager) Dequeue(id string) error        // remove from queue (cancel)
func (m *Manager) Promote(id string) error         // move to front of queue
func (m *Manager) Reorder(id string, newPos int) error
func (m *Manager) OnDownloadComplete(id string)    // called when a download finishes
func (m *Manager) Run(ctx context.Context)         // main loop
func (m *Manager) ActiveCount() int
```

**Queue evaluation loop:**

```go
func (m *Manager) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-m.notify:
            m.evaluate()
        }
    }
}

func (m *Manager) evaluate() {
    m.mu.Lock()
    defer m.mu.Unlock()

    for m.activeCount < m.maxConcurrent && len(m.queue) > 0 {
        next := m.queue[0]
        m.queue = m.queue[1:]
        m.activeCount++
        go m.engine.startDownload(next) // starts segment goroutines
    }
}
```

The `notify` channel is signaled when:
- A new download is enqueued.
- An active download completes, fails, or is paused/cancelled.
- `maxConcurrent` setting changes.

### 5.1 Queue Persistence

Queue order is stored in the `queue_order` column of the `downloads` table. On startup, `Engine.Start()` loads all `queued` and `active` downloads from DB, re-populates the queue, and re-evaluates.

---

## 6. Speed Limiter

```go
// internal/engine/limiter.go

type Limiter struct {
    limiter *rate.Limiter // from golang.org/x/time/rate
}

func NewLimiter(bytesPerSec int64) *Limiter
func (l *Limiter) SetLimit(bytesPerSec int64)
func (l *Limiter) Reader(r io.Reader) io.Reader // returns throttled reader
func (l *Limiter) Unlimited() bool
```

**Architecture:**

- **Global limiter:** One instance shared across all active downloads. Stored in `Engine`.
- **Per-download limiter:** One instance per active download. Stored in `activeDownload`.
- Each segment goroutine wraps its response body reader:

```go
var reader io.Reader = resp.Body
if download.limiter != nil && !download.limiter.Unlimited() {
    reader = download.limiter.Reader(reader)
}
if engine.globalLimiter != nil && !engine.globalLimiter.Unlimited() {
    reader = engine.globalLimiter.Reader(reader)
}
```

**Throttled reader implementation:**

```go
type throttledReader struct {
    r       io.Reader
    limiter *rate.Limiter
    ctx     context.Context
}

func (tr *throttledReader) Read(p []byte) (int, error) {
    // Limit read size to avoid large bursts
    if len(p) > 32*1024 {
        p = p[:32*1024]
    }
    n, err := tr.r.Read(p)
    if n > 0 {
        // Wait for token bucket to allow these bytes
        if waitErr := tr.limiter.WaitN(tr.ctx, n); waitErr != nil {
            return n, waitErr
        }
    }
    return n, err
}
```

**Rate limiter configuration:**

The `rate.Limiter` is configured with:
- `rate.Limit`: bytes per second.
- Burst: `64 * 1024` (64 KB) — allows small bursts for better throughput on bursty connections.

When the user changes the speed limit at runtime, `SetLimit` updates the limiter without interrupting active downloads.

---

## 7. Dead Link Refresh

### 7.1 Expiry Detection

```go
// internal/engine/refresh.go

type expiryDetector struct {
    mu              sync.Mutex
    failureCounts   map[int]int // HTTP status code → consecutive count
}

func (d *expiryDetector) RecordFailure(statusCode int) bool // returns true if expiry detected
func (d *expiryDetector) Reset()
```

**Trigger condition:** 3 consecutive segment failures with the same HTTP status code from the set `{401, 403, 410}`. This distinguishes expired URLs from transient server issues (which produce different error codes or network errors).

### 7.2 Tier 1 — Automatic Refresh

When expiry is detected:

1. Pause all active segments for this download (cancel their contexts).
2. Set download status to `refresh`.
3. Retrieve stored `RefererURL` and original `Headers` from DB.
4. Send a `GET` request to the referer URL with original headers.
5. Follow redirects. At each redirect hop, check if the URL looks like a direct download link (check `Content-Type` and `Content-Disposition`).
6. If the final response has `Content-Length` matching `download.TotalSize`:
   - Store the new URL.
   - Update headers if the server sent `Set-Cookie`.
   - Resume all incomplete segments with the new URL.
   - Set download status back to `active`.
7. If the sizes don't match or the request fails: fall through to Tier 2/3 (mark as `refresh` and notify user).

### 7.3 Tier 2 — Extension-Assisted Refresh

The browser extension participates via a REST endpoint:

```
POST /api/downloads/:id/refresh
{
    "url": "https://cdn.example.com/new-signed-url/file.zip",
    "headers": { "Cookie": "new-session=xyz" }
}
```

The extension recognizes refresh candidates by matching:
- Exact filename match against downloads in `refresh` status.
- Same `Content-Length` from a URL on the same domain.

Implementation: The extension queries `GET /api/downloads?status=refresh` when the user triggers a download on a page matching a refresh candidate. If a match is found, it calls the refresh endpoint instead of creating a new download.

### 7.4 Tier 3 — Manual URL Paste

Exposed via:
- GUI: right-click → "Update URL" on a failed/refresh download.
- CLI: `bolt refresh <id> <new-url>`.
- API: `POST /api/downloads/:id/refresh` (same endpoint as Tier 2).

**Validation:** Before swapping, Bolt sends a HEAD request to the new URL and verifies `Content-Length` matches the original download. If it doesn't, the user is warned and asked to confirm.

---

## 8. Database Layer

### 8.1 SQLite Configuration

```go
// internal/db/db.go

func Open(path string) (*Store, error) {
    db, err := sql.Open("sqlite", path)
    if err != nil {
        return nil, err
    }

    // Performance pragmas
    pragmas := []string{
        "PRAGMA journal_mode=WAL",          // Write-ahead logging for concurrent reads
        "PRAGMA synchronous=NORMAL",        // Good balance of safety and speed
        "PRAGMA busy_timeout=5000",         // Wait 5s on lock contention
        "PRAGMA cache_size=-20000",         // 20 MB page cache
        "PRAGMA foreign_keys=ON",           // Enforce FK constraints
        "PRAGMA auto_vacuum=INCREMENTAL",   // Reclaim space without full vacuum
        "PRAGMA temp_store=MEMORY",         // Temp tables in memory
    }

    for _, pragma := range pragmas {
        _, err = db.ExecContext(ctx, pragma)
    }

    db.SetMaxOpenConns(1)     // SQLite doesn't handle concurrent writes well
    db.SetMaxIdleConns(1)
    db.SetConnMaxLifetime(0)  // Keep connection alive forever

    return &Store{db: db}, nil
}
```

**Why `SetMaxOpenConns(1)`:** SQLite uses file-level locking. With WAL mode, multiple readers can proceed concurrently, but only one writer at a time. Using a single connection through Go's `database/sql` pool serializes all writes through one connection, avoiding `SQLITE_BUSY` errors. Reads are still fast because WAL allows readers to proceed during writes.

### 8.2 Schema & Migrations

```go
// internal/db/db.go

func (s *Store) migrate() error {
    // Version-based migration. Store current schema version in user_version pragma.
    var version int
    s.db.QueryRow("PRAGMA user_version").Scan(&version)

    migrations := []func(*sql.Tx) error{
        migrateV1, // initial schema
        // migrateV2, etc. for future changes
    }

    for i := version; i < len(migrations); i++ {
        tx, _ := s.db.Begin()
        if err := migrations[i](tx); err != nil {
            tx.Rollback()
            return err
        }
        tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1))
        tx.Commit()
    }
    return nil
}

func migrateV1(tx *sql.Tx) error {
    _, err := tx.Exec(`
        CREATE TABLE IF NOT EXISTS downloads (
            id           TEXT PRIMARY KEY,
            url          TEXT NOT NULL,
            filename     TEXT NOT NULL,
            dir          TEXT NOT NULL,
            total_size   INTEGER DEFAULT -1,
            downloaded   INTEGER DEFAULT 0,
            status       TEXT DEFAULT 'queued',
            segments     INTEGER DEFAULT 16,
            speed_limit  INTEGER DEFAULT 0,
            headers      TEXT,                    -- JSON
            referer_url  TEXT DEFAULT '',
            checksum     TEXT DEFAULT '',          -- "algorithm:value" or empty
            etag         TEXT DEFAULT '',
            last_modified TEXT DEFAULT '',
            error        TEXT DEFAULT '',
            created_at   TEXT DEFAULT (datetime('now')),
            completed_at TEXT,
            scheduled_at TEXT,
            queue_order  INTEGER DEFAULT 0
        );

        CREATE TABLE IF NOT EXISTS segments (
            download_id TEXT NOT NULL,
            idx         INTEGER NOT NULL,
            start_byte  INTEGER NOT NULL,
            end_byte    INTEGER NOT NULL,
            downloaded  INTEGER DEFAULT 0,
            done        INTEGER DEFAULT 0,
            PRIMARY KEY (download_id, idx),
            FOREIGN KEY (download_id) REFERENCES downloads(id) ON DELETE CASCADE
        );

        CREATE INDEX IF NOT EXISTS idx_downloads_status ON downloads(status);
        CREATE INDEX IF NOT EXISTS idx_downloads_created ON downloads(created_at DESC);
        CREATE INDEX IF NOT EXISTS idx_downloads_queue ON downloads(queue_order ASC);
    `)
    return err
}
```

### 8.3 Batch Progress Persistence

Segment progress is written to SQLite every 2 seconds to minimize write I/O while limiting data loss on crash:

```go
func (s *Store) BatchUpdateSegments(segments []model.Segment) error {
    tx, err := s.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    stmt, err := tx.Prepare(`
        UPDATE segments SET downloaded = ?, done = ?
        WHERE download_id = ? AND idx = ?
    `)
    if err != nil {
        return err
    }
    defer stmt.Close()

    for _, seg := range segments {
        _, err = stmt.Exec(seg.Downloaded, boolToInt(seg.Done), seg.DownloadID, seg.Index)
        if err != nil {
            return err
        }
    }

    return tx.Commit()
}
```

Also called on:
- Download pause
- Download cancel
- Application shutdown (graceful)

**Worst case data loss on crash:** Up to 2 seconds of download progress. On resume, segments restart from the last persisted offset — duplicate downloaded bytes are overwritten identically, so there is no data corruption.

### 8.4 Store Interface

```go
// internal/db/downloads.go

type Store struct {
    db *sql.DB
}

// Downloads
func (s *Store) InsertDownload(ctx context.Context, d *model.Download) error
func (s *Store) GetDownload(ctx context.Context, id string) (*model.Download, error)
func (s *Store) ListDownloads(ctx context.Context, status string, limit, offset int) ([]model.Download, error)
func (s *Store) UpdateDownloadStatus(ctx context.Context, id string, status model.Status, errMsg string) error
func (s *Store) UpdateDownloadURL(ctx context.Context, id string, newURL string, newHeaders map[string]string) error
func (s *Store) UpdateDownloadProgress(ctx context.Context, id string, downloaded int64) error
func (s *Store) SetCompleted(ctx context.Context, id string) error
func (s *Store) DeleteDownload(ctx context.Context, id string) error
func (s *Store) GetNextQueued(ctx context.Context) (*model.Download, error)
func (s *Store) CountByStatus(ctx context.Context, status model.Status) (int, error)

// Segments
func (s *Store) InsertSegments(ctx context.Context, segments []model.Segment) error
func (s *Store) GetSegments(ctx context.Context, downloadID string) ([]model.Segment, error)
func (s *Store) BatchUpdateSegments(ctx context.Context, segments []model.Segment) error
func (s *Store) DeleteSegments(ctx context.Context, downloadID string) error
```

---

## 9. Configuration Management

### 9.1 Config File Location

| Platform | Path |
|----------|------|
| Linux | `~/.config/bolt/config.json` |

Uses `os.UserConfigDir()` which returns `~/.config` on Linux.

### 9.2 Config Struct

```go
// internal/config/config.go

type Config struct {
    DownloadDir      string            `json:"download_dir"`
    Categorize       bool              `json:"categorize"`
    MaxConcurrent    int               `json:"max_concurrent"`
    DefaultSegments  int               `json:"default_segments"`
    GlobalSpeedLimit int64             `json:"global_speed_limit"`   // bytes/sec, 0 = unlimited
    ServerPort       int               `json:"server_port"`
    AuthToken        string            `json:"auth_token"`
    MinimizeToTray   bool              `json:"minimize_to_tray"`
    ClipboardMonitor bool              `json:"clipboard_monitor"`
    SoundOnComplete  bool              `json:"sound_on_complete"`
    Theme            string            `json:"theme"`                // "light", "dark", "system"
    Proxy            string            `json:"proxy"`                // "" = no proxy
    MaxRetries       int               `json:"max_retries"`
    MinSegmentSize   int64             `json:"min_segment_size"`     // bytes
    Categories       map[string][]string `json:"categories"`
}

func DefaultConfig() *Config {
    return &Config{
        DownloadDir:      defaultDownloadDir(), // ~/Downloads
        Categorize:       false,
        MaxConcurrent:    3,
        DefaultSegments:  16,
        GlobalSpeedLimit: 0,
        ServerPort:       6800,
        AuthToken:        generateToken(), // crypto/rand UUID
        MinimizeToTray:   true,
        ClipboardMonitor: false,
        SoundOnComplete:  true,
        Theme:            "system",
        Proxy:            "",
        MaxRetries:       10,
        MinSegmentSize:   1 * 1024 * 1024, // 1 MB
        Categories: map[string][]string{
            "Video":      {".mp4", ".mkv", ".avi", ".mov", ".webm", ".flv"},
            "Compressed": {".zip", ".tar.gz", ".tar.bz2", ".rar", ".7z", ".gz", ".xz"},
            "Documents":  {".pdf", ".docx", ".xlsx", ".pptx", ".txt", ".epub"},
            "Programs":   {".exe", ".msi", ".dmg", ".deb", ".rpm", ".AppImage", ".sh"},
            "Music":      {".mp3", ".flac", ".ogg", ".wav", ".aac"},
            "DiskImages": {".iso", ".img"},
        },
    }
}
```

### 9.3 Config Lifecycle

```go
func Load(path string) (*Config, error)   // Read JSON file, merge with defaults
func (c *Config) Save(path string) error   // Write JSON file (pretty-printed)
func (c *Config) Validate() error          // Check ranges, paths, port conflicts
```

**Load behavior:**
1. If file doesn't exist, create it with `DefaultConfig()` and save.
2. If file exists, unmarshal JSON over a copy of `DefaultConfig()` — this ensures new fields added in future versions get their defaults.
3. Call `Validate()` after loading.

**Validation rules:**
- `MaxConcurrent`: 1–10.
- `DefaultSegments`: 1–32.
- `ServerPort`: 1024–65535.
- `AuthToken`: non-empty, minimum 16 characters.
- `DownloadDir`: path exists and is writable (or can be created).
- `MinSegmentSize`: ≥ 64 KB.
- `MaxRetries`: 0–100.

### 9.4 Auth Token Generation

```go
func generateToken() string {
    b := make([]byte, 32)
    _, err := crypto_rand.Read(b)
    if err != nil {
        panic("crypto/rand unavailable")
    }
    return hex.EncodeToString(b) // 64-char hex string
}
```

Generated once on first run and persisted in `config.json`. The browser extension and CLI client must use this token.

---

## 10. HTTP Server & REST API

### 10.1 Server Setup

```go
// internal/server/server.go

type Server struct {
    engine *engine.Engine
    cfg    *config.Config
    bus    *event.Bus
    srv    *http.Server
}

func New(engine *engine.Engine, cfg *config.Config, bus *event.Bus) *Server

func (s *Server) Start(ctx context.Context) error {
    mux := http.NewServeMux()

    // Middleware chain: recovery → logging → CORS → auth
    handler := s.recovery(s.logging(s.cors(s.auth(mux))))

    // Routes (Go 1.22+ pattern syntax)
    mux.HandleFunc("POST /api/downloads", s.handleAddDownload)
    mux.HandleFunc("GET /api/downloads", s.handleListDownloads)
    mux.HandleFunc("GET /api/downloads/{id}", s.handleGetDownload)
    mux.HandleFunc("DELETE /api/downloads/{id}", s.handleDeleteDownload)
    mux.HandleFunc("POST /api/downloads/{id}/pause", s.handlePauseDownload)
    mux.HandleFunc("POST /api/downloads/{id}/resume", s.handleResumeDownload)
    mux.HandleFunc("POST /api/downloads/{id}/retry", s.handleRetryDownload)
    mux.HandleFunc("POST /api/downloads/{id}/refresh", s.handleRefreshURL)
    mux.HandleFunc("GET /api/config", s.handleGetConfig)
    mux.HandleFunc("PUT /api/config", s.handleUpdateConfig)
    mux.HandleFunc("GET /api/stats", s.handleGetStats)
    mux.HandleFunc("POST /api/probe", s.handleProbe)
    mux.HandleFunc("GET /ws", s.handleWebSocket) // exempt from auth via query param

    s.srv = &http.Server{
        Addr:         fmt.Sprintf("127.0.0.1:%d", cfg.ServerPort),
        Handler:      handler,
        ReadTimeout:  10 * time.Second,
        WriteTimeout: 30 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
    return s.srv.Shutdown(ctx)
}
```

### 10.2 Middleware

**CORS middleware:**

```go
func (s *Server) cors(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
        w.Header().Set("Access-Control-Max-Age", "86400")
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusNoContent)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**Auth middleware:**

```go
func (s *Server) auth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // WebSocket auth is via query parameter
        if r.URL.Path == "/ws" {
            token := r.URL.Query().Get("token")
            if token != s.cfg.AuthToken {
                http.Error(w, "unauthorized", http.StatusUnauthorized)
                return
            }
            next.ServeHTTP(w, r)
            return
        }

        auth := r.Header.Get("Authorization")
        if !strings.HasPrefix(auth, "Bearer ") {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        token := strings.TrimPrefix(auth, "Bearer ")
        if token != s.cfg.AuthToken {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**Logging middleware:**

Uses `slog` to log every request with method, path, status code, and duration.

**Recovery middleware:**

Catches panics in handlers, logs the stack trace, and returns `500 Internal Server Error`.

### 10.3 Request/Response Formats

**Add download request:**

```json
POST /api/downloads
{
    "url": "https://example.com/file.zip",
    "filename": "",
    "dir": "",
    "segments": 0,
    "headers": {
        "Cookie": "session=abc123",
        "Referer": "https://example.com/download-page",
        "User-Agent": "Mozilla/5.0..."
    },
    "referer_url": "https://example.com/download-page",
    "speed_limit": 0,
    "checksum": {
        "algorithm": "sha256",
        "value": "abc123..."
    }
}
```

Empty/zero fields use defaults from config.

**Add download response:**

```json
201 Created
{
    "id": "d_01JMQX...",
    "url": "https://cdn.example.com/file.zip",
    "filename": "file.zip",
    "dir": "/home/user/Downloads",
    "total_size": 104857600,
    "status": "queued",
    "segments": 16,
    "created_at": "2026-02-27T10:00:00Z"
}
```

**List downloads response:**

```json
200 OK
{
    "downloads": [...],
    "total": 42
}
```

**Error response (all endpoints):**

```json
{
    "error": "download not found",
    "code": "NOT_FOUND"
}
```

Error codes: `VALIDATION_ERROR`, `NOT_FOUND`, `CONFLICT`, `INTERNAL_ERROR`, `PROBE_FAILED`.

**Probe request/response:**

```json
POST /api/probe
{
    "url": "https://example.com/file.zip",
    "headers": {}
}

200 OK
{
    "filename": "file.zip",
    "total_size": 104857600,
    "accepts_ranges": true,
    "content_type": "application/zip"
}
```

**Stats response:**

```json
GET /api/stats
{
    "active_count": 2,
    "queued_count": 5,
    "completed_count": 42,
    "total_speed": 15728640,
    "version": "1.0.0"
}
```

### 10.4 Input Validation

All handlers validate input before processing:

| Field | Validation |
|-------|-----------|
| `url` | Non-empty, valid URL (http or https scheme), max 4096 chars |
| `filename` | No path separators (`/`, `\`), no null bytes, max 255 chars |
| `dir` | Absolute path, exists and writable |
| `segments` | 1–32 (0 means use default) |
| `speed_limit` | ≥ 0 (0 means unlimited) |
| `checksum.algorithm` | One of: `md5`, `sha1`, `sha256` |
| `checksum.value` | Hex string, correct length for algorithm |
| `id` path parameter | Valid ULID format with `d_` prefix |

---

## 11. WebSocket

### 11.1 Connection Lifecycle

```go
// internal/server/websocket.go

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
        OriginPatterns: []string{"*"}, // localhost, extension contexts
    })
    if err != nil {
        return
    }
    defer conn.Close(websocket.StatusNormalClosure, "")

    ctx := conn.CloseRead(r.Context())

    // Subscribe to event bus
    sub := s.bus.Subscribe()
    defer s.bus.Unsubscribe(sub)

    // Push loop
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            msg := s.buildProgressMessage()
            data, _ := json.Marshal(msg)
            err := conn.Write(ctx, websocket.MessageText, data)
            if err != nil {
                return
            }
        case evt := <-sub:
            // Immediate push for state change events
            data, _ := json.Marshal(evt)
            conn.Write(ctx, websocket.MessageText, data)
        }
    }
}
```

### 11.2 Message Types

```json
// Progress update (sent every 500ms while downloads are active)
{
    "type": "progress",
    "downloads": [
        {
            "id": "d_01JMQX...",
            "downloaded": 52428800,
            "speed": 5242880,
            "eta": 10,
            "status": "active",
            "segments": [
                {"index": 0, "downloaded": 6553600, "done": true},
                {"index": 1, "downloaded": 3276800, "done": false}
            ]
        }
    ]
}

// State change events (sent immediately)
{ "type": "download_added",     "download": { ... } }
{ "type": "download_completed", "download": { ... } }
{ "type": "download_failed",    "download": { ... } }
{ "type": "download_removed",   "id": "d_01JMQX..." }
{ "type": "download_paused",    "id": "d_01JMQX..." }
{ "type": "download_resumed",   "id": "d_01JMQX..." }
{ "type": "refresh_needed",     "download": { ... } }
```

### 11.3 Backpressure

If the WebSocket write buffer fills (slow client), messages are dropped silently. The next tick will send fresh data. This prevents a slow WebSocket client from blocking the event bus or other subscribers.

---

## 12. CLI

### 12.1 Argument Parsing

Use stdlib `flag` package or a minimal hand-rolled parser. No external CLI framework.

```go
// cmd/bolt/main.go

func main() {
    if len(os.Args) < 2 {
        launchGUI() // Mode 1
        return
    }

    switch os.Args[1] {
    case "gui":
        launchGUI()
    case "start":
        launchHeadless()
    case "stop":
        cli.Stop()
    case "add":
        cli.Add(os.Args[2:])
    case "list":
        cli.List(os.Args[2:])
    case "status":
        cli.Status(os.Args[2:])
    case "pause":
        cli.Pause(os.Args[2:])
    case "resume":
        cli.Resume(os.Args[2:])
    case "cancel":
        cli.Cancel(os.Args[2:])
    case "refresh":
        cli.Refresh(os.Args[2:])
    case "config":
        cli.Config(os.Args[2:])
    case "version":
        fmt.Println("bolt version 1.0.0")
    default:
        fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
        os.Exit(1)
    }
}
```

### 12.2 CLI Client

```go
// internal/cli/cli.go

type Client struct {
    baseURL string
    token   string
    http    *http.Client
}

func NewClient() (*Client, error) {
    cfg, err := config.Load(config.DefaultPath())
    if err != nil {
        return nil, err
    }
    return &Client{
        baseURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.ServerPort),
        token:   cfg.AuthToken,
        http:    &http.Client{Timeout: 10 * time.Second},
    }, nil
}
```

### 12.3 Daemon Detection

Before executing any command that requires the daemon, the CLI checks if it's running:

```go
func (c *Client) checkDaemon() error {
    resp, err := c.get("/api/stats")
    if err != nil {
        return fmt.Errorf("Bolt is not running. Start it with `bolt` or `bolt start`.")
    }
    resp.Body.Close()
    return nil
}
```

### 12.4 Output Formatting

**Default (human-readable):**

```
$ bolt list
ID              Filename              Size     Progress  Speed       Status
d_01JMQX...    ubuntu-24.04.iso      4.7 GB   47%       12.3 MB/s   Active
d_01JMQY...    node-v22.tar.gz       48 MB    100%      —           Completed
d_01JMQZ...    dataset.csv           1.2 GB   0%        —           Queued
```

Uses fixed-width columns with `text/tabwriter`. Filenames truncated to 20 characters with `...`.

**JSON mode (`--json`):**

```
$ bolt list --json
[{"id":"d_01JMQX...","filename":"ubuntu-24.04.iso",...}]
```

Raw JSON array, one line, suitable for `jq` piping.

### 12.5 `bolt add` Flags

```
Usage: bolt add <url> [flags]

Flags:
  --dir <path>           Save directory (default: from config)
  --filename <name>      Override filename
  --segments <n>         Segment count (default: from config)
  --speed-limit <rate>   Per-download speed limit (e.g., 5M, 500K)
  --checksum <algo:hash> Verify checksum after download (e.g., sha256:abc...)
  --header <key:value>   Add custom header (repeatable)
  --referer <url>        Set referer URL for link refresh
  --json                 Output as JSON
```

### 12.6 `bolt stop` Behavior

```go
func Stop() {
    client := NewClient()
    if err := client.checkDaemon(); err != nil {
        fmt.Println("Bolt is not running.")
        return
    }

    // Read PID file
    pid, err := pid.Read()
    if err != nil {
        fmt.Fprintln(os.Stderr, "Could not read PID file.")
        os.Exit(1)
    }

    // Send SIGTERM
    process, _ := os.FindProcess(pid)
    process.Signal(syscall.SIGTERM)

    // Wait up to 10 seconds for process to exit
    // (poll /api/stats until it fails)
}
```

---

## 13. Wails GUI Application

### 13.1 Wails Bindings

The `App` struct exposes Go methods to the Svelte frontend via Wails IPC:

```go
// internal/app/app.go

type App struct {
    ctx    context.Context
    engine *engine.Engine
    cfg    *config.Config
    bus    *event.Bus
}

// Wails lifecycle hooks
func (a *App) OnStartup(ctx context.Context)   // called by Wails on window creation
func (a *App) OnShutdown(ctx context.Context)   // called by Wails on window close

// Exposed to frontend (Wails auto-generates TypeScript bindings)
func (a *App) AddDownload(req engine.AddRequest) (*model.Download, error)
func (a *App) ListDownloads(status string, limit int, offset int) ([]model.Download, error)
func (a *App) GetDownload(id string) (*model.Download, []model.Segment, error)
func (a *App) PauseDownload(id string) error
func (a *App) ResumeDownload(id string) error
func (a *App) CancelDownload(id string, deleteFile bool) error
func (a *App) RetryDownload(id string) error
func (a *App) RefreshURL(id string, newURL string) error
func (a *App) Probe(url string, headers map[string]string) (*model.ProbeResult, error)
func (a *App) GetConfig() *config.Config
func (a *App) UpdateConfig(cfg config.Config) error
func (a *App) GetStats() Stats
func (a *App) PauseAll() error
func (a *App) ResumeAll() error
func (a *App) ClearCompleted() error
func (a *App) SelectDirectory() string  // opens OS directory picker dialog
func (a *App) OpenFile(path string) error
func (a *App) OpenFolder(path string) error
func (a *App) GetAuthToken() string
```

### 13.2 Wails Configuration

```json
// wails.json
{
    "name": "Bolt",
    "outputfilename": "bolt",
    "frontend:install": "pnpm install",
    "frontend:build": "pnpm build",
    "frontend:dev:watcher": "pnpm dev",
    "frontend:dev:serverUrl": "auto",
    "author": {
        "name": "Farhan Hasin Chowdhury"
    }
}
```

### 13.3 Event Emission to Frontend

Wails provides `runtime.EventsEmit` to push events from Go to the Svelte frontend:

```go
func (a *App) OnStartup(ctx context.Context) {
    a.ctx = ctx

    // Subscribe to engine events and forward to Wails
    sub := a.bus.Subscribe()
    go func() {
        for evt := range sub {
            switch e := evt.(type) {
            case event.Progress:
                runtime.EventsEmit(ctx, "progress", e)
            case event.DownloadAdded:
                runtime.EventsEmit(ctx, "download:added", e)
            case event.DownloadCompleted:
                runtime.EventsEmit(ctx, "download:completed", e)
                // Trigger OS notification
                if a.cfg.SoundOnComplete {
                    beeep.Notify("Bolt", fmt.Sprintf("%s completed", e.Filename), "")
                }
            case event.DownloadFailed:
                runtime.EventsEmit(ctx, "download:failed", e)
            case event.DownloadRemoved:
                runtime.EventsEmit(ctx, "download:removed", e)
            case event.RefreshNeeded:
                runtime.EventsEmit(ctx, "download:refresh", e)
            }
        }
    }()
}
```

### 13.4 System Tray

Wails v2 supports system tray natively:

```go
func launchGUI() {
    app := &app.App{}
    // ... engine, server setup ...

    err := wails.Run(&options.App{
        Title:            "Bolt",
        Width:            1024,
        Height:           680,
        MinWidth:         800,
        MinHeight:        500,
        StartHidden:      false,
        HideWindowOnClose: app.cfg.MinimizeToTray,
        Bind:             []interface{}{app},
        OnStartup:        app.OnStartup,
        OnShutdown:       app.OnShutdown,

        // System tray
        SystemTray: &options.SystemTray{
            LightModeIcon: iconData,
            DarkModeIcon:  iconData,
            Menu: menu.NewMenuFromItems(
                menu.Text("Show/Hide", nil, func(_ *menu.CallbackData) {
                    runtime.WindowToggle(app.ctx)
                }),
                menu.Separator(),
                menu.Text("Pause All", nil, func(_ *menu.CallbackData) {
                    app.PauseAll()
                }),
                menu.Text("Resume All", nil, func(_ *menu.CallbackData) {
                    app.ResumeAll()
                }),
                menu.Separator(),
                menu.Text("Quit", nil, func(_ *menu.CallbackData) {
                    runtime.Quit(app.ctx)
                }),
            ),
            OnClick: func() {
                runtime.WindowToggle(app.ctx)
            },
        },
    })
}
```

### 13.5 Startup Sequence (GUI Mode)

```
1. Parse CLI args → detect GUI mode
2. Load config.json (create with defaults if missing)
3. Check PID file → if daemon already running, bring existing window to front and exit
4. Write PID file
5. Open SQLite database, run migrations
6. Create Engine (resumes interrupted downloads from DB)
7. Create Queue Manager, start its run loop
8. Create HTTP Server, start listening on configured port
9. Create Wails App, bind methods
10. Launch Wails window + system tray
11. Engine.Start() → resume paused/active downloads from DB
```

### 13.6 Startup Sequence (Headless Mode)

```
1. Parse CLI args → detect headless mode
2. Load config.json
3. Check PID file → if daemon already running, error and exit
4. Write PID file
5. Open SQLite database, run migrations
6. Create Engine
7. Create Queue Manager, start its run loop
8. Create HTTP Server, start listening
9. Engine.Start() → resume downloads
10. Block on signal (SIGTERM, SIGINT) → Shutdown()
```

---

## 14. Svelte Frontend

### 14.1 Technology Stack

| Tool | Version | Purpose |
|------|---------|---------|
| Svelte | 5.x | UI framework |
| TypeScript | 5.x | Type safety |
| Vite | 6.x | Build tool (Wails default) |
| Tailwind CSS | 4.x | Styling |
| Wails Runtime | (bundled) | IPC calls and event listeners |

### 14.2 State Management

Use Svelte 5 runes (`$state`, `$derived`, `$effect`) for reactive state. No external state management library.

```typescript
// frontend/src/lib/stores/downloads.ts

import { EventsOn } from '../../wailsjs/runtime/runtime';
import { ListDownloads } from '../../wailsjs/go/app/App';

// Reactive download list
let downloads = $state<Download[]>([]);
let activeDownloads = $derived(downloads.filter(d => d.status === 'active'));
let totalSpeed = $derived(activeDownloads.reduce((sum, d) => sum + d.speed, 0));

// Initial load
export async function loadDownloads() {
    downloads = await ListDownloads('', 100, 0);
}

// Live updates from Go backend
EventsOn('progress', (data: ProgressUpdate) => {
    for (const update of data.downloads) {
        const idx = downloads.findIndex(d => d.id === update.id);
        if (idx >= 0) {
            downloads[idx].downloaded = update.downloaded;
            downloads[idx].speed = update.speed;
            downloads[idx].eta = update.eta;
            downloads[idx].status = update.status;
        }
    }
});

EventsOn('download:added', (download: Download) => {
    downloads = [download, ...downloads];
});

EventsOn('download:completed', (download: Download) => {
    const idx = downloads.findIndex(d => d.id === download.id);
    if (idx >= 0) downloads[idx] = download;
});

EventsOn('download:removed', (id: string) => {
    downloads = downloads.filter(d => d.id !== id);
});
```

### 14.3 Component Hierarchy

```
App.svelte
├── Toolbar.svelte              # Add, Pause All, Resume All, Clear, Settings
├── SearchBar.svelte            # Filter downloads by filename/URL
├── DownloadList.svelte         # Main content area
│   └── DownloadRow.svelte      # Single download row (repeated)
│       ├── ProgressBar.svelte  # Multi-segment progress visualization
│       └── ActionButtons.svelte # Pause/Resume/Cancel/etc.
├── AddDownloadDialog.svelte    # Modal dialog for adding downloads
├── SettingsDialog.svelte       # Modal dialog for settings
├── ContextMenu.svelte          # Right-click context menu
└── StatusBar.svelte            # Bottom bar: active count, total speed, disk space
```

### 14.4 Key Components

**DownloadRow.svelte:**

Displays one row in the download list. Shows:
- File icon (based on extension).
- Filename (truncated, full name in tooltip).
- Size (formatted: "4.7 GB").
- Progress bar (percentage + visual bar).
- Speed (formatted: "12.3 MB/s" or "—" if not active).
- ETA (formatted: "2m 30s" or "—").
- Status badge (color-coded: green=completed, blue=active, yellow=queued, red=error, gray=paused).
- Action buttons (contextual: Pause when active, Resume when paused, Retry when error).

**ProgressBar.svelte:**

Two modes:
1. **Simple mode:** Single bar showing overall percentage.
2. **Segment mode:** Mini multi-bar showing each segment's progress. Each segment is a thin colored band within the overall bar.

Segment mode activates when the user hovers or clicks the progress bar.

**AddDownloadDialog.svelte:**

Sequence when opened:
1. If URL is pre-filled (from extension or clipboard), auto-run probe.
2. Show loading spinner during probe.
3. On probe success: populate filename, size, show "Ready to download".
4. On probe failure: show error inline, allow user to proceed anyway.
5. User can modify filename, save directory, segment count, speed limit.
6. "Download" button submits to `App.AddDownload()`.

### 14.5 Formatting Utilities

```typescript
// frontend/src/lib/utils/format.ts

export function formatBytes(bytes: number): string
// 0 → "0 B", 1024 → "1.0 KB", 1048576 → "1.0 MB", etc.

export function formatSpeed(bytesPerSec: number): string
// 0 → "—", 5242880 → "5.0 MB/s"

export function formatETA(seconds: number): string
// -1 → "—", 90 → "1m 30s", 3661 → "1h 1m"

export function formatDate(iso: string): string
// "2026-02-27T10:00:00Z" → "Feb 27, 2026 10:00 AM"

export function truncateFilename(name: string, maxLen: number): string
// "very-long-filename-here.iso" → "very-long-filen...here.iso"
// Preserves extension visibility by truncating the middle
```

### 14.6 Keyboard Shortcuts

Handled in `App.svelte` via `svelte:window` event listener:

```svelte
<svelte:window on:keydown={handleKeydown} />

<script>
function handleKeydown(e: KeyboardEvent) {
    if (e.ctrlKey && e.key === 'n') { showAddDialog = true; e.preventDefault(); }
    if (e.ctrlKey && e.key === 'v') { addFromClipboard(); e.preventDefault(); }
    if (e.key === 'Delete') { removeSelected(); }
    if (e.key === ' ' && selectedIds.length) { togglePauseSelected(); e.preventDefault(); }
    if (e.ctrlKey && e.key === 'a') { selectAll(); e.preventDefault(); }
    if (e.ctrlKey && e.key === 'q') { quit(); }
}
</script>
```

### 14.7 Theme Support

Tailwind CSS with `darkMode: 'class'` strategy:

```typescript
// frontend/src/lib/stores/theme.ts

function applyTheme(theme: 'light' | 'dark' | 'system') {
    if (theme === 'system') {
        const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
        document.documentElement.classList.toggle('dark', prefersDark);
    } else {
        document.documentElement.classList.toggle('dark', theme === 'dark');
    }
}
```

System theme changes are detected via `matchMedia` listener and applied in real time.

---

## 15. Browser Extension

### 15.1 Manifest

```json
{
    "manifest_version": 3,
    "name": "Bolt Capture",
    "version": "1.0.0",
    "description": "Capture browser downloads and send them to Bolt download manager",
    "permissions": [
        "downloads",
        "cookies",
        "contextMenus",
        "storage",
        "notifications"
    ],
    "host_permissions": [
        "http://127.0.0.1:*/*",
        "http://localhost:*/*",
        "<all_urls>"
    ],
    "background": {
        "service_worker": "background.js"
    },
    "action": {
        "default_popup": "popup/popup.html",
        "default_icon": {
            "16": "icons/icon-16.png",
            "48": "icons/icon-48.png",
            "128": "icons/icon-128.png"
        }
    },
    "options_ui": {
        "page": "options/options.html",
        "open_in_tab": true
    },
    "icons": {
        "16": "icons/icon-16.png",
        "48": "icons/icon-48.png",
        "128": "icons/icon-128.png"
    }
}
```

**Permission rationale:**

| Permission | Reason |
|------------|--------|
| `downloads` | Intercept and cancel browser-initiated downloads |
| `cookies` | Read cookies for the download URL's domain (forwarded to Bolt) |
| `contextMenus` | "Download with Bolt" context menu on links |
| `storage` | Store extension settings (server URL, token, filters) |
| `notifications` | Show notifications when Bolt is unreachable |
| `<all_urls>` host permission | Required to read cookies from any domain |

### 15.2 Service Worker (background.js)

```javascript
// extension/background.js

// -- Config --
const DEFAULT_SERVER = 'http://127.0.0.1:6800';

async function getConfig() {
    const data = await chrome.storage.local.get({
        serverUrl: DEFAULT_SERVER,
        authToken: '',
        captureEnabled: true,
        minFileSize: 2 * 1024 * 1024, // 2 MB
        extensionWhitelist: [],        // empty = capture all
        extensionBlacklist: ['.html', '.htm', '.json', '.xml', '.js', '.css'],
        domainBlocklist: ['localhost', '127.0.0.1'],
    });
    return data;
}

// -- Download Interception --
chrome.downloads.onCreated.addListener(async (downloadItem) => {
    const config = await getConfig();
    if (!config.captureEnabled) return;
    if (!shouldCapture(downloadItem, config)) return;

    // Cancel the browser download
    chrome.downloads.cancel(downloadItem.id);
    chrome.downloads.erase({ id: downloadItem.id });

    // Extract headers
    const url = downloadItem.finalUrl || downloadItem.url;
    const cookies = await getCookiesForUrl(url);
    const referer = downloadItem.referrer || '';

    // Send to Bolt
    try {
        await sendToBolt(config, {
            url: url,
            filename: downloadItem.filename.split('/').pop(),
            headers: {
                'Cookie': cookies,
                'Referer': referer,
                'User-Agent': navigator.userAgent,
            },
            referer_url: referer,
        });
    } catch (err) {
        // Bolt unreachable — notify user, don't lose the download
        chrome.notifications.create({
            type: 'basic',
            iconUrl: 'icons/icon-128.png',
            title: 'Bolt Capture',
            message: `Could not send download to Bolt: ${err.message}. The download was cancelled.`,
        });
    }
});

function shouldCapture(item, config) {
    // Check domain blocklist
    try {
        const hostname = new URL(item.finalUrl || item.url).hostname;
        if (config.domainBlocklist.some(d => hostname.includes(d))) return false;
    } catch { return false; }

    // Check file extension blacklist
    const filename = item.filename || '';
    const ext = '.' + filename.split('.').pop().toLowerCase();
    if (config.extensionBlacklist.includes(ext)) return false;

    // Check file extension whitelist (if non-empty, only capture matching)
    if (config.extensionWhitelist.length > 0) {
        if (!config.extensionWhitelist.includes(ext)) return false;
    }

    // Note: file size check happens server-side (Bolt probes the URL)
    // because Content-Length may not be available to the extension at this point

    return true;
}

async function getCookiesForUrl(url) {
    const cookies = await chrome.cookies.getAll({ url });
    return cookies.map(c => `${c.name}=${c.value}`).join('; ');
}

async function sendToBolt(config, request) {
    const resp = await fetch(`${config.serverUrl}/api/downloads`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${config.authToken}`,
        },
        body: JSON.stringify(request),
    });
    if (!resp.ok) {
        const body = await resp.json().catch(() => ({}));
        throw new Error(body.error || `HTTP ${resp.status}`);
    }
    return resp.json();
}

// -- Context Menu --
chrome.contextMenus.create({
    id: 'download-with-bolt',
    title: 'Download with Bolt',
    contexts: ['link'],
});

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
    if (info.menuItemId !== 'download-with-bolt') return;

    const config = await getConfig();
    const url = info.linkUrl;
    const cookies = await getCookiesForUrl(url);

    try {
        await sendToBolt(config, {
            url: url,
            headers: {
                'Cookie': cookies,
                'Referer': info.pageUrl || '',
                'User-Agent': navigator.userAgent,
            },
            referer_url: info.pageUrl || '',
        });
    } catch (err) {
        chrome.notifications.create({
            type: 'basic',
            iconUrl: 'icons/icon-128.png',
            title: 'Bolt Capture',
            message: `Failed: ${err.message}`,
        });
    }
});
```

### 15.3 Extension Popup

**Layout (400x300):**

```
┌──────────────────────────────────────┐
│ ● Connected to Bolt        [⏸ Toggle] │
├──────────────────────────────────────┤
│ ▼ Active Downloads                    │
│                                       │
│ 📄 ubuntu-24.04.iso                   │
│ ████████░░░░░░░░  47%  12.3 MB/s      │
│                                       │
│ 📄 node-v22.tar.gz                    │
│ ██████████████████ 100%  Done          │
│                                       │
│ 📄 dataset.csv                        │
│ ░░░░░░░░░░░░░░░░  0%   Queued         │
│                                       │
├──────────────────────────────────────┤
│ [Open Bolt]                   [⚙ Settings] │
└──────────────────────────────────────┘
```

**Implementation:**

- Popup opens → connect to `ws://127.0.0.1:6800/ws?token=...` for live progress.
- On close → WebSocket disconnects (intentional, MV3 limitation).
- "Toggle" button → enables/disables download capture.
- Click a download → sends message to open Bolt GUI window (via `sendToBolt` or system command).
- Connection status: green dot = reachable, red dot = unreachable (checked via fetch to `/api/stats`).

### 15.4 Link Refresh (Tier 2) in Extension

```javascript
// In background.js — refresh matching logic

async function checkForRefreshCandidate(url, filename) {
    const config = await getConfig();
    const resp = await fetch(`${config.serverUrl}/api/downloads?status=refresh`, {
        headers: { 'Authorization': `Bearer ${config.authToken}` },
    });
    const data = await resp.json();

    // Match by filename or domain + similar path
    return data.downloads.find(d =>
        d.filename === filename ||
        (new URL(d.url).hostname === new URL(url).hostname &&
         d.status === 'refresh')
    );
}

// Modified download interception: check for refresh before creating new download
chrome.downloads.onCreated.addListener(async (downloadItem) => {
    // ... existing logic ...

    const filename = downloadItem.filename.split('/').pop();
    const refreshCandidate = await checkForRefreshCandidate(url, filename);

    if (refreshCandidate) {
        // This is a link refresh, not a new download
        await fetch(`${config.serverUrl}/api/downloads/${refreshCandidate.id}/refresh`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${config.authToken}`,
            },
            body: JSON.stringify({
                url: url,
                headers: {
                    'Cookie': cookies,
                    'Referer': referer,
                    'User-Agent': navigator.userAgent,
                },
            }),
        });
        return;
    }

    // ... proceed with normal download capture ...
});
```

---

## 16. Concurrency Model

### 16.1 Goroutine Map

```
main goroutine
├── HTTP server (net/http — managed internally)
│   ├── handler goroutine per request
│   └── WebSocket goroutine per connection
├── Queue manager (1 goroutine — Run loop)
├── Event bus dispatcher (1 goroutine)
├── Wails runtime (managed by Wails)
│   └── Event forwarding goroutine
└── Per active download:
    ├── Progress aggregator (1 goroutine)
    └── Segment workers (N goroutines, N = segment count)
```

**Typical goroutine count with 3 active downloads at 16 segments:**
- Base: ~5 (main, server, queue, event bus, Wails)
- HTTP server: ~2–5 (idle connections)
- Per download: 17 (16 segments + 1 aggregator)
- Total: ~60 goroutines

### 16.2 Channel Usage

| Channel | Type | Buffer | Purpose |
|---------|------|--------|---------|
| `segmentReport` | Per download | 64 | Segment → progress aggregator |
| `event.Bus` subscriber | Per subscriber | 256 | Event bus → WebSocket/Wails |
| `queue.notify` | Singleton | 1 | Signal queue re-evaluation |

**Buffer sizing rationale:**
- `segmentReport`: 64 = 4× the max segment count (16). Prevents segment goroutines from blocking on report while aggregator is busy computing.
- Event bus subscribers: 256 = generous buffer for slow consumers. If full, messages are dropped (see WebSocket backpressure).
- `queue.notify`: 1 = only need to know "something changed", not what.

### 16.3 Context Hierarchy

```
main context (cancelled on shutdown signal)
├── HTTP server context
├── Queue manager context
├── Event bus context
└── Per-download context (cancelled on pause/cancel/shutdown)
    └── Per-segment context (inherits from download context)
```

Cancelling a download's context immediately signals all its segment goroutines to stop. They check `ctx.Done()` in their read loop and exit cleanly.

### 16.4 Shared State & Synchronization

| Shared State | Protected By | Accessed By |
|-------------|-------------|-------------|
| `Engine.active` map | `Engine.mu` (sync.Mutex) | Handlers, queue, shutdown |
| `Queue.queue` slice | `Queue.mu` (sync.Mutex) | Queue evaluate, enqueue, dequeue |
| `progressAggregator.speeds` | `progressAggregator.mu` | Aggregator goroutine, progress reads |
| `Config` struct | Immutable after load (copy on update) | All components |
| `*os.File` per download | No mutex (non-overlapping WriteAt) | Segment goroutines |
| SQLite | Single connection (serialized) | All DB operations |

---

## 17. Error Handling

### 17.1 Error Types

```go
// internal/model/errors.go

var (
    ErrNotFound           = errors.New("download not found")
    ErrAlreadyActive      = errors.New("download is already active")
    ErrAlreadyPaused      = errors.New("download is already paused")
    ErrAlreadyCompleted   = errors.New("download is already completed")
    ErrInvalidURL         = errors.New("invalid URL")
    ErrInvalidSegments    = errors.New("segment count must be 1-32")
    ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
    ErrURLExpired         = errors.New("download URL has expired")
    ErrSizeMismatch       = errors.New("file size mismatch on URL refresh")
    ErrDaemonNotRunning   = errors.New("bolt daemon is not running")
    ErrDaemonAlreadyRunning = errors.New("bolt daemon is already running")
    ErrProbeRejected      = errors.New("HEAD request rejected by server")
    ErrDuplicateURL       = errors.New("URL is already queued or active")
)
```

### 17.2 Error Propagation

```
Segment error → segmentReport.Err
    → Progress aggregator inspects error type
        → Transient: retry logic handles it (no propagation)
        → Permanent: download marked as "error" in DB
            → Event emitted → GUI shows error state
            → User action: retry (resets error, re-enqueues)

HTTP handler error → JSON error response
    → { "error": "message", "code": "CODE" }
    → HTTP status: 400 (validation), 404 (not found), 409 (conflict), 500 (internal)

Probe error → returned to caller
    → GUI: shows inline error in Add Download dialog
    → API: 400 with PROBE_FAILED code
```

### 17.3 Logging Strategy

All components use `slog` with structured fields:

```go
slog.Info("download started",
    "download_id", d.ID,
    "url", d.URL,
    "segments", d.SegmentCount,
)

slog.Error("segment failed",
    "download_id", d.ID,
    "segment", seg.Index,
    "attempt", attempt,
    "error", err,
)
```

**Log levels:**
- `DEBUG`: Segment-level operations, HTTP requests/responses, DB queries.
- `INFO`: Download lifecycle events, server start/stop, config changes.
- `WARN`: Retries, URL refresh attempts, non-critical failures.
- `ERROR`: Failed downloads, unrecoverable errors, panics caught by recovery middleware.

**Output:**
- GUI mode: logs to `~/.config/bolt/bolt.log` (rotated at 10 MB, keep 3 files).
- Headless mode: logs to stderr (systemd journal captures this).
- Log level configurable: default `INFO`, `--debug` flag enables `DEBUG`.

---

## 18. Security

### 18.1 Threat Model

Bolt's HTTP server listens on `127.0.0.1` only. The primary threat is a malicious website making requests to `http://127.0.0.1:6800/api/...` from JavaScript (DNS rebinding or localhost attacks).

**Mitigations:**

| Threat | Mitigation |
|--------|-----------|
| Unauthorized API access | Bearer token on all endpoints |
| DNS rebinding | Check `Host` header — reject if not `127.0.0.1` or `localhost` |
| CSRF via browser fetch | Bearer token (not cookie-based auth, so CSRF is not applicable) |
| Path traversal in `dir` parameter | Validate `dir` is an absolute path, sanitize, ensure it resolves within allowed directories |
| Path traversal in `filename` | Strip `/`, `\`, `..`, null bytes; reject if filename differs after sanitization |
| Argument injection via URLs | URLs are passed to `http.NewRequest`, not shell commands; no shell execution with user input |
| Extension token theft | Token stored in `chrome.storage.local` (not accessible to web pages) |

### 18.2 Host Header Validation

```go
func (s *Server) validateHost(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        host := r.Host
        if h, _, err := net.SplitHostPort(host); err == nil {
            host = h
        }
        if host != "127.0.0.1" && host != "localhost" && host != "::1" {
            http.Error(w, "forbidden", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

### 18.3 File Path Sanitization

```go
func sanitizeFilename(name string) string {
    // Remove path separators
    name = strings.ReplaceAll(name, "/", "_")
    name = strings.ReplaceAll(name, "\\", "_")
    name = strings.ReplaceAll(name, "\x00", "")

    // Remove leading dots (hidden files on Unix)
    name = strings.TrimLeft(name, ".")

    // Limit length
    if len(name) > 255 {
        ext := filepath.Ext(name)
        name = name[:255-len(ext)] + ext
    }

    if name == "" {
        name = "download"
    }
    return name
}

func validateDir(dir string) error {
    if !filepath.IsAbs(dir) {
        return errors.New("directory must be an absolute path")
    }
    cleaned := filepath.Clean(dir)
    info, err := os.Stat(cleaned)
    if err != nil {
        return fmt.Errorf("directory does not exist: %s", cleaned)
    }
    if !info.IsDir() {
        return fmt.Errorf("not a directory: %s", cleaned)
    }
    // Test writability
    testFile := filepath.Join(cleaned, ".bolt_write_test")
    f, err := os.Create(testFile)
    if err != nil {
        return fmt.Errorf("directory is not writable: %s", cleaned)
    }
    f.Close()
    os.Remove(testFile)
    return nil
}
```

---

## 19. Testing Strategy

### 19.1 Test Categories

| Category | Location | Framework | Description |
|----------|----------|-----------|-------------|
| Unit tests | `*_test.go` next to source | `testing` | Test individual functions (filename detection, progress calculation, config validation) |
| Integration tests | `internal/engine/*_test.go` | `testing` + `httptest` | Test engine with a real SQLite DB and mock HTTP server |
| API tests | `internal/server/*_test.go` | `testing` + `httptest` | Test REST endpoints end-to-end |
| E2E tests | `tests/e2e_test.go` | `testing` | Full pipeline: add download via API → verify file on disk |

### 19.2 Mock HTTP Server

For testing the download engine, use `httptest.Server` that serves test files with configurable behavior:

```go
// testdata/testserver.go

type TestServer struct {
    *httptest.Server
    fileData       []byte
    supportsRanges bool
    latency        time.Duration
    failAfterBytes int64 // simulate mid-download failure
    statusOverride int   // force specific status code
}

func NewTestServer(size int64, opts ...Option) *TestServer
```

**Test scenarios:**

| Scenario | Test server config |
|----------|-------------------|
| Normal segmented download | 10 MB file, ranges supported |
| Single-connection fallback | 1 MB file, ranges not supported |
| Resume after pause | Download 50%, pause, resume, verify completion |
| Resume after crash | Download 50%, kill engine, restart, verify completion |
| Server error mid-download | Fail after 30% with 500, verify retry |
| URL expiry mid-download | Return 403 after 50%, verify refresh attempt |
| Small file (below threshold) | 500 KB file, verify single connection |
| Content-Disposition filename | Custom filename header |
| Redirect chain | 3 redirects before final URL |
| No Content-Length | Unknown size, verify single-connection download |

### 19.3 Test Commands

```makefile
# Run all tests
make test

# Run with race detector
make test-race

# Run specific package
go test ./internal/engine/... -v

# Run integration tests (tagged)
go test -tags=integration ./...

# Run E2E tests
go test -tags=e2e ./tests/...

# Coverage report
make coverage
```

### 19.4 Test Data

- `testdata/` contains small test files (1 KB, 1 MB) for unit tests.
- Integration tests generate test data in memory via `TestServer`.
- No large files committed to the repository.

---

## 20. Build & Distribution

### 20.1 Makefile

```makefile
.PHONY: dev build test lint clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -X main.version=$(VERSION)

# Development
dev:
	wails dev

# Production build (Linux only)
build:
	cd frontend && pnpm install && pnpm build
	CGO_ENABLED=1 go build -tags desktop,production,webkit2_41 -ldflags "$(LDFLAGS)" -o bolt ./cmd/bolt/

# Extension
build-extension:
	cd extensions/chrome && zip -r ../../dist/bolt-capture-chrome.zip . -x ".*"
	cd extensions/firefox && zip -r ../../dist/bolt-capture-firefox.zip . -x ".*"

# Testing
test:
	go test ./... -count=1

test-race:
	go test -race ./... -count=1

test-integration:
	go test -tags=integration ./... -count=1 -timeout 120s

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# Linting
lint:
	go vet ./...
	staticcheck ./...

# Clean
clean:
	rm -rf build/bin dist coverage.out coverage.html

# Frontend
frontend-install:
	cd frontend && pnpm install

frontend-lint:
	cd frontend && pnpm lint

# Full CI pipeline
ci: lint test-race test-integration build
```

### 20.2 Build Artifacts

```
dist/
├── bolt                           # Linux binary
├── bolt-capture-chrome.zip        # Chrome browser extension
└── bolt-capture-firefox.zip       # Firefox browser extension
```

### 20.3 Version Injection

```go
// cmd/bolt/main.go
var version = "dev" // overridden by -ldflags at build time
```

Used in:
- `bolt version` CLI output.
- `GET /api/stats` response.
- GUI window title: "Bolt v1.0.0".

### 20.4 .gitignore

```
# Build output
build/bin/
dist/
coverage.out
coverage.html

# Frontend
frontend/node_modules/
frontend/dist/

# IDE
.idea/
.vscode/
*.swp

# OS
.DS_Store
Thumbs.db

# Config (local dev)
bolt.db
bolt.pid
```

---

## 21. Systemd Integration

### 21.1 Service File

```ini
# bolt.service — install to ~/.config/systemd/user/bolt.service

[Unit]
Description=Bolt Download Manager (headless daemon)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%h/.local/bin/bolt start
ExecStop=/bin/kill -TERM $MAINPID
Restart=on-failure
RestartSec=5

# Graceful shutdown: give Bolt 15 seconds to persist state
TimeoutStopSec=15

# Logging goes to systemd journal
StandardOutput=journal
StandardError=journal
SyslogIdentifier=bolt

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=%h/Downloads %h/.config/bolt
PrivateTmp=yes

[Install]
WantedBy=default.target
```

### 21.2 Signal Handling (Headless Mode)

```go
func launchHeadless() {
    // ... engine, server setup ...

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

    go server.Start(ctx)
    go engine.Start(ctx)

    sig := <-sigCh
    slog.Info("received signal, shutting down", "signal", sig)

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    server.Shutdown(shutdownCtx)
    engine.Shutdown(shutdownCtx)
    slog.Info("shutdown complete")
}
```

### 21.3 Usage

```bash
# Install service
cp bolt.service ~/.config/systemd/user/
systemctl --user daemon-reload

# Start/stop
systemctl --user start bolt
systemctl --user stop bolt

# Enable on login
systemctl --user enable bolt

# View logs
journalctl --user -u bolt -f
```

---

## 22. Appendix

### 22.1 PID File Management

```go
// internal/pid/pid.go

func Path() string {
    return filepath.Join(config.Dir(), "bolt.pid")
}

func Write() error {
    return os.WriteFile(Path(), []byte(strconv.Itoa(os.Getpid())), 0644)
}

func Read() (int, error) {
    data, err := os.ReadFile(Path())
    if err != nil { return 0, err }
    return strconv.Atoi(strings.TrimSpace(string(data)))
}

func IsRunning() bool {
    pid, err := Read()
    if err != nil { return false }
    process, err := os.FindProcess(pid)
    if err != nil { return false }
    // On Unix, FindProcess always succeeds. Check if process exists.
    err = process.Signal(syscall.Signal(0))
    return err == nil
}

func Remove() {
    os.Remove(Path())
}
```

### 22.2 Event Bus

```go
// internal/event/event.go

type Event interface{}

type Progress struct {
    Downloads []model.ProgressUpdate `json:"downloads"`
}

type DownloadAdded struct {
    Download model.Download `json:"download"`
}

type DownloadCompleted struct {
    Download model.Download `json:"download"`
}

type DownloadFailed struct {
    Download model.Download `json:"download"`
}

type DownloadRemoved struct {
    ID string `json:"id"`
}

type RefreshNeeded struct {
    Download model.Download `json:"download"`
}

type Bus struct {
    mu          sync.RWMutex
    subscribers map[int]chan Event
    nextID      int
}

func New() *Bus
func (b *Bus) Subscribe() chan Event          // returns buffered channel (256)
func (b *Bus) Unsubscribe(ch chan Event)
func (b *Bus) Publish(evt Event)              // non-blocking send to all subscribers
```

**`Publish` behavior:**

```go
func (b *Bus) Publish(evt Event) {
    b.mu.RLock()
    defer b.mu.RUnlock()

    for _, ch := range b.subscribers {
        select {
        case ch <- evt:
        default:
            // Subscriber buffer full — drop message (backpressure)
        }
    }
}
```

### 22.3 ULID Generation

```go
// internal/model/id.go

import "github.com/oklog/ulid/v2"

var entropy = ulid.Monotonic(crypto_rand.Reader, 0)

func NewDownloadID() string {
    id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy)
    return "d_" + id.String()
}
```

The `d_` prefix makes IDs visually distinguishable and is stripped when parsing. ULIDs are 26 characters (Crockford Base32), lexicographically sortable by creation time.

### 22.4 Human-Readable Byte Sizes

```go
func FormatBytes(b int64) string {
    const unit = 1024
    if b < unit { return fmt.Sprintf("%d B", b) }
    div, exp := int64(unit), 0
    for n := b / unit; n >= unit; n /= unit {
        div *= unit
        exp++
    }
    return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
```

### 22.5 Rate Parsing (CLI Speed Limits)

```go
// Parse "5M", "500K", "1G" etc. to bytes/sec
func ParseRate(s string) (int64, error) {
    s = strings.TrimSpace(s)
    if s == "" || s == "0" { return 0, nil }

    multiplier := int64(1)
    suffix := strings.ToUpper(s[len(s)-1:])
    switch suffix {
    case "K":
        multiplier = 1024
        s = s[:len(s)-1]
    case "M":
        multiplier = 1024 * 1024
        s = s[:len(s)-1]
    case "G":
        multiplier = 1024 * 1024 * 1024
        s = s[:len(s)-1]
    }

    val, err := strconv.ParseFloat(s, 64)
    if err != nil { return 0, err }
    return int64(val * float64(multiplier)), nil
}
```

### 22.6 Checksum Verification

Run after download completion (all segments done):

```go
func VerifyChecksum(filepath string, algo string, expected string) (bool, error) {
    f, err := os.Open(filepath)
    if err != nil { return false, err }
    defer f.Close()

    var h hash.Hash
    switch algo {
    case "md5":
        h = md5.New()
    case "sha1":
        h = sha1.New()
    case "sha256":
        h = sha256.New()
    default:
        return false, fmt.Errorf("unsupported algorithm: %s", algo)
    }

    if _, err := io.Copy(h, f); err != nil {
        return false, err
    }

    actual := hex.EncodeToString(h.Sum(nil))
    return strings.EqualFold(actual, expected), nil
}
```

### 22.7 Constants

```go
const (
    DefaultPort           = 6800
    DefaultMaxConcurrent  = 3
    DefaultSegments       = 16
    DefaultMinSegmentSize = 1 * 1024 * 1024  // 1 MB
    DefaultMaxRetries     = 10
    MaxSegments           = 32
    MaxRedirects          = 10

    ProgressInterval      = 500 * time.Millisecond
    PersistInterval       = 2 * time.Second
    ShutdownTimeout       = 10 * time.Second
    ReadBufferSize        = 32 * 1024  // 32 KB
    SpeedWindowSize       = 5          // rolling average samples

    WSBufferSize          = 256        // event bus subscriber buffer
    SegmentReportBuffer   = 64         // segment → aggregator channel buffer
)
```

### 22.8 Implementation Phase Mapping

Cross-reference between this TRD and the PRD's implementation phases:

| Phase | PRD Section | TRD Sections |
|-------|-------------|-------------|
| Phase 1 — Engine + CLI | PRD §12, Phase 1 | §4 (Engine), §5 (Queue), §8 (Database), §9 (Config), §12 (CLI) |
| Phase 2 — Server & Queue | PRD §12, Phase 2 | §10 (HTTP Server), §11 (WebSocket), §7 (Dead Link Refresh Tier 1) |
| Phase 3 — GUI Core | PRD §12, Phase 3 | §13 (Wails App), §14 (Svelte Frontend) |
| Phase 4 — Extension | PRD §12, Phase 4 | §15 (Browser Extension), §7 (Dead Link Refresh Tier 2+3) |
| Phase 5 — P1 Features | PRD §12, Phase 5 | §6 (Speed Limiter), §14.6 (Keyboard Shortcuts), §14.7 (Theme) |

---

*End of TRD. This document, alongside the PRD, serves as the complete technical blueprint for Bolt.*
