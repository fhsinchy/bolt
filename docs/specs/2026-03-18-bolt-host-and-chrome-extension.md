# bolt-host and Chrome Extension — Design Spec

## Overview

Phase 2 adds two components to Bolt:

1. **bolt-host** — a Go native messaging bridge binary that relays commands between the Chrome extension and the Bolt daemon over the Unix socket.
2. **Chrome extension** — a fresh Manifest V3 implementation that hands browser downloads to Bolt through bolt-host.

The extension is a thin handoff client. It captures download intent, collects browser metadata the daemon cannot access, and forwards requests through bolt-host. It does not own downloader policy.

**Parent specs:** `docs/PRD.md` (Phase 2), `docs/TRD.md` (Sections 9.3, 17.1-17.5), `docs/chrome-extension-spec.md`

**Deliberate scope reduction from TRD:** The TRD (Sections 9.3, 17.2) describes bolt-host subscribing to the daemon's WebSocket endpoint and multiplexing async events alongside command responses. This spec defers WebSocket event forwarding entirely. V1 bolt-host is request/response only. The `connectNative` transport supports adding event streaming later without protocol changes — a future version can add event envelope messages to stdout alongside command responses. This deferral aligns with `docs/chrome-extension-spec.md`, which explicitly defers real-time progress in the extension popup.

---

## bolt-host

### Binary and Module

`cmd/bolt-host/main.go` — a separate Go binary in the same module (`github.com/fhsinchy/bolt`). bolt-host may import stable shared types/protocol packages only (currently `internal/model`). It must not import daemon internals like engine, queue, service, or db. Both binaries share a Go module, so `internal/` is accessible to both `cmd/bolt/` and `cmd/bolt-host/`.

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
{"id": "1", "command": "ping"}
{"id": "2", "command": "add_download", "data": {"url": "...", "headers": {"Cookie": "a=1; b=2", "User-Agent": "...", "Referer": "..."}, "referer_url": "..."}}
{"id": "3", "command": "probe", "data": {"url": "...", "headers": {"Cookie": "a=1; b=2"}}}
```

Each request includes an `id` field (string) for correlation. bolt-host echoes it back in the response. V1 is strictly request/response, but the ID field ensures the protocol is ready for async messages without a breaking change — any future unsolicited messages from bolt-host will have no `id`, making them distinguishable from command responses.

Cookies are serialized by the extension into a single `Cookie` header string within the `headers` map (e.g., `"name1=value1; name2=value2"`). User-Agent is included in `headers` as a best-effort compatibility hint (may not exactly match the UA Chrome used for the original request). The `referer_url` field is the page URL for daemon-side storage; the `Referer` header in `headers` is for the actual HTTP request. bolt-host passes the `data` object directly to the daemon's `POST /api/downloads` or `POST /api/probe` endpoint — it does not transform the payload.

**Response envelope:**
```json
{"id": "1", "command": "ping", "success": true, "data": {"version": "0.4.0", "active_count": 2}}
{"id": "2", "command": "add_download", "success": true, "data": {"download": {"id": "01ABC...", "filename": "file.iso", "status": "queued"}}}
{"id": "2", "command": "add_download", "success": false, "error": "duplicate_filename", "data": {"error": "file already exists", "code": "DUPLICATE_FILENAME"}}
```

Every response includes the `id` echoed from the request, the originating `command` name, a `success` boolean, and either `data` (on success) or `error` plus optional `data` (on failure).

### Error Mapping

bolt-host translates daemon HTTP responses to the command protocol. The daemon already includes a `code` field in error responses (e.g., `DUPLICATE_FILENAME`, `VALIDATION_ERROR`, `NOT_FOUND`). bolt-host maps these as follows:

- HTTP 2xx → `{"success": true, "data": <response body>}`
- HTTP 4xx/5xx → `{"success": false, "error": <daemon code field, lowercased>, "data": <daemon response body>}`
- Connection refused / socket missing → `{"success": false, "error": "daemon_unavailable"}`
- Request timeout → `{"success": false, "error": "timeout"}`

The `error` field uses the daemon's own error codes (lowercased) so the extension can act on specific errors (e.g., `duplicate_filename`) without bolt-host inventing its own error taxonomy.

### Message Size Limit

Chrome's native messaging protocol has a 1 MB per-message limit. All expected command/response payloads are well under this (typical add_download request is under 2 KB, largest response is a download object at under 4 KB). bolt-host does not need special handling for this limit but should log and return an error if a daemon response exceeds 1 MB.

### Supported Commands (V1)

| Command | Daemon Endpoint | Purpose |
|---------|----------------|---------|
| `ping` | `GET /api/stats` | Health check + version |
| `add_download` | `POST /api/downloads` | Hand off a download |
| `probe` | `POST /api/probe` | Check URL metadata before adding |

### Internal Structure

- **Command handler goroutine:** reads length-prefixed messages from stdin, decodes JSON, makes HTTP request to daemon via Unix socket, writes length-prefixed response to stdout
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
- `allowed_origins` requires the extension's Chrome Web Store ID. For development, `manifest.json` includes a `key` field (a public RSA key) that makes the extension ID deterministic across unpacked loads. The corresponding extension ID is hardcoded in `make install` for manifest generation. When published to the Chrome Web Store, the store assigns the real ID and the manifest must be updated accordingly.
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
  "permissions": ["downloads", "contextMenus", "storage", "cookies", "nativeMessaging", "notifications"],
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
| `notifications` | Success/failure notifications for download handoff |
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
- Ping times out → `daemon_unavailable` (not `host_unavailable` — the host process is running, the daemon is not responding)
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
- The content script is statically declared in MV3 and injected into all pages, but only intercepts clicks when capture is enabled (checks storage before acting)

**File extension matching:** The content script uses a hardcoded list of common downloadable file extensions (e.g., `.zip`, `.tar.gz`, `.iso`, `.deb`, `.rpm`, `.exe`, `.msi`, `.dmg`, `.pdf`, `.7z`, `.rar`, `.AppImage`). This list is separate from the popup's whitelist/blacklist filters — those filters are applied by the background service worker on the `downloads.onCreated` path only. The content script list is deliberately broad and static; it exists only to identify links that are likely downloads rather than navigation. Web resource extensions (`.html`, `.js`, `.css`, `.json`, `.xml`) and image extensions are excluded.

**Filter coverage by entry point:** Not all filters apply to all interception paths. Size-based filtering only works on the `downloads.onCreated` path where Chrome provides `totalBytes`. The content script and context menu paths do not know file size before handoff — the daemon probes the URL and determines size. Extension whitelist/blacklist and domain blocklist are applied by the background service worker and work on all paths.

### Download Interception Flows

#### Context Menu (always available)

1. User right-clicks a link, selects "Download with Bolt"
2. Background collects: link URL, page URL as referrer, cookies via `cookies.getAll()`
3. Ensure port is connected and healthy (lazy connect + ping if needed)
4. Send `add_download` to bolt-host
5. Success: brief notification ("Sent to Bolt: filename")
6. Failure: initiate browser download via `chrome.downloads.download({url})`, notify ("Bolt unavailable, downloading normally")

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
- **Any failure:** notify so user knows fallback happened. During auto-capture, if repeated failures occur (e.g., daemon goes down mid-session), suppress repeated notifications after the first — one "Bolt unavailable" notification is enough until state changes back to `ready`.
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
