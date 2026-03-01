# Bolt — Download Manager

Fast, segmented download manager built with Go. See `bolt-prd.md` and `bolt-trd.md` for full specs.

## Project Info

- **Module:** `github.com/fhsinchy/bolt`
- **Go version:** 1.23+
- **Author:** github.com/fhsinchy
- **SQLite driver:** `modernc.org/sqlite` (pure Go, no CGO)
- **ULID library:** `github.com/oklog/ulid/v2`
- **WebSocket:** `nhooyr.io/websocket`
- **Wails:** `github.com/wailsapp/wails/v2` (desktop GUI)
- **System tray:** `github.com/energye/systray`
- **Frontend:** Svelte 5, TypeScript 5, Vite 6, Tailwind CSS 4, pnpm
- **Test framework:** stdlib `testing` + `net/http/httptest` (no external test deps)

## TRD Errata

- TRD §13.4 says Wails v2 has native `options.SystemTray` — this is incorrect. Wails v2 has no system tray API. We use `energye/systray` instead.
- TRD/PRD specify port 6800, but this conflicts with aria2c's default JSON-RPC port. Changed to 9683.

## Development Phases

### Phase 1: Download Engine + CLI (COMPLETE)
Standalone binary with embedded engine. No HTTP server, no GUI, no browser extension.

**Exit criteria (met):** Can download a file in 16 segments, pause, kill the process, restart, and resume to completion. Verified by `TestIntegration_ExitCriteria`.

**What was built:**
- Step 1: Project scaffolding + models — `internal/model/`
- Step 2: Configuration management — `internal/config/`
- Step 3: Database layer (SQLite/WAL) — `internal/db/`
- Step 4: Event bus (pub/sub) — `internal/event/`
- Step 5: Probe + filename detection + HTTP client — `internal/engine/{probe,filename,httpclient}.go`
- Step 6: Segment downloader + progress aggregator — `internal/engine/{segment,progress}.go`
- Step 7: Engine core (lifecycle orchestration) — `internal/engine/{engine,refresh}.go`
- Step 8: Queue manager — `internal/queue/`
- Step 9: CLI interface — `internal/cli/`, `cmd/bolt/`
- Step 10: Integration tests + Makefile

### Phase 2: HTTP Server + Daemon (COMPLETE)
HTTP server with REST API and WebSocket. CLI refactored to HTTP client. PID file daemon management.

**Exit criteria (met):** Can add downloads via `curl` to the API, see progress via WebSocket, and queue respects concurrency limits.

**What was built:**
- Step 1: PID file management — `internal/pid/`
- Step 2: New event types (DownloadPaused, DownloadResumed) — `internal/event/`
- Step 3: Engine.ProbeURL method — `internal/engine/engine.go`
- Step 4: WebSocket dependency — `nhooyr.io/websocket`
- Step 5: HTTP server (REST + WebSocket + middleware) — `internal/server/`
- Step 6: CLI refactored to HTTP client — `internal/cli/`
- Step 7: Entry point with daemon/client modes — `cmd/bolt/main.go`

### Phase 3: Wails GUI + Svelte Frontend (COMPLETE)
Desktop app with system tray, Wails v2 bindings, Svelte 5 frontend.

**Exit criteria (met):** Fully functional desktop app that can manage downloads with core controls, no CLI needed.

**What was built:**
- Step 0: Prerequisites — Wails CLI, GTK3/WebKit system deps
- Step 1: Wails project scaffolding — `wails.json`, `frontend/`, `build/appicon.png`
- Step 2: Go app bindings (IPC methods) — `internal/app/app.go`
- Step 3: Entry point refactored for GUI mode — `cmd/bolt/gui.go`, `cmd/bolt/main.go`
- Step 4: Frontend foundation — types, utils, reactive state, layout shell
- Step 5: Download list UI — `DownloadList`, `DownloadRow`, `ProgressBar`, `ActionButtons`
- Step 6: Toolbar + SearchBar + StatusBar
- Step 7: Add download dialog with URL probing
- Step 8: Settings dialog with config persistence
- Step 9: System tray via `energye/systray` — `internal/tray/`

### Phase 4: Browser Extension — P0 (COMPLETE)
Chromium Manifest V3 extension ("Bolt Capture") that intercepts browser downloads and sends them to the Bolt daemon via REST API.

**Exit criteria (met):** Extension intercepts downloads, forwards cookies/referrer, uses check-then-cancel safety, supports context menu "Download with Bolt", Tier 2 refresh matching, and minimal config popup.

**What was built:**
- Step 1: Backend — Extended `RefreshURL` to accept optional `headers` parameter
- Step 2: Extension scaffolding — `extension/manifest.json`, icons
- Step 3: Service worker — `extension/background.js` (interception, context menu, refresh matching)
- Step 4: Popup UI — `extension/popup/` (config, connection test, capture toggle)
- Step 5: Makefile — `build-extension` target

## Key Design Decisions

**Phase 1:** CLI embedded the engine directly.

**Phase 2:** CLI is now an HTTP client. The daemon (`bolt start`) runs the engine + HTTP server. CLI commands (`bolt add`, `bolt list`, etc.) talk to the daemon via REST API. Real-time progress uses WebSocket. The engine interface stayed identical — only the calling layer changed.

**Phase 3:** GUI mode is now the default. `bolt` (no args) and `bolt start` launch the GUI. `bolt start --headless` runs the headless daemon (Phase 2 behavior). Both modes start the HTTP server for CLI/extension compatibility. The `internal/app` package wraps the engine as Wails IPC bindings. Events are forwarded via `runtime.EventsEmit`. Frontend assets are embedded at the root package (`embed.go`) since `go:embed` can't use `..` paths. System tray uses `energye/systray` with `RunWithExternalLoop` to avoid conflicting with Wails' main thread.

**Phase 4:** Vanilla JS extension (no build step). Check-then-cancel safety: verifies Bolt is reachable before cancelling browser download — if Bolt is down, the browser download proceeds normally. `RefreshURL` now accepts optional `headers` map for cookie/referrer forwarding from the extension. Tier 2 refresh matching checks `/api/downloads?status=refresh` for candidates before creating new downloads.

## Commands

```
make build       # frontend build + Go build with Wails tags → ./bolt
make build-gui   # full Wails build (same result, uses wails CLI)
make dev         # wails dev (hot-reload)
make test        # run all tests (no Wails tags needed for tests)
make test-race   # run all tests with race detector
make test-v      # run all tests verbose
make test-stress # run all tests including stress tests (slower, ~2 min)
make test-cover  # run tests with coverage report
make build-extension  # zip extension → dist/bolt-capture.zip
make clean       # remove binary, clear test cache
```

## Build Tags

Wails requires `desktop,production` build tags for release builds. On systems with webkit2gtk-4.1 (Fedora 39+, Ubuntu 24.04+), also add `webkit2_41`. The Makefile handles this automatically. CGO must be enabled (`CGO_ENABLED=1`) for the Wails/WebKit bindings.

Tests do not require Wails build tags — `go test ./...` works without them.

## Architecture

```
cmd/bolt/
  main.go                  Entry point (GUI/headless/CLI dispatch)
  gui.go                   launchGUI() + Wails window + tray setup
embed.go                   //go:embed frontend/dist
wails.json                 Wails project config
frontend/                  Svelte 5 + TypeScript + Vite + Tailwind
  src/
    App.svelte             Root layout (Toolbar + Search + List + StatusBar)
    lib/
      types.ts             TypeScript interfaces mirroring Go models
      utils/format.ts      Formatting (bytes, speed, ETA, dates)
      state/
        downloads.svelte.ts  Reactive download state + event listeners
        config.svelte.ts     Config state (load/save)
      components/
        Toolbar.svelte       Add, Pause All, Resume All, Clear, Settings
        SearchBar.svelte     Client-side filter
        DownloadList.svelte  Scrollable download list
        DownloadRow.svelte   Single download with progress + actions
        ProgressBar.svelte   Progress bar (determinate + indeterminate)
        ActionButtons.svelte Per-download context actions
        AddDownloadDialog.svelte  URL probe + download creation
        SettingsDialog.svelte     Config editor
        StatusBar.svelte     Active/queued counts + total speed
internal/
  app/                     Wails app bindings (IPC methods)
  model/                   Shared types, ID generation, formatting
  config/                  config.json management
  db/                      SQLite data access layer
  event/                   Event bus (pub/sub)
  engine/                  Download engine (core business logic)
  queue/                   Queue manager
  server/                  HTTP server (REST API + WebSocket)
  cli/                     CLI HTTP client
  pid/                     PID file management
  tray/                    System tray (energye/systray)
  testutil/                Test helpers (httptest server)
extension/                 Chromium Manifest V3 browser extension
  manifest.json            MV3 manifest (permissions, service worker)
  background.js            Service worker (interception, context menu, refresh)
  popup/
    popup.html             Config popup layout
    popup.css              Dark theme styling
    popup.js               Config load/save, connection test
  icons/                   Extension icons (16, 48, 128)
```
