# Bolt — Download Manager

Fast, segmented download manager for **Linux**. See `bolt-prd.md` and `bolt-trd.md` for full specs.

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

## Icons & Wayland

Wails' `linux.Options{Icon: []byte}` calls `gtk_window_set_icon()`, which only works on X11. On Wayland (both GNOME and KDE), compositors ignore GTK window icons and instead match the app ID to a `.desktop` file to look up icons from the icon theme.

**How it works:**
- `linux.Options{ProgramName: "bolt"}` calls `g_set_prgname("bolt")`, which sets the Wayland `app_id` to `"bolt"`
- The compositor looks for `bolt.desktop` in `~/.local/share/applications/` or `/usr/share/applications/`
- The `.desktop` file specifies `Icon=bolt`, which resolves to `bolt.png` in the icon theme (e.g. `~/.local/share/icons/hicolor/256x256/apps/bolt.png` or `/usr/share/icons/hicolor/256x256/apps/bolt.png`)

**What packages must install:**
1. `bolt.desktop` → `/usr/share/applications/` (or `~/.local/share/applications/` for user installs)
2. `bolt.png` → `/usr/share/icons/hicolor/256x256/apps/` (source: `build/appicon.png`)
3. The `.desktop` file's `StartupWMClass=bolt` must match `ProgramName` in `gui.go`

**Icon files:**
- `build/appicon.png` — 256x256 RGBA, app icon (Wails build + icon theme install)
- `cmd/bolt/appicon.png` — same file, embedded via `//go:embed` for `linux.Options{Icon}` (X11 fallback)
- `internal/tray/icon.png` — 64x64 white-on-transparent, embedded for systray via `energye/systray`
- `extensions/{chrome,firefox}/icons/` — 16, 48, 128px variants for browser extensions
- `images/icon.png` — full-resolution source icon (1536x1024), used to generate all of the above
- `images/banner.png` — repo banner for README
- `dist/bolt.desktop` — desktop entry file for Wayland icon resolution

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
- Step 2: Extension scaffolding — `extensions/chrome/`, `extensions/firefox/`, icons
- Step 3: Service worker / background script — interception, context menu, refresh matching
- Step 4: Popup UI — config, connection test, capture toggle
- Step 5: Makefile — `build-extension` target

### Phase 5: Linux-Only Focus Shift (COMPLETE)
Removed cross-platform code, updated all docs to reflect Linux-only targeting.

**What was done:**
- Removed Windows/macOS code paths from `internal/notify/notify.go` (was: `runtime.GOOS` switch with `osascript` and PowerShell; now: direct `notify-send` call)
- Removed Windows/macOS code paths from `internal/app/app.go` `openPath()` (was: `runtime.GOOS` switch; now: direct `xdg-open` call)
- Updated PRD, TRD, README, STATUS, CLAUDE.md
- Added Steam Deck / Decky Plugin as Phase 9
- Renumbered P1/P2/P3 feature phases to 6/7/8

### Phase 6: Remaining P1 Features (COMPLETE)
Four features for daily-driver readiness.

**What was built:**
- Extension filters + blocklist — user-configurable min file size, extension whitelist/blacklist, domain blocklist in popup; `shouldCapture(url, config)` with subdomain matching
- Keyboard shortcuts — Ctrl+N (add), Ctrl+V (paste URL), Delete (remove selected), Space (toggle pause), Ctrl+A (select all), Ctrl+Q (quit); guards for dialogs/form focus
- Batch URL import — `BatchImportDialog` with textarea/file import, sequential `AddDownload` calls with progress tracking; `SelectTextFile`/`ReadTextFile` IPC methods
- Queue reordering (drag & drop) — `queue_order` column, `NextQueueOrder`/`ReorderDownloads` DB methods, `PUT /api/downloads/reorder` endpoint, HTML5 drag-and-drop in `DownloadList`/`DownloadRow` with grip handle

### Phase 9: Steam Deck + Decky Plugin (NOT STARTED)
Decky Loader plugin (Python + React) as thin client to Bolt's REST API. QAM panel for Gaming Mode.

## Key Design Decisions

**Linux-Only Focus:** Bolt targets Linux exclusively. This enables deep desktop integration (D-Bus, XDG portals, systemd, Steam Deck / Decky Loader) instead of lowest-common-denominator cross-platform abstractions. The Go stdlib functions like `os.UserConfigDir()` still work correctly on Linux and are kept as-is.

**Phase 1:** CLI embedded the engine directly.

**Phase 2:** CLI is now an HTTP client. The daemon (`bolt start`) runs the engine + HTTP server. CLI commands (`bolt add`, `bolt list`, etc.) talk to the daemon via REST API. Real-time progress uses WebSocket. The engine interface stayed identical — only the calling layer changed.

**Phase 3:** GUI mode is now the default. `bolt` (no args) and `bolt start` launch the GUI. `bolt start --headless` runs the headless daemon (Phase 2 behavior). Both modes start the HTTP server for CLI/extension compatibility. The `internal/app` package wraps the engine as Wails IPC bindings. Events are forwarded via `runtime.EventsEmit`. Frontend assets are embedded at the root package (`embed.go`) since `go:embed` can't use `..` paths. System tray uses `energye/systray` with `RunWithExternalLoop` to avoid conflicting with Wails' main thread.

**Phase 4:** Vanilla JS extensions split into `extensions/chrome/` and `extensions/firefox/` (no build step, no runtime polyfills). Each directory is directly loadable in its browser. Check-then-cancel safety: verifies Bolt is reachable before cancelling browser download — if Bolt is down, the browser download proceeds normally. `RefreshURL` now accepts optional `headers` map for cookie/referrer forwarding from the extension. Tier 2 refresh matching checks `/api/downloads?status=refresh` for candidates before creating new downloads.

### Alpha Release Features (COMPLETE)
Six features for daily-driver readiness:

**What was built:**
- Global speed limiter — `golang.org/x/time/rate`, `Engine.SetSpeedLimit()`, throttles all segments via shared `rate.Limiter`
- Complete settings panel — speed limit (KB/MB) + theme selector in SettingsDialog
- Desktop notifications — `internal/notify/` package, `notify-send` on Linux
- Dark theme — Tailwind `@custom-variant dark`, class-based toggle on `<html>`, system/light/dark modes, all 10 components updated
- Systemd user unit — `dist/bolt.service`, `make install` / `make uninstall`
- Firefox extension support — separate `extensions/firefox/` directory using native `browser.*` API, no polyfills

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
make build-extension         # build both Chrome and Firefox zips
make build-extension-chrome  # zip extensions/chrome/ → dist/bolt-capture-chrome.zip
make build-extension-firefox # zip extensions/firefox/ → dist/bolt-capture-firefox.zip
make install     # build + install binary + systemd unit + .desktop + icon
make uninstall   # stop + disable + remove binary + unit + .desktop + icon
make clean       # remove binary, clear test cache
```

## Build Tags

Wails requires `desktop,production` build tags for release builds. On systems with webkit2gtk-4.1 (Fedora 39+, Ubuntu 24.04+), also add `webkit2_41`. The Makefile handles this automatically. CGO must be enabled (`CGO_ENABLED=1`) for the Wails/WebKit bindings.

Tests do not require Wails build tags — `go test ./...` works without them.

## Architecture

```
cmd/bolt/
  main.go                  Entry point (GUI/headless/CLI dispatch)
  gui.go                   launchGUI() + Wails window + tray + Linux icon setup
  appicon.png              Embedded app icon for linux.Options{Icon} (X11 fallback)
build/
  appicon.png              256x256 app icon (Wails build + icon theme install)
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
        Toolbar.svelte       Add, Import, Pause All, Resume All, Clear, Settings
        SearchBar.svelte     Client-side filter
        DownloadList.svelte  Scrollable download list + drag-and-drop reordering
        DownloadRow.svelte   Single download with progress + actions + drag handle
        ProgressBar.svelte   Progress bar (determinate + indeterminate)
        ActionButtons.svelte Per-download context actions
        AddDownloadDialog.svelte  URL probe + download creation (supports initialUrl prop)
        BatchImportDialog.svelte  Batch URL import (paste/file) with progress
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
  tray/                    System tray (energye/systray, white icon embedded)
  notify/                  Desktop notifications (notify-send)
  testutil/                Test helpers (httptest server)
extensions/
  chrome/                  Chrome browser extension (chrome.* API)
    manifest.json          MV3 manifest (service_worker, downloads.ui)
    background.js          Service worker (interception, context menu, refresh, user filters)
    content.js             Content script (link click interception)
    popup/                 Config popup (with Save As warning banner + filters section)
    welcome/               First-install welcome page (2 steps)
    icons/                 Extension icons (16, 48, 128)
  firefox/                 Firefox browser extension (browser.* API)
    manifest.json          MV3 manifest (background scripts, menus, gecko settings)
    background.js          Background script (no setUiOptions, user filters)
    content.js             Content script (link click interception)
    popup/                 Config popup (no Save As warning, filters section)
    welcome/               First-install welcome page (1 step, no JS)
    icons/                 Extension icons (16, 48, 128)
images/
  icon.png                 Full-resolution source icon (generate all sizes from this)
  banner.png               Repo banner for README
dist/
  bolt.service             Systemd user unit file
  bolt.desktop             Desktop entry (Wayland icon resolution)
```
