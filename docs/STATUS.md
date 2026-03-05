# Bolt — Project Status Report

**Date:** March 3, 2026 (updated)

---

## Phase Completion Summary

| Phase | Status | Completion |
|-------|--------|------------|
| Phase 1 — Engine | **COMPLETE** | 100% |
| Phase 2 — HTTP Server | **COMPLETE** | 100% |
| Phase 3 — Wails GUI + Frontend | **COMPLETE** | 100% |
| Phase 4 — Browser Extension | **COMPLETE** | 100% |
| Phase 5 — Linux-Only Focus Shift | **COMPLETE** | 100% |
| Phase 6 — P1 Features | **COMPLETE** | 100% |
| Phase 7 — P2 Features | **COMPLETE** | 100% |

---

## Phase 1: Download Engine — COMPLETE

All deliverables built and tested:

- Segmented downloading with configurable segments
- Single-connection fallback
- Resume support with SQLite persistence
- Auto-retry with exponential backoff
- Filename detection (Content-Disposition, URL path)
- Progress reporting via event bus
- Dead link refresh (Tier 3 — manual URL swap)
- Integration tests with local HTTP server (`TestIntegration_ExitCriteria`)

## Phase 2: HTTP Server — COMPLETE

All deliverables built and tested:

- REST API with all endpoints (add, list, get, delete, pause, resume, retry, refresh, probe, config, stats)
- WebSocket progress push
- Bearer token authentication

## Phase 3: Wails GUI + Svelte Frontend — COMPLETE

All deliverables built:

- Wails app bindings (`internal/app/app.go`)
- Entry point with GUI dispatch (`cmd/bolt/main.go`, `cmd/bolt/gui.go`)
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
- Header forwarding — Cookie, Referer, User-Agent sent to Bolt
- Tier 2 dead link refresh — matches by filename/domain against `/api/downloads?status=refresh`
- Check-then-cancel safety — verifies Bolt is reachable before cancelling browser download
- Popup UI — config, connection test, capture toggle
- Welcome page on first install
- Desktop notifications on capture success/failure
- Download bar suppression via `chrome.downloads.setUiOptions()`
- Graceful fallback on invalidated extension context (try-catch in content script)
- Probe falls back from HEAD to GET on 403 (pre-signed S3/R2 URL support)

## Phase 5: Linux-Only Focus Shift — COMPLETE

Bolt now targets Linux only. Cross-platform code removed, docs updated.

- Removed Windows/macOS code paths from `internal/notify/` (was: `osascript`, PowerShell; now: `notify-send` only)
- Removed Windows/macOS code paths from `internal/app/` `openPath()` (was: `open`, `cmd /c start`; now: `xdg-open` only)
- Updated PRD, TRD, README, STATUS, CLAUDE.md to reflect Linux-only focus
- Renumbered phases 5-7 → 6-8

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
| Dead link refresh (Tier 3 manual) | Done (GUI + API endpoint) |
| Download interception (extension) | Done (`chrome.downloads.onCreated` + content script) |
| Header forwarding (extension) | Done (Cookie/Referer/User-Agent) |
| Context menu (extension) | Done ("Download with Bolt") |
| Dead link refresh Tier 2 (extension-assisted) | Done (filename/domain matching) |

## P1 Feature Status

| Feature | Status |
|---------|--------|
| Speed limiter (global) | Done — `golang.org/x/time/rate`, shared limiter across all segments, configurable in Settings |
| Duplicate URL detection | Done (`ErrDuplicateURL`, 409 Conflict) |
| Dark/light theme | Done — class-based toggle (system/light/dark), all components styled |
| Keyboard shortcuts | Done — Ctrl+N/V/A/Q, Delete, Space; dialog/focus guards |
| Queue reordering (drag & drop) | Done — `queue_order` DB column, `PUT /api/downloads/reorder`, HTML5 DnD in frontend |
| Desktop notifications | Done — `internal/notify/` (`notify-send`) |
| Batch URL import | Done — `BatchImportDialog`, paste/file import, `SelectTextFile`/`ReadTextFile` IPC |
| Search/filter in download list | Done — `SearchBar` with client-side text filtering |
| Extension popup | Done (`extensions/chrome/popup/`, `extensions/firefox/popup/`) |
| Extension file/size filters | Done — hardcoded + user-configurable min file size, extension whitelist/blacklist in popup |
| Extension domain blocklist | Done — hardcoded blocklist + user-configurable domain blocklist with subdomain matching |
| Download details dialog | Done — segment visualization, URL refresh, checksum editing, metadata; info button + double-click |

## P2 Feature Status

| Feature | Status |
|---------|--------|
| Checksum verification | Done — verified on completion, editable via details dialog (including active downloads), pass/fail indicator |
| Full settings panel | Done — exposes 9 settings (dir, concurrency, segments, retries, tray, speed limit, theme, port, token) |

## P3 Feature Status

| Feature | Status |
|---------|--------|
| Start on system boot | Done — `packaging/bolt.service` systemd user unit, `make install` / `make uninstall` |
| Firefox extension | Done (`extensions/firefox/`) |

## Other Missing Artifacts

None — all planned artifacts are implemented.
