# bolt-host and Chrome Extension — Design Spec

## Overview

Phase 2 adds two components to Bolt:

1. **bolt-host** — a Go native messaging bridge binary that relays commands between the Chrome extension and the Bolt daemon over the Unix socket.
2. **Chrome extension** — a fresh Manifest V3 implementation that hands browser downloads to Bolt through bolt-host.

The extension is a thin handoff client. It captures download intent, collects browser metadata the daemon cannot access, and forwards requests through bolt-host. It does not own downloader policy.

**Parent specs:** `docs/PRD.md` (Phase 2), `docs/TRD.md` (Sections 9.3, 17.1-17.5), `docs/chrome-extension-spec.md`

---

## bolt-host

### Binary and Module

`cmd/bolt-host/main.go` — a separate Go binary in the same module (`github.com/fhsinchy/bolt`). Imports `internal/model` for shared types (AddRequest, ProbeResult, etc.) but nothing else from the daemon.

Built with: `CGO_ENABLED=0 go build -o bolt-host ./cmd/bolt-host/`

Installed to: `~/.local/bin/bolt-host`

### Process Lifecycle

- Chrome spawns bolt-host when the extension calls `runtime.connectNative("com.fhsinchy.bolt")`
- bolt-host opens an `http.Client` with a Unix socket transport to `$XDG_RUNTIME_DIR/bolt/bolt.sock`
- Reads length-prefixed JSON commands from stdin, relays each to the daemon, writes length-prefixed JSON responses to stdout
- Exits when stdin closes (extension disconnects) or on fatal error
- Stateless — no persistent state between commands, behaves as a thin session bridge

### Native Messaging Wire Format

Chrome's native messaging protocol:
- 4-byte little-endian uint32 length prefix, then JSON payload
- Same format in both directions (stdin and stdout)

### Command Protocol

**Request envelope:**
```json
{"command": "ping"}
{"command": "add_download", "data": {"url": "...", "headers": {...}, "referer_url": "..."}}
{"command": "probe", "data": {"url": "...", "headers": {...}}}
```

**Response envelope:**
```json
{"command": "ping", "success": true, "data": {"version": "0.4.0", "active_count": 2}}
{"command": "add_download", "success": true, "data": {"id": "01ABC...", "filename": "file.iso"}}
{"command": "add_download", "success": false, "error": "duplicate_filename", "data": {"existing_id": "01XYZ..."}}
```

Every response includes the originating `command` name, a `success` boolean, and either `data` (on success) or `error` plus optional `data` (on failure).

### Supported Commands (V1)

| Command | Daemon Endpoint | Purpose |
|---------|----------------|---------|
| `ping` | `GET /api/stats` | Health check + version |
| `add_download` | `POST /api/downloads` | Hand off a download |
| `probe` | `POST /api/probe` | Check URL metadata before adding |

### Internal Structure

- **stdin reader goroutine:** reads length-prefixed messages, decodes JSON, makes HTTP request to daemon via Unix socket, writes length-prefixed response to stdout
- **stdout writes** protected by a mutex (future-proofing for async additions)
- **HTTP client** with 10-second timeout per request, Unix socket dialer
- If the Unix socket doesn't exist or connect fails, commands return `{"success": false, "error": "daemon_unavailable"}`
- Unknown commands return `{"success": false, "error": "unknown_command"}`

### Installation and Manifest

Chrome requires a native messaging host manifest to locate bolt-host.

**Manifest location:**
- Chrome: `~/.config/google-chrome/NativeMessagingHosts/com.fhsinchy.bolt.json`
- Chromium: `~/.config/chromium/NativeMessagingHosts/com.fhsinchy.bolt.json`

**Manifest content:**
```json
{
    "name": "com.fhsinchy.bolt",
    "description": "Bolt Download Manager bridge",
    "path": "/home/<user>/.local/bin/bolt-host",
    "type": "stdio",
    "allowed_origins": ["chrome-extension://<extension-id>/"]
}
```

- `path` must be absolute — `make install` generates it with `$(HOME)`
- `allowed_origins` uses the extension ID derived from the `key` field in `manifest.json` (deterministic for unpacked development builds)
- `make install` installs manifests to both Chrome and Chromium paths if their config directories exist

### Makefile Changes

- `build-host` — builds bolt-host binary
- `install` — extended to install bolt-host binary + generate native messaging manifest(s)
- `uninstall` — extended to remove bolt-host binary + manifests
- `build-all` — includes `build-host`
- `test-all` — includes bolt-host tests

---

## Chrome Extension

### Design Principle

The extension does only what the browser can uniquely do: capture download intent, collect request metadata, forward to bolt-host, and fall back safely when Bolt is unavailable.

It does not own downloader policy, refresh matching, duplicate resolution, queue logic, or filename heuristics.

### File Structure

```
extensions/chrome/
  manifest.json          Manifest V3, permissions, service worker declaration
  background.js          Service worker — port management, interception, context menu
  content.js             Content script — link click interception
  popup/
    popup.html           Settings UI
    popup.js             Status display, capture toggle, filters
    popup.css            Styling
  icons/                 Extension icons (reuse existing)
```

The existing extension code is deleted and replaced entirely.

### Permissions

```json
{
  "permissions": ["downloads", "contextMenus", "storage", "cookies", "nativeMessaging"],
  "host_permissions": ["<all_urls>"]
}
```

| Permission | Justification |
|-----------|---------------|
| `downloads` | Intercept and cancel browser downloads for handoff |
| `contextMenus` | "Download with Bolt" right-click menu |
| `storage` | Persist capture toggle and filter settings |
| `cookies` | Collect cookies for authenticated downloads |
| `nativeMessaging` | Connect to bolt-host |
| `<all_urls>` | Required for `cookies.getAll()` on arbitrary download URLs |

Intentionally omitted: `webRequest`, `downloads.ui`. No Content-Disposition detection, no download UI suppression in V1.

### Connection State Management

Three explicit states:

| State | Meaning | Extension Behavior |
|-------|---------|-------------------|
| `host_unavailable` | bolt-host binary missing or crashed | Do not interrupt browser downloads |
| `daemon_unavailable` | bolt-host connected but daemon not running | Do not interrupt browser downloads |
| `ready` | Ping succeeded, daemon is alive | Safe to hand off downloads |

**State transitions:**
- Port opened → send `ping` → `ready` or `daemon_unavailable`
- `onDisconnect` fires → `host_unavailable`
- Port opened but `connectNative()` fails immediately → `host_unavailable`

**When to check:**
- Port opened lazily on first need (context menu, auto-capture, popup open)
- One `ping` on port open
- Re-ping on: popup open, service worker startup, before auto-capture if state is stale, after `onDisconnect`
- Avoid pinging on every operation — cache state briefly

### Service Worker (background.js)

Responsibilities:
1. Manage native messaging port (open on demand, reconnect on disconnect)
2. Track connection state (`host_unavailable` | `daemon_unavailable` | `ready`)
3. Register "Download with Bolt" context menu item
4. Handle `downloads.onCreated` when capture is enabled
5. Handle context menu clicks and content script messages
6. Graceful fallback on any failure

### Content Script (content.js)

Responsibilities:
- Listen for clicks on `<a>` tags with href matching download file extensions
- `preventDefault()` and send message to background service worker
- Only active when capture is enabled

### Download Interception Flows

#### Context Menu (always available)

1. User right-clicks a link, selects "Download with Bolt"
2. Background collects: link URL, page URL as referrer, cookies via `cookies.getAll()`
3. Ensure port is connected and healthy (lazy connect + ping if needed)
4. Send `add_download` to bolt-host
5. Success: brief notification ("Sent to Bolt: filename")
6. Failure: open URL as normal browser download, notify ("Bolt unavailable, downloading normally")

#### Automatic Capture (when capture enabled)

1. `downloads.onCreated` fires with a `DownloadItem`
2. Check URL against re-interception prevention set — skip if present
3. Apply filters: extension whitelist/blacklist, domain blocklist, minimum file size (when `totalBytes` is available)
4. If filtered out: let browser download proceed
5. Check connection state — if not `ready` and fresh ping also fails: let browser download proceed
6. Cancel browser download via `downloads.cancel()`
7. Collect cookies for the URL
8. Send `add_download` to bolt-host with URL, referrer, cookies, browser-observed filename (from `DownloadItem.filename` if present)
9. Success: silent (no notification for auto-capture success)
10. Failure: re-initiate browser download via `downloads.download({url})`, track URL in prevention set, notify user

#### Content Script Link Clicks (when capture enabled)

1. User clicks a link matching download extension patterns
2. Content script calls `preventDefault()`, sends message to background
3. Background handles same as context menu path

#### Re-interception Prevention

- `Set<string>` of URLs the extension re-initiated after failed handoff
- `downloads.onCreated` for URLs in this set: skip interception, remove from set
- Prevents loop: intercept, fail, re-download, intercept again

### Notification Policy

- **Context menu success:** brief notification ("Sent to Bolt: filename")
- **Auto-capture success:** silent
- **Any failure:** always notify so user knows fallback happened
- **Daemon policy responses** (duplicate, etc.): surfaced but not alarmist

### Popup UI

Single column, minimal layout:

1. **Status indicator** — colored dot + text label
   - Green "Connected" when `ready`
   - Yellow "Daemon not running" when `daemon_unavailable`
   - Red "Host not installed" when `host_unavailable`
   - Re-pings on popup open for fresh state

2. **Capture toggle** — "Capture downloads automatically"
   - Stored in `chrome.storage.local`
   - Default: `false` (opt-in for automatic capture)
   - Context menu works regardless of this setting

3. **Filters** — collapsible section, collapsed by default
   - Minimum file size: number + KB/MB selector
   - Extension whitelist: comma-separated text input
   - Extension blacklist: comma-separated text input
   - Domain blocklist: comma-separated text input
   - Save button persists to storage

No server URL field, no auth token field, no connection test button.

### Storage Schema

```json
{
  "captureEnabled": false,
  "minFileSize": 0,
  "extensionWhitelist": [],
  "extensionBlacklist": [],
  "domainBlocklist": []
}
```

---

## Testing Strategy

### bolt-host (automated)

- **Unit tests** in `cmd/bolt-host/`:
  - Native messaging read/write: feed raw length-prefixed bytes, assert correct decoding/encoding
  - Command routing: given command JSON, assert correct HTTP request to daemon (via `httptest.Server` on a temp Unix socket)
  - Error paths: daemon unreachable, daemon error responses, malformed stdin input, unknown commands
- **Integration test:** wire up `httptest` server on temp Unix socket, pipe commands through bolt-host stdin/stdout processing, verify end-to-end relay

### Chrome extension (manual)

Test matrix:

| Scenario | Expected Behavior |
|----------|-------------------|
| Context menu with daemon running | Download handed off, success notification |
| Context menu with daemon stopped | Browser downloads normally, failure notification |
| Context menu with bolt-host missing | Browser downloads normally, failure notification |
| Auto-capture with matching filters | Download intercepted and handed off |
| Auto-capture with non-matching filters | Browser download proceeds |
| Auto-capture disabled | No interception, context menu still works |
| Authenticated download | Cookies forwarded, daemon receives them |
| Failed handoff re-download | Browser download resumes, no re-interception loop |
| Popup with all three connection states | Correct status indicator for each |

---

## Explicitly Deferred

These are out of scope for V1:

- WebSocket event forwarding through bolt-host
- Refresh matching in extension
- Content-Disposition network-level detection (`webRequest.onHeadersReceived`)
- Real-time download progress in popup
- Duplicate resolution UX beyond surfacing daemon responses
- Browser-side queue or history UI

---

## Success Criteria

The implementation is successful when:

- A user can right-click a link and send it to Bolt reliably
- Automatic capture works for ordinary downloads when enabled
- Authenticated downloads are handed off with cookies
- If Bolt is unavailable, the browser download proceeds normally
- The popup shows correct connection state and allows configuring capture + filters
- The extension code is materially simpler than the legacy implementation
- bolt-host is testable with standard Go tooling
