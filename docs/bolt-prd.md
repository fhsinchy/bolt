# Bolt — Product Requirements Document

**Fast, segmented download manager built with Go**

---

**Version:** 1.0
**Author:** Farhan Hasin Chowdhury
**Date:** February 27, 2026
**Status:** Active

---

## 1. Overview

### 1.1 Problem Statement

Existing open-source download managers for desktop are either unreliable (Aria2 + AriaNg frequently fails to capture browser downloads), overengineered with features most users don't need (BitTorrent, metalinks), or have broken browser integration (Gopeed). There is no lightweight, fast, modern download manager that does one thing well — download regular files as fast as possible with a clean interface and reliable browser capture.

### 1.2 Product Vision

Bolt is a desktop download manager that accelerates file downloads through segmented (multi-connection) downloading. It consists of three components: a Go backend (download engine + HTTP server), a native GUI built with Wails, and a browser extension that captures downloads and forwards them to Bolt. No BitTorrent. No metalinks. No protocol bloat. Just fast, reliable file downloads.

### 1.3 Name Rationale

"Bolt" — fast, direct, and descriptive. Implies speed without being generic. The binary is `bolt`, the GUI app is "Bolt", and the browser extension is "Bolt Capture".

### 1.4 Target Users

- Power users who regularly download large files (ISOs, datasets, software packages, media)
- Developers and sysadmins who want a lightweight, modern alternative to Aria2
- Anyone frustrated with browser-native download managers that don't support resuming, segmentation, or queuing

---

## 2. Architecture

### 2.1 High-Level Architecture

```
┌────────────────────┐
│  Browser Extension  │──── Captures downloads, sends URL + cookies + headers
│  (Bolt Capture)     │
└────────┬───────────┘
         │ HTTP POST (localhost)
         ▼
┌────────────────────────────────────────────┐
│              Go Backend                     │
│  ┌──────────────┐  ┌───────────────────┐   │
│  │  HTTP Server  │  │  Wails IPC Binds  │   │
│  │  (extension)  │  │  (GUI frontend)   │   │
│  └──────┬───────┘  └────────┬──────────┘   │
│         │                   │               │
│         ▼                   ▼               │
│  ┌──────────────────────────────────────┐   │
│  │          Download Engine              │   │
│  │  - Segmented downloader              │   │
│  │  - Queue manager                     │   │
│  │  - Speed limiter                     │   │
│  │  - Retry / resume logic              │   │
│  └──────────────┬───────────────────────┘   │
│                 │                            │
│  ┌──────────────▼───────────────────────┐   │
│  │          SQLite Database              │   │
│  │  - Downloads, segments, history      │   │
│  └──────────────────────────────────────┘   │
└────────────────────────────────────────────┘
         │
         ▼
┌────────────────────┐
│   Wails GUI Window  │
│   (Svelte / React)  │
│   + System Tray     │
└────────────────────┘
```

### 2.2 Component Breakdown

| Component | Technology | Role |
|-----------|------------|------|
| Download Engine | Go (stdlib `net/http`) | Segmented downloading, resume, retry, speed control |
| HTTP Server | Go (`net/http`, Go 1.22+) | localhost API for browser extension |
| WebSocket | `nhooyr.io/websocket` | Real-time progress push to extension popup |
| GUI | Wails v2 + Svelte or React | Native desktop window, system tray |
| Database | SQLite via `modernc.org/sqlite` | Download history, segment state, settings |
| Browser Extension | Manifest V3 (JS) | Intercept browser downloads, context menu |

### 2.3 Single Binary Design

Bolt ships as a single binary. Running `bolt` or `bolt start` launches the GUI app (Wails window + tray icon + HTTP server). The HTTP server remains available for browser extension communication.

---

## 3. Core Features

### 3.1 Segmented Downloading

**Priority: P0 — This is the core value proposition.**

Bolt splits each file into N segments and downloads them concurrently over separate HTTP connections, similar to Aria2's `-x16 -s16` behavior.

**Behavior:**

1. Send a `HEAD` request to determine `Content-Length` and `Accept-Ranges: bytes` support.
2. If range requests are supported and file size is above a configurable threshold (default: 1 MB), split the file into N segments (default: 16, configurable 1–32).
3. Each segment issues a `GET` request with `Range: bytes=start-end` header.
4. Each segment writes to the correct offset in a pre-allocated file using `WriteAt`. Since each goroutine writes to a non-overlapping byte range, no mutex is needed.
5. If range requests are not supported, fall back to single-connection download.
6. Pre-allocate the target file with `os.File.Truncate(totalSize)` before downloading to reduce disk fragmentation.

**Configurable parameters:**

| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `segments` | 16 | 1–32 | Number of segments per download |
| `max_connections_per_server` | 16 | 1–32 | Max simultaneous connections to same host |
| `min_segment_size` | 1 MB | — | Files smaller than this use single connection |

### 3.2 Download Queue

**Priority: P0**

- Configurable maximum concurrent downloads (default: 3).
- Downloads beyond the limit are queued in FIFO order.
- When an active download completes, the next queued download starts automatically.
- Users can reorder the queue via the GUI (drag and drop) or promote a queued download to active.
- Queue state persists across restarts.

### 3.3 Resume Support

**Priority: P0**

- Segment progress (bytes downloaded per segment) is persisted to SQLite on a regular interval (every 2 seconds) and on pause/stop.
- On resume, Bolt reads segment state from the database, verifies the partially downloaded file still exists and matches expected size, then resumes each incomplete segment from where it left off using `Range: bytes=resumePoint-end`.
- If the remote file has changed (different `Content-Length`, `ETag`, or `Last-Modified`), Bolt discards the partial download and starts fresh, notifying the user.

### 3.4 Auto-Retry

**Priority: P0**

- Individual segments retry on failure, not the entire download.
- Retry uses exponential backoff: 1s, 2s, 4s, 8s, 16s, capped at 60s.
- Maximum retries per segment: 10 (configurable).
- If all retries for a segment are exhausted, the download is marked as errored. The user can manually retry from the GUI.
- Transient errors (timeouts, connection resets, 5xx responses) trigger retries. Permanent errors (404, 403, 410) fail immediately.

### 3.5 Dead Link Refresh

**Priority: P0 — Prevents loss of progress on expired CDN URLs.**

Many file hosts and CDNs generate temporary download URLs that expire after a set time. If a large download takes longer than the URL's TTL, segments start failing with 403, 410, or connection resets. Without link refresh, the user loses all progress and must restart from scratch.

Bolt implements a three-tier refresh chain. Each tier is attempted in order; if it succeeds, remaining tiers are skipped.

**Tier 1 — Automatic refresh (silent):**

When Bolt detects that segments are failing with expiry-like responses (403 Forbidden, 410 Gone, 401 Unauthorized, or a redirect to a login/error page), it attempts to obtain a fresh URL automatically:

1. Bolt sends a `GET` request to the stored referer URL using the original cookie jar and headers.
2. It follows redirects, looking for a final URL that points to a file with the same `Content-Length` as the original download.
3. If found, Bolt swaps the URL on the download, updates the stored headers if the server sent new cookies, and resumes all incomplete segments with the new URL.
4. This happens transparently — the user sees a brief pause and then the download continues.

This works for simple CDNs where visiting the download page re-triggers a redirect to a fresh signed URL (common with S3 presigned URLs, MediaFire, SourceForge, etc.).

**Tier 2 — Extension-assisted refresh (user interaction):**

If automatic refresh fails (referer requires JavaScript rendering, CAPTCHA, login, or the page structure is too complex), Bolt escalates to the browser:

1. Bolt marks the download as "Link Expired — Refresh Required" and sends a notification.
2. The user clicks "Refresh" in the GUI (or the extension popup). This opens the original referer URL in the user's default browser.
3. The user re-triggers the download on that page (clicks the download button/link again).
4. The browser extension intercepts the new download, recognizes that Bolt has a pending refresh for a file with matching filename/size, and sends the new URL back to Bolt as a link refresh (not a new download).
5. Bolt swaps the URL and resumes.

The extension matches refresh candidates by: (a) exact filename match, or (b) same `Content-Length` from a URL on the same domain. If multiple candidates exist, the extension asks the user which download to refresh.

**Tier 3 — Manual URL paste (fallback):**

If the extension isn't installed or the user obtained a fresh URL through other means:

1. Right-click a failed/expired download in the GUI → "Update URL."
2. Paste the new direct download URL.
3. Bolt sends a `HEAD` request to verify `Content-Length` matches the original download.
4. If it matches, the URL is swapped and segments resume. If size differs, Bolt warns the user that the file may have changed and asks whether to restart or proceed.

**Implementation details:**

- Bolt stores the referer URL and full original headers (cookies, UA) at download creation time. These are required for Tier 1 and Tier 2.
- Expiry detection triggers after 3 consecutive segment failures with the same HTTP status code (to distinguish from transient errors which are handled by auto-retry).
- The refresh operation preserves all segment progress — only the URL (and optionally headers) changes. The byte ranges remain the same.
- A download can be refreshed multiple times if the file host has aggressive URL expiry.

### 3.6 Speed Limiter

**Priority: P1**

- Global speed limit (applies across all active downloads).
- Per-download speed limit (overrides global for that download).
- Implemented via a token bucket rate limiter on reads.
- Default: unlimited. Configurable from GUI settings and per-download in the add dialog.

### 3.7 Checksum Verification

**Priority: P2**

- After download completes, optionally verify against a user-provided checksum (MD5, SHA-1, SHA-256).
- Checksum can be provided when adding the download (via GUI dialog or API parameter).
- Result shown in download details: verified, failed, or not provided.

---

## 4. Feature Priority Matrix

All features across the project, sorted by priority. Implementation phases follow this ordering.

### P0 — Core (Must ship in v1)

These are the features that make Bolt worth using over a browser's built-in downloader.

| Feature | Component | Description |
|---------|-----------|-------------|
| Segmented downloading | Engine | Multi-connection download with configurable segments (default 16) |
| Resume support | Engine | Persist segment progress, resume after pause/crash/restart |
| Auto-retry | Engine | Per-segment retry with exponential backoff |
| Single-connection fallback | Engine | Graceful handling of servers without range support |
| Filename detection | Engine | Content-Disposition → URL path → fallback |
| Download queue | Engine | FIFO queue with configurable max concurrent downloads |
| REST API | Server | Full CRUD for downloads, pause/resume/cancel |
| Bearer token auth | Server | Shared secret between extension and server |
| WebSocket progress | Server | Real-time progress push |
| Download list view | GUI | Table with progress, speed, ETA, status, actions |
| Add download dialog | GUI | URL input, filename, directory, segment count |
| Pause/Resume/Cancel | GUI | Basic download controls |
| System tray | GUI | Minimize to tray, tray menu |
| Download interception | Extension | Capture browser downloads via `chrome.downloads` API |
| Header forwarding | Extension | Cookies, referer, user-agent sent to Bolt |
| Context menu | Extension | "Download with Bolt" on links |
| Dead link refresh | Engine + Extension | Three-tier URL refresh: auto → extension-assisted → manual paste |

### P1 — Important (Ship in v1 if time allows, otherwise fast-follow)

Features that significantly improve daily usability but aren't blockers.

| Feature | Component | Description |
|---------|-----------|-------------|
| Speed limiter (global) | Engine | Bandwidth cap across all downloads |
| Speed limiter (per-download) | Engine | Per-download override |
| Duplicate URL detection | Engine | Warn when adding a URL that's already active/queued |
| Dark/light theme | GUI | Theme toggle + system detection |
| Keyboard shortcuts | GUI | Ctrl+N, Space, Delete, etc. |
| Queue reordering | GUI | Drag and drop in download list |
| Desktop notifications | GUI | OS-native notification on completion |
| Batch URL import | GUI | Paste multiple URLs or import from file |
| Search/filter | GUI | Filter downloads by filename, URL, or status |
| File type/size filters | Extension | Only capture downloads matching criteria |
| Extension popup | Extension | Mini download list with live progress |
| Domain blocklist | Extension | Never capture from specific domains |

### P2 — Nice to Have (Post-v1)

Useful features that can wait. Add them as you feel the need.

| Feature | Component | Description |
|---------|-----------|-------------|
| Checksum verification | Engine | MD5/SHA-256 verify after download |
| Download scheduling | Engine | "Start after" time for queued downloads |
| Clipboard monitoring | GUI | Detect copied URLs and prompt to download |
| Settings panel (full) | GUI | All configurable options in one place |
| Sound on completion | GUI | Audio notification |
| Extension options page | Extension | Full settings UI in extension |

### P3 — Low Priority (Whenever)

Features you may never need personally but are standard in download managers.

| Feature | Component | Description |
|---------|-----------|-------------|
| File categorization | Engine | Auto-sort downloads into subdirectories by type |
| Proxy support (HTTP/SOCKS5) | Engine | Route downloads through a proxy |
| Auto-shutdown/sleep | GUI | System action after all downloads complete (`systemctl suspend`) |

---

## 5. GUI Application

### 4.1 Technology

Wails v2 with a web frontend (Svelte recommended for bundle size; React acceptable). The Go backend exposes methods via Wails bindings that the frontend calls directly — no HTTP involved for the GUI.

### 4.2 Main Window — Download List

The primary view is a table/list of all downloads (active, queued, paused, completed, errored).

**Columns:**

| Column | Description |
|--------|-------------|
| Filename | Truncated with tooltip for full name |
| Size | Total file size (human readable) |
| Progress | Progress bar with percentage |
| Speed | Current download speed |
| ETA | Estimated time remaining |
| Segments | Visual indicator showing segment progress (mini multi-bar) |
| Status | Active / Queued / Paused / Completed / Error |
| Actions | Pause, Resume, Cancel, Remove, Open File, Open Folder |

**Interactions:**

- Double-click a completed download → open the file.
- Right-click context menu → Pause, Resume, Cancel, Remove (with/without file), Copy URL, Retry, Open containing folder.
- Drag and drop to reorder queue.
- Multi-select for batch operations (pause all, resume all, remove all).
- Toolbar buttons: Add Download, Pause All, Resume All, Clear Completed, Settings.
- Search/filter bar to find downloads by filename or URL.

### 4.3 Add Download Dialog

Triggered by: toolbar button, keyboard shortcut (Ctrl+N), browser extension, or clipboard detection.

**Fields:**

| Field | Details |
|-------|---------|
| URL | Pre-filled if from extension or clipboard |
| Filename | Auto-detected from URL/Content-Disposition; editable |
| Save to | Directory picker, defaults to configured download dir |
| Segments | Slider or number input, default 16 |
| Speed limit | Optional, per-download override |
| Checksum | Optional field for hash verification |
| Start immediately | Checkbox, default on |

Before confirming, Bolt sends a `HEAD` request to validate the URL and populate filename and size. If the URL is invalid or the server is unreachable, show an error inline.

### 4.4 System Tray

- Bolt minimizes to system tray on window close (configurable: close vs minimize).
- Tray icon shows a subtle animation or badge when downloads are active.
- Tray context menu: Show/Hide Window, Pause All, Resume All, Quit.
- Desktop notification on download complete (OS-native via Wails notification API or `beeep` library).

### 4.5 Settings Panel

| Setting | Default | Description |
|---------|---------|-------------|
| Default download directory | `~/Downloads` | Base directory for new downloads |
| Categorize by file type | Off | Auto-sort into subdirectories (Video, Compressed, Documents, etc.) |
| Max concurrent downloads | 3 | How many downloads run simultaneously |
| Default segments | 16 | Segment count for new downloads |
| Global speed limit | Unlimited | Bandwidth cap across all downloads |
| HTTP server port | 6800 | Port for browser extension communication |
| Auth token | Auto-generated | Shared secret for extension authentication |
| Start on system boot | Off | Register as startup application |
| Minimize to tray on close | On | Window close behavior |
| Clipboard monitoring | Off | Detect copied URLs and prompt to download |
| Sound on completion | On | Play a sound when a download finishes |
| Theme | System | Light / Dark / System |
| Proxy | None | HTTP or SOCKS5 proxy URL |

### 4.6 Batch Download

- "Add batch" option: paste multiple URLs (one per line) or import from a text file.
- All URLs share the same target directory and segment settings.
- Each URL becomes a separate download entry in the queue.

### 4.7 Download Scheduling

- In the Add Download dialog or via right-click on a queued download, allow setting a "Start after" time.
- Scheduled downloads sit in a "Scheduled" state until the trigger time, then move to queue.
- Use case: start large downloads at off-peak hours.

### 4.8 Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| Ctrl+N | Add new download |
| Ctrl+V | Add download from clipboard |
| Delete | Remove selected download(s) |
| Space | Pause/Resume selected |
| Ctrl+A | Select all |
| Ctrl+Q | Quit |

---

## 5. HTTP Server (Extension API)

### 5.1 Server Configuration

- Listens on `localhost` only (never exposed to network).
- Default port: `6800` (configurable).
- Authentication: `Authorization: Bearer <token>` header on all requests. Token is auto-generated on first run and stored in the config file. The browser extension stores this token.
- CORS: allow `*` origins for localhost since the extension makes requests from various contexts.

### 5.2 REST Endpoints

```
POST   /api/downloads              Add a new download
GET    /api/downloads              List all downloads (supports ?status=active&limit=50&offset=0)
GET    /api/downloads/:id          Get download details including segment progress
DELETE /api/downloads/:id          Cancel and remove a download (?delete_file=true to also delete file)
POST   /api/downloads/:id/pause    Pause a download
POST   /api/downloads/:id/resume   Resume a download
POST   /api/downloads/:id/retry    Retry a failed download
GET    /api/config                 Get current configuration
PUT    /api/config                 Update configuration
GET    /api/stats                  Global stats (active count, total speed, disk usage)
```

### 5.3 Add Download Request

```json
POST /api/downloads
{
  "url": "https://example.com/file.zip",
  "filename": "file.zip",
  "dir": "/home/user/Downloads",
  "segments": 16,
  "headers": {
    "Cookie": "session=abc123",
    "Referer": "https://example.com/download-page",
    "User-Agent": "Mozilla/5.0..."
  },
  "checksum": {
    "algorithm": "sha256",
    "value": "abc123..."
  }
}
```

The `headers` field is critical — the browser extension forwards cookies, referer, and user-agent from the browser context so that CDN/authenticated downloads work correctly.

### 5.4 Download Status Response

```json
{
  "id": "d_abc123",
  "url": "https://example.com/file.zip",
  "filename": "file.zip",
  "dir": "/home/user/Downloads",
  "total_size": 104857600,
  "downloaded": 52428800,
  "speed": 5242880,
  "eta": 10,
  "status": "active",
  "segments": [
    { "index": 0, "start": 0, "end": 6553599, "downloaded": 6553600, "done": true },
    { "index": 1, "start": 6553600, "end": 13107199, "downloaded": 3276800, "done": false }
  ],
  "created_at": "2026-02-27T10:00:00Z",
  "completed_at": null,
  "error": null
}
```

### 5.5 WebSocket Endpoint

```
WS /ws
```

After connection (with auth token as query param or first message), the server pushes progress updates at a configurable interval (default: 500ms).

**Push message format:**

```json
{
  "type": "progress",
  "downloads": [
    {
      "id": "d_abc123",
      "downloaded": 52428800,
      "speed": 5242880,
      "eta": 10,
      "status": "active"
    }
  ]
}
```

Other event types: `download_added`, `download_completed`, `download_failed`, `download_removed`.

---

## 6. Browser Extension (Bolt Capture)

### 6.1 Manifest V3

Target: Chromium-based browsers (Chrome, Edge, Brave, Arc). Firefox support can follow as a separate manifest.

### 6.2 Download Interception

Use `chrome.downloads.onCreated` to intercept browser-initiated downloads.

**Interception logic:**

1. Browser triggers a download.
2. Extension checks if interception is enabled (user can toggle on/off).
3. Extension checks file type/size filters (if configured).
4. If intercepting: cancel the browser download via `chrome.downloads.cancel()`, extract URL, filename, referrer, cookies, and user-agent, then POST to Bolt's API.
5. If not intercepting (filter mismatch or disabled): let the browser handle it.

### 6.3 File Type / Size Filters

Configurable in extension options:

- **Capture mode:** All downloads, or only files matching criteria.
- **Minimum file size:** Only capture downloads above a threshold (e.g., 2 MB). This avoids capturing tiny files, images, and web assets that are fine in the browser.
- **File extension whitelist:** Only capture specific types (e.g., `.zip`, `.iso`, `.tar.gz`, `.deb`, `.rpm`, `.AppImage`, `.mp4`, `.mkv`).
- **File extension blacklist:** Never capture specific types (e.g., `.html`, `.json`, `.xml`).
- **Domain blocklist:** Never capture from specific domains (e.g., `localhost`, `127.0.0.1`, internal domains).

Note: determining file size before download requires a HEAD request from the extension or relying on Bolt's backend to check and reject if below threshold.

### 6.4 Context Menu

Register a context menu item: **"Download with Bolt"** on links (`<a>` elements).

When clicked, extract the link's `href`, current page URL (as referer), and cookies for that domain, then POST to Bolt's API.

### 6.5 Extension Popup

A small popup (400×300) showing:

- Connection status (green dot if Bolt is reachable, red if not)
- Toggle for capture on/off
- List of recent/active downloads with progress bars
- Each entry shows: filename (truncated), progress %, speed
- Click a download to open the Bolt GUI window

The popup connects to the WebSocket when opened for live updates. When closed, the connection drops (Manifest V3 limitation — service workers don't hold persistent connections, and that's fine).

### 6.6 Extension Options Page

- Bolt server address (default: `http://localhost:6800`)
- Auth token input
- File type/size filter configuration
- Capture toggle
- "Test connection" button

### 6.7 Header Forwarding

When intercepting a download, the extension must forward these headers to Bolt:

- `Cookie` — required for authenticated downloads
- `Referer` — required by many CDNs
- `User-Agent` — some servers serve different files based on UA

Use `chrome.cookies.getAll()` for the download URL's domain and `chrome.webRequest` (where available) or tab URL for referrer.

---

## 7. Data Model

### 8.1 SQLite Schema

```sql
CREATE TABLE downloads (
    id          TEXT PRIMARY KEY,
    url         TEXT NOT NULL,
    filename    TEXT NOT NULL,
    dir         TEXT NOT NULL,
    total_size  INTEGER,
    downloaded  INTEGER DEFAULT 0,
    status      TEXT DEFAULT 'queued',  -- queued, active, paused, completed, error, scheduled
    segments    INTEGER DEFAULT 16,
    speed_limit INTEGER DEFAULT 0,      -- bytes/sec, 0 = unlimited
    headers     TEXT,                   -- JSON: forwarded cookies, referer, UA
    checksum    TEXT,                   -- "algorithm:value"
    error       TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME,
    scheduled_at DATETIME,              -- for scheduled downloads
    queue_order INTEGER                 -- for manual queue reordering
);

CREATE TABLE segments (
    download_id TEXT NOT NULL,
    idx         INTEGER NOT NULL,
    start_byte  INTEGER NOT NULL,
    end_byte    INTEGER NOT NULL,
    downloaded  INTEGER DEFAULT 0,
    done        INTEGER DEFAULT 0,
    PRIMARY KEY (download_id, idx),
    FOREIGN KEY (download_id) REFERENCES downloads(id) ON DELETE CASCADE
);

CREATE TABLE config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE INDEX idx_downloads_status ON downloads(status);
CREATE INDEX idx_downloads_created ON downloads(created_at DESC);
```

### 8.2 Configuration File

Location: `~/.config/bolt/config.json`

```json
{
  "download_dir": "~/Downloads",
  "categorize": false,
  "max_concurrent": 3,
  "default_segments": 16,
  "global_speed_limit": 0,
  "server_port": 6800,
  "auth_token": "auto-generated-uuid",
  "minimize_to_tray": true,
  "clipboard_monitor": false,
  "sound_on_complete": true,
  "theme": "system",
  "proxy": "",
  "categories": {
    "Video": [".mp4", ".mkv", ".avi", ".mov", ".webm"],
    "Compressed": [".zip", ".tar.gz", ".rar", ".7z", ".bz2"],
    "Documents": [".pdf", ".docx", ".xlsx", ".pptx"],
    "Programs": [".deb", ".rpm", ".AppImage", ".flatpak"],
    "Music": [".mp3", ".flac", ".ogg", ".wav"],
    "Images": [".iso", ".img"]
  }
}
```

### 8.3 Data Directory

```
~/.config/bolt/
├── config.json
└── bolt.db          (SQLite database)
```

---

## 8. Technical Specifications

### 9.1 Download Engine Details

**Connection handling:**

- Use a shared `http.Client` with a custom `http.Transport`:
  - `MaxIdleConnsPerHost`: 32 (accommodate 16 segments + headroom)
  - `TLSHandshakeTimeout`: 10s
  - `ResponseHeaderTimeout`: 15s
  - `IdleConnTimeout`: 90s
- Follow redirects (up to 10 hops) and persist cookies across the redirect chain using a `http.CookieJar`.
- Support HTTP and SOCKS5 proxies via `Transport.Proxy`.

**Filename detection priority:**

1. User-specified filename (from GUI dialog or API request)
2. `Content-Disposition` header (`filename=` or `filename*=` parameter)
3. Last path segment of the final URL (after redirects), URL-decoded
4. Fallback: `download_<timestamp>`

**Duplicate handling:**

- If a file with the same name exists in the target directory, append `(1)`, `(2)`, etc.
- If a download with the same URL is already active/queued, warn the user and ask whether to proceed.

**Progress reporting:**

- Each segment goroutine reports bytes read through a channel.
- An aggregator goroutine collects these, computes per-second speed (rolling 5-second average for smoothness), ETA, and total progress.
- Progress is emitted as events that both the Wails frontend and WebSocket subscribers can consume.

### 9.2 Speed Limiting Implementation

Use `golang.org/x/time/rate` or a custom token bucket:

- Global limiter: shared across all download goroutines.
- Per-download limiter: each download has its own limiter; segment goroutines for that download share it.
- When both global and per-download limits are set, the effective limit is the minimum of the two.
- Limiter wraps the `io.Reader` from the HTTP response body, throttling reads.

### 9.3 File Category Mapping

When categorization is enabled, the target directory is `<download_dir>/<category>/`:

| Category | Extensions |
|----------|------------|
| Video | `.mp4`, `.mkv`, `.avi`, `.mov`, `.webm`, `.flv` |
| Compressed | `.zip`, `.tar.gz`, `.tar.bz2`, `.rar`, `.7z`, `.gz`, `.xz` |
| Documents | `.pdf`, `.docx`, `.xlsx`, `.pptx`, `.txt`, `.epub` |
| Programs | `.deb`, `.rpm`, `.AppImage`, `.flatpak`, `.sh` |
| Music | `.mp3`, `.flac`, `.ogg`, `.wav`, `.aac` |
| Disk Images | `.iso`, `.img` |
| Other | Everything else (stays in base download directory) |

### 9.4 Auto-Shutdown

Optional feature: after all downloads complete, Bolt can:

- Do nothing (default)
- Show a notification only
- Shut down the system (with a 60-second countdown + cancel option)
- Put the system to sleep

Implemented via `systemctl poweroff` / `systemctl suspend`.

---

## 9. Platform Support

### 10.1 Target Platform

Bolt targets **Linux only** (x86_64, aarch64). This is a deliberate focus decision — Linux lacks a good, modern download manager, and going deep on one platform enables tighter desktop integration (D-Bus notifications, XDG portals, systemd) rather than spreading thin across three.

| Platform | Status | Notes |
|----------|--------|-------|
| Linux (x86_64) | Primary | Wails + GTK/WebKit |
| Linux (aarch64) | Planned | Same stack, ARM64 builds |

### 10.2 Browser Extension

| Browser | Status | Notes |
|---------|--------|-------|
| Chrome / Chromium | Primary | Manifest V3 |
| Firefox | Primary | Manifest V3 with `browser.*` API |

---

## 10. Build & Distribution

### 11.1 Project Structure

```
bolt/
├── cmd/
│   └── bolt/
│       └── main.go              # Entry point (GUI)
├── internal/
│   ├── engine/
│   │   ├── engine.go            # Download engine core
│   │   ├── segment.go           # Segment downloader
│   │   ├── limiter.go           # Speed limiter
│   │   └── queue.go             # Queue manager
│   ├── server/
│   │   ├── server.go            # HTTP server
│   │   ├── handlers.go          # API route handlers
│   │   └── websocket.go         # WebSocket handler
│   ├── db/
│   │   ├── db.go                # SQLite connection and migrations
│   │   ├── downloads.go         # Download CRUD
│   │   └── segments.go          # Segment CRUD
│   ├── config/
│   │   └── config.go            # Configuration management
│   └── app/
│       └── app.go               # Wails app bindings (methods exposed to frontend)
├── frontend/                    # Svelte/React app (Wails frontend)
│   ├── src/
│   ├── public/
│   └── package.json
├── extension/                   # Browser extension (Manifest V3)
│   ├── manifest.json
│   ├── background.js            # Service worker (download interception)
│   ├── popup/                   # Popup UI
│   ├── options/                 # Options page
│   └── icons/
├── build/                       # Wails build configuration
├── go.mod
├── go.sum
├── wails.json
├── Makefile
└── README.md
```

### 11.2 Dependencies

**Go modules:**

| Module | Purpose |
|--------|---------|
| `github.com/wailsapp/wails/v2` | GUI framework |
| `nhooyr.io/websocket` | WebSocket for extension communication |
| `modernc.org/sqlite` | SQLite (pure Go, no CGO) |
| `golang.org/x/time/rate` | Token bucket rate limiter |

No other external dependencies. Stdlib for HTTP server, routing, crypto, JSON, etc.

**Frontend:**

| Package | Purpose |
|---------|---------|
| Svelte (or React) | UI framework |
| Tailwind CSS | Styling |

### 11.3 Build Commands

```bash
# Development
wails dev                        # Hot-reload GUI development

# Production build
make build                       # Frontend + Go build with Wails tags

# Extension
make build-extension             # Build Chrome + Firefox extension zips
```

---

## 11. Implementation Phases

Phases are ordered by the priority matrix (Section 4). Each phase ships something usable.

### Phase 1 — Download Engine (P0) — Week 1–2

Build the engine as a standalone Go package.

**Deliverables (all P0):**

- Segmented download with configurable segment count
- Single-connection fallback for servers without range support
- Resume support with segment state persistence (SQLite)
- Auto-retry with exponential backoff per segment
- Filename detection (Content-Disposition, URL path)
- Progress reporting via channels
- Dead link refresh — Tier 3 (manual URL swap)
- Unit tests for engine, integration tests with a local HTTP server

**Exit criteria:** Can download a 1 GB file from a CDN in 16 segments, pause, kill the process, restart, and resume to completion.

### Phase 2 — Server & Queue (P0) — Week 2–3

Wrap the engine with the HTTP API and add queue management.

**Deliverables (all P0):**

- HTTP server with all REST endpoints
- WebSocket progress push
- Bearer token authentication
- Download queue with configurable max concurrent downloads
- Dead link refresh — Tier 1 (automatic referer-based URL refresh on expiry detection)

**Exit criteria:** Can add downloads via `curl` to the API, see progress via WebSocket, and queue respects concurrency limits.

### Phase 3 — GUI Core (P0) — Week 3–5

Build the Wails application with P0 GUI features only.

**Deliverables (all P0):**

- Main window with download list (progress, speed, ETA, status)
- Add download dialog with URL validation and HEAD pre-check
- Pause/Resume/Cancel controls
- System tray with minimize-to-tray
- Basic settings (download dir, max concurrent, default segments, server port, auth token)

**Exit criteria:** Fully functional desktop app that can manage downloads with core controls.

### Phase 4 — Browser Extension Core (P0) — Week 5–6

Build Bolt Capture for Chromium browsers with P0 features.

**Deliverables (all P0):**

- Download interception via `chrome.downloads.onCreated`
- Cookie, referer, and user-agent forwarding
- Context menu "Download with Bolt"
- Dead link refresh — Tier 2 (extension-assisted refresh matching) and Tier 3 GUI ("Update URL" in right-click menu)

**Exit criteria:** Install extension in Chrome, click a download link on a website, and it appears in Bolt with full speed and correct authentication. Expired downloads can be refreshed through all three tiers.

### Phase 5 — Linux-Only Focus Shift

Remove cross-platform code and update documentation to reflect Linux-only targeting.

**Deliverables:**

- Remove Windows/macOS code paths from `internal/notify/` and `internal/app/`
- Update PRD, TRD, README, STATUS, CLAUDE.md to reflect Linux-only focus

**Exit criteria:** No Windows/macOS code paths remain. All docs reflect Linux-only focus.

### Phase 6 — P1 Features

Add the features that make daily use smoother.

**Deliverables (all P1):**

- Global and per-download speed limiter
- Duplicate URL detection
- Dark/light theme + system detection
- Keyboard shortcuts
- Queue reordering (drag and drop)
- Desktop notifications on completion
- Batch URL import
- Search/filter in download list
- Extension: file type/size filters
- Extension: popup with live progress
- Extension: domain blocklist

**Exit criteria:** A polished v1 that handles all common daily download scenarios comfortably.

### Phase 7 — P2 Features — Post-v1

Add incrementally as needed. No fixed timeline.

- Checksum verification
- Download scheduling
- Clipboard monitoring
- Full settings panel
- Sound on completion
- Extension options page

### Phase 8 — P3 Features — Whenever

Low priority, add only if you feel the need.

- File categorization by type
- Proxy support (HTTP/SOCKS5)
- Auto-shutdown/sleep after downloads complete

---

## 12. Success Metrics

| Metric | Target |
|--------|--------|
| Download speed vs browser | ≥ 2x faster on files > 50 MB from CDNs supporting range requests |
| Resume reliability | 100% resume success rate after process kill on supporting servers |
| Extension capture rate | > 95% of browser downloads intercepted when enabled |
| Memory usage (idle) | < 50 MB with no active downloads |
| Memory usage (active) | < 200 MB with 3 concurrent downloads at 16 segments each |
| Binary size | < 30 MB (single binary with embedded frontend) |
| Startup time | < 2 seconds to tray-ready |

---

## 13. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| CDN URLs expire before all segments complete | Segments fail mid-download | Capture and store full cookie jar; implement URL refresh by re-requesting from original referer page |
| Servers rate-limit many connections | Slower than expected or blocked | Detect 429 responses, auto-reduce segment count, respect Retry-After headers |
| Manifest V3 limits on download interception | Extension can't reliably capture all downloads | Use `chrome.downloads` API which is well-supported; fall back to context menu for edge cases |
| Wails WebKit version differences across distros | UI inconsistencies | Test on major distros (Fedora, Ubuntu, Arch); use webkit2_41 build tag where needed |
| SQLite write contention under heavy load | Slow progress persistence | Use WAL mode, batch writes, and accept eventual consistency for progress (worst case: lose ~2s of progress on crash) |

---

## 14. Out of Scope (Permanently)

The following will not be implemented:

- BitTorrent / magnet links
- Metalink support
- Video/stream grabbing (HLS, DASH)
- Built-in browser
- Plugin/extension system within Bolt
- Multi-language i18n
- FTP/SFTP protocol support
- Site scraping / recursive download
