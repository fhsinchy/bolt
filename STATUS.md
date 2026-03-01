# Bolt — Project Status Report

**Date:** March 1, 2026 (updated)

---

## Phase Completion Summary

| Phase | Status | Completion |
|-------|--------|------------|
| Phase 1 — Engine + CLI | **COMPLETE** | 100% |
| Phase 2 — HTTP Server + Daemon | **COMPLETE** | 100% |
| Phase 3 — Wails GUI + Frontend | **COMPLETE** | 100% |
| Phase 4 — Browser Extension | **COMPLETE** | 100% |
| Phase 5 — P1 Features | **IN PROGRESS** | ~25% |
| Phase 6 — P2 Features | **NOT STARTED** | ~5% (config fields only) |
| Phase 7 — P3 Features | **NOT STARTED** | ~5% (config fields only) |

---

## Phase 1: Download Engine + CLI — COMPLETE

All deliverables built and tested:

- Segmented downloading with configurable segments
- Single-connection fallback
- Resume support with SQLite persistence
- Auto-retry with exponential backoff
- Filename detection (Content-Disposition, URL path)
- Progress reporting via event bus
- Dead link refresh (Tier 3 — manual URL swap via CLI `bolt refresh`)
- CLI commands: `add`, `list`, `status`, `pause`, `resume`, `cancel`
- Integration tests with local HTTP server (`TestIntegration_ExitCriteria`)

## Phase 2: HTTP Server + Daemon — COMPLETE

All deliverables built and tested:

- REST API with all endpoints (add, list, get, delete, pause, resume, retry, refresh, probe, config, stats)
- WebSocket progress push
- Bearer token authentication + CORS middleware
- PID file management (`internal/pid/`)
- CLI refactored to HTTP client (talks to daemon)
- Headless daemon mode (`bolt start --headless`)

## Phase 3: Wails GUI + Svelte Frontend — COMPLETE

All deliverables built:

- Wails app bindings (`internal/app/app.go`)
- Entry point with GUI/headless/CLI dispatch (`cmd/bolt/main.go`, `cmd/bolt/gui.go`)
- System tray via `energye/systray` (`internal/tray/`)
- Tray icon click toggles window visibility
- Cancel confirmation dialog
- Frontend components: `DownloadList`, `DownloadRow`, `ProgressBar`, `ActionButtons`, `Toolbar`, `SearchBar`, `StatusBar`, `AddDownloadDialog`, `SettingsDialog`
- Embedded frontend assets (`embed.go`)
- Minimize-to-tray setting takes effect immediately (live config read)

## Phase 4: Browser Extension — COMPLETE

All deliverables built:

- Browser extensions split into `extensions/chrome/` and `extensions/firefox/` (no runtime polyfills)
- Service worker / background script — download interception via `downloads.onCreated`
- Content script (`content.js`) — link click interception for 30+ file types
- Context menu — "Download with Bolt" on right-click links
- Header forwarding — Cookie, Referer, User-Agent sent to Bolt daemon
- Tier 2 dead link refresh — matches by filename/domain against `/api/downloads?status=refresh`
- Check-then-cancel safety — verifies Bolt is reachable before cancelling browser download
- Popup UI — config, connection test, capture toggle
- Welcome page on first install
- Desktop notifications on capture success/failure
- Download bar suppression via `chrome.downloads.setUiOptions()`
- Graceful fallback on invalidated extension context (try-catch in content script)
- Probe falls back from HEAD to GET on 403 (pre-signed S3/R2 URL support)

---

## P0 Feature Status

| Feature | Status |
|---------|--------|
| Segmented downloading | Done |
| Resume support | Done |
| Auto-retry | Done |
| Single-connection fallback | Done |
| Filename detection | Done |
| Download queue | Done |
| REST API | Done |
| Bearer token auth | Done |
| WebSocket progress | Done |
| Download list view (GUI) | Done |
| Add download dialog | Done |
| Pause/Resume/Cancel (GUI) | Done |
| System tray | Done |
| Dead link refresh (Tier 1 auto) | Done (`internal/engine/refresh.go`) |
| Dead link refresh (Tier 3 manual) | Done (CLI `refresh` + API endpoint) |
| CLI | Done |
| Download interception (extension) | Done (`chrome.downloads.onCreated` + content script) |
| Header forwarding (extension) | Done (Cookie/Referer/User-Agent) |
| Context menu (extension) | Done ("Download with Bolt") |
| Dead link refresh Tier 2 (extension-assisted) | Done (filename/domain matching) |

## P1 Feature Status

| Feature | Status |
|---------|--------|
| Speed limiter (global + per-download) | **Not enforced** — model fields exist (`SpeedLimit`, `GlobalSpeedLimit`), no rate limiter in engine |
| Duplicate URL detection | Done (`ErrDuplicateURL`, 409 Conflict) |
| Dark/light theme | **Config only** — `theme` field exists (default "system"), frontend always renders light |
| Keyboard shortcuts | Partial — Enter to probe, Escape to close dialogs; no global shortcuts |
| Queue reordering (drag & drop) | Not implemented (`queue_order` model field exists, unused) |
| Desktop notifications | Not implemented in desktop app (extension has chrome notifications) |
| Batch URL import | Not implemented |
| Search/filter in download list | Done — `SearchBar` with client-side text filtering |
| Extension popup | Done (`extension/popup/`) |
| Extension file/size filters | Partial — file extension filtering (30+ types); no user-configurable size filter |
| Extension domain blocklist | Partial — hardcoded blocklist (localhost); not user-configurable |

## P2 Feature Status

| Feature | Status |
|---------|--------|
| Checksum verification | **Config only** — `Checksum` struct in model; not enforced in engine |
| Download scheduling | Not implemented |
| Clipboard monitoring | **Config only** — `clipboard_monitor` field exists; not wired |
| Full settings panel | Partial — exposes 5 settings; missing: theme, speed limit, proxy, categorize, etc. |
| Sound on completion | **Config only** — `sound_on_complete` field exists; not wired |
| Extension options page | Not implemented (popup only) |
| CLI `--json` output | Not implemented |

## P3 Feature Status

| Feature | Status |
|---------|--------|
| File categorization by type | **Config only** — `categorize` bool + `categories` map exist; not used by engine |
| Proxy support (HTTP/SOCKS5) | **Config only** — `proxy` field exists; not connected to HTTP client |
| Auto-shutdown/sleep | Not implemented |
| Start on system boot | Not implemented |
| Firefox extension | Done (`extensions/firefox/`) |

---

## Other Missing Artifacts

- No `bolt.service` systemd user unit file (mentioned in TRD §21)
