# bolt-qt GUI Design Spec

**Goal:** Qt6 desktop GUI for the Bolt download manager daemon. Thin client — all state lives in the daemon. Communicates via REST over Unix socket.

**Scope (V1):**
- Download list with progress, speed, ETA, status
- Add URL dialog with probing
- Pause/Resume/Retry/Delete controls
- Settings dialog (max concurrent, segments, speed limit, retries, notifications)
- Disconnected-state handling with auto-reconnect

**Out of scope (V1):** WebSocket live updates (poll instead), system tray icon, single-instance raise-window, download categories, drag-and-drop reorder, per-download speed limit editing after creation, URL refresh dialog, checksum configuration in add dialog. These are candidates for V2.

---

## Architecture

bolt-qt is a standalone C++17/Qt6 binary. Thin client — the daemon owns all state. The GUI polls the daemon's REST API over a Unix socket. No WebSocket, no direct file/DB access.

### Components

```
bolt-qt/src/
  main.cpp                 Entry point, QApplication setup
  types.h                  Data structs + JSON parsing
  daemonclient.h/.cpp      REST client over QLocalSocket
  downloadlistmodel.h/.cpp QAbstractTableModel for download list
  progressdelegate.h/.cpp  Progress bar column delegate
  mainwindow.h/.cpp        Main window (toolbar, table, status bar)
  adddownloaddialog.h/.cpp Add URL dialog with probing
  settingsdialog.h/.cpp    Settings dialog
```

### Data Flow

1. `DaemonClient` connects to the daemon's Unix socket, sends HTTP requests, parses JSON responses
2. A 1-second `QTimer` polls `GET /api/downloads` to keep the model current
3. `DownloadListModel` holds a `QVector<Download>` — each poll replaces it, preserving transient UI state (selection)
4. UI actions call `DaemonClient` methods, which fire HTTP requests; on success, an immediate poll is triggered
5. On disconnect, polling pauses, status bar shows "Disconnected", a 3-second retry timer attempts reconnection

### Transport: REST over Unix Socket

Single `QLocalSocket` connection. HTTP/1.1 requests are serialized (one at a time, queued internally). No WebSocket — V1 uses 1-second polling for progress updates.

**Why not WebSocket:** `QWebSocket` does not support Unix domain sockets. Implementing RFC 6455 framing manually is fragile and test-heavy. Polling at 1s over a local socket is negligible overhead and drastically simpler. WebSocket can be added later as an optimization if polling proves insufficient.

**Socket path:** `$XDG_RUNTIME_DIR/bolt/bolt.sock`, fallback `/tmp/bolt-<uid>/bolt.sock`.

**QLocalSocket note:** On Linux, `QLocalSocket::connectToServer(name)` treats the name as an abstract socket by default. To connect to a filesystem Unix socket, use `QLocalSocket::setServerName(fullPath)` with the full path then `connectToServer()`, or connect using the path directly. The implementation must use the filesystem path, not an abstract socket name.

**Assumption:** The daemon always sends `Content-Length` headers (no chunked transfer encoding). If a future daemon change introduces chunked responses, the parser will need updating.

---

## Types (types.h)

C++ structs with `static fromJson(QJsonObject)` factory methods. All `fromJson` methods read from **snake_case** JSON keys (matching the daemon's Go JSON tags) and map to camelCase C++ fields. Unknown JSON fields are silently ignored.

### JSON Field Name Mapping Convention

The daemon serializes Go structs with `json:"snake_case"` tags. C++ structs use camelCase. The `fromJson` methods handle this mapping:
- `obj["total_size"].toInteger()` → `download.totalSize`
- `obj["segments"].toInt()` → `download.segmentCount` (note: daemon JSON key is `"segments"`, not `"segment_count"`)
- `obj["download_dir"].toString()` → `config.downloadDir`

```cpp
struct Download {
    QString id;
    QString url;
    QString filename;
    QString dir;
    qint64 totalSize;       // JSON: "total_size"
    qint64 downloaded;      // JSON: "downloaded"
    QString status;         // JSON: "status" — "queued"|"active"|"paused"|"completed"|"error"|"refresh"|"verifying"
    int segmentCount;       // JSON: "segments" (not "segment_count")
    qint64 speedLimit;      // JSON: "speed_limit"
    QString error;          // JSON: "error"
    QDateTime createdAt;    // JSON: "created_at"
    QDateTime completedAt;  // JSON: "completed_at" (null if not completed)
    int queueOrder;         // JSON: "queue_order"

    static Download fromJson(const QJsonObject &obj);
};

struct AddRequest {
    QString url;
    QString filename;       // optional — daemon probes if empty
    QString dir;            // optional — daemon uses config default
    int segments = 0;       // 0 = use config default
    qint64 speedLimit = 0;  // 0 = unlimited
    bool force = false;     // if true, ignore duplicate filename

    QJsonObject toJson() const;
    // Produces: {"url": "...", "filename": "...", "dir": "...", "segments": N, "speed_limit": N, "force": bool}
    // Omits empty/zero fields
};

struct ProbeResult {
    QString filename;       // JSON: "filename"
    qint64 totalSize;       // JSON: "total_size"
    bool acceptsRanges;     // JSON: "accepts_ranges"
    QString finalUrl;       // JSON: "final_url"
    QString contentType;    // JSON: "content_type"
    // Note: daemon also returns "etag" and "last_modified" — silently ignored

    static ProbeResult fromJson(const QJsonObject &obj);
};

struct Config {
    QString downloadDir;       // JSON: "download_dir"
    int maxConcurrent;         // JSON: "max_concurrent"
    int defaultSegments;       // JSON: "default_segments"
    qint64 globalSpeedLimit;   // JSON: "global_speed_limit"
    bool notifications;        // JSON: "notifications"
    int maxRetries;            // JSON: "max_retries"
    qint64 minSegmentSize;     // JSON: "min_segment_size"

    static Config fromJson(const QJsonObject &obj);
};

struct Stats {
    int activeCount;      // JSON: "active_count"
    int queuedCount;      // JSON: "queued_count"
    int completedCount;   // JSON: "completed_count"
    int totalCount;       // JSON: "total_count"
    QString version;      // JSON: "version"

    static Stats fromJson(const QJsonObject &obj);
};
```

### Status Display Mapping

| Daemon status | Display text |
|---------------|-------------|
| `queued` | Queued |
| `active` | Downloading |
| `paused` | Paused |
| `completed` | Completed |
| `error` | Error |
| `refresh` | Needs Refresh |
| `verifying` | Verifying |

### Toolbar Button Behavior by Status

| Status | Pause | Resume | Retry | Delete |
|--------|-------|--------|-------|--------|
| `queued` | — | — | — | yes |
| `active` | yes | — | — | yes |
| `paused` | — | yes | — | yes |
| `completed` | — | — | — | yes |
| `error` | — | — | yes | yes |
| `refresh` | — | — | — | yes |
| `verifying` | — | — | — | yes |

`refresh` and `verifying` downloads have no actionable controls in V1 (URL refresh dialog is out of scope). They can only be deleted.

---

## DaemonClient

Owns a `QLocalSocket`, sends HTTP/1.1 requests, parses responses, emits signals.

### Connection Lifecycle

1. On construction, attempt to connect to the daemon socket
2. On success: emit `connected()`, start 1-second poll timer
3. On disconnect: emit `disconnected()`, stop poll timer, start 3-second reconnect timer
4. On reconnect success: emit `connected()`, resume polling

### HTTP over Unix Socket

Write request line + headers + body to `QLocalSocket`, read response status + headers + body. `Content-Length` based.

Parsing: a small state machine driven by `QLocalSocket::readyRead`:
1. Read lines until `\r\n\r\n` (end of headers)
2. Extract `Content-Length` from headers
3. Read exactly that many bytes for the body
4. Parse status code from first line

One request at a time. Pending requests queued in a `QQueue`.

### API

```cpp
// Requests — all async
void fetchDownloads();                                    // GET /api/downloads
void addDownload(const AddRequest &req);                  // POST /api/downloads — body: req.toJson()
void deleteDownload(const QString &id, bool deleteFile);  // DELETE /api/downloads/{id}?delete_file=true
void pauseDownload(const QString &id);                    // POST /api/downloads/{id}/pause
void resumeDownload(const QString &id);                   // POST /api/downloads/{id}/resume
void retryDownload(const QString &id);                    // POST /api/downloads/{id}/retry
void probeUrl(const QString &url);                        // POST /api/probe — body: {"url": "<value>"}
void fetchConfig();                                       // GET /api/config
void updateConfig(const QJsonObject &partial);            // PUT /api/config
void fetchStats();                                        // GET /api/stats

signals:
    void connected();
    void disconnected();
    void downloadsFetched(QVector<Download> list);
    void downloadAdded(Download dl);
    void probeCompleted(ProbeResult result);
    void probeFailed(QString error);
    void configFetched(Config cfg);
    void configUpdated();   // PUT /api/config succeeded
    void statsFetched(Stats stats);
    void requestFailed(QString endpoint, int statusCode, QString errorCode, QString errorMessage);
```

### Action Result Handling

Action methods (`pauseDownload`, `resumeDownload`, `retryDownload`, `deleteDownload`) do not have individual success signals. On success, they trigger an immediate `fetchDownloads()` to refresh the model. On failure, they emit `requestFailed`. The caller (MainWindow) connects to `requestFailed` to show error messages.

### Response Envelope Parsing

Daemon wraps responses — `DaemonClient` unwraps internally:

| Endpoint | Envelope | Extract |
|----------|----------|---------|
| `GET /api/downloads` | `{"downloads": [...], "total": N}` | `downloads` array |
| `POST /api/downloads` | `{"download": {...}}` | `download` object |
| `DELETE /api/downloads/{id}` | `{"status": "deleted"}` | success indicator |
| `POST .../pause\|resume\|retry` | `{"status": "..."}` | success indicator |
| `POST /api/probe` | flat `ProbeResult` | direct |
| `GET /api/config` | flat `Config` | direct |
| `PUT /api/config` | `{"status": "updated"}` | success indicator |
| `GET /api/stats` | flat `Stats` | direct |
| Error responses | `{"error": "msg", "code": "CODE"}` | `error` + `code` |
| Duplicate on add | `{"code": "DUPLICATE_FILENAME", "error": "msg", "existing": {...}}` | all fields |

### Polling

A `QTimer` fires every 1 second and calls `fetchDownloads()`. The response replaces the model contents. Polling is active only while connected.

V1 uses a fixed 1-second interval for simplicity. Adaptive polling (slower when no active downloads) is a future optimization.

---

## DownloadListModel

`QAbstractTableModel` with these columns:

| Column | Data | Notes |
|--------|------|-------|
| Filename | `download.filename` | Elided if too long |
| Size | `download.totalSize` | Formatted: "1.5 GB", or "Unknown" if 0 |
| Progress | `download.downloaded / totalSize` | Progress bar via delegate |
| Speed | Derived from download delta | "10.5 MB/s" or blank if not active |
| ETA | Derived from speed + remaining | "2h30m" or blank |
| Status | `download.status` | Display text per mapping table |

### Speed and ETA Calculation

Since V1 polls instead of receiving WebSocket speed/ETA data, the model calculates these locally:
- **Speed:** `(current.downloaded - previous.downloaded) / pollInterval` for each active download, comparing the current poll response against the previous one. Smoothed with a simple exponential moving average (alpha = 0.3) to avoid jitter.
- **ETA:** `(totalSize - downloaded) / speed` when speed > 0, blank otherwise.

The model keeps a `QHash<QString, qint64>` of previous `downloaded` values, updated each poll cycle.

### Update Strategy

On each `downloadsFetched` signal:
1. Build a `QHash<QString, int>` of new downloads keyed by ID
2. Remove rows whose IDs are absent (download deleted externally)
3. Update existing rows in-place (patch fields, emit `dataChanged`)
4. Insert new rows (download added externally, e.g., via Chrome extension)
5. This handles de-duplication naturally — the poll response is the single source of truth, and IDs are unique

### Progress Delegate

`QStyledItemDelegate` for the Progress column. Draws a native `QProgressBar` via `QStyle::drawControl(CE_ProgressBar, ...)`. No custom painting.

### Selection

Standard `QTableView` with multi-row selection. Selected rows drive toolbar button enabled state.

When multi-selecting downloads with mixed statuses (e.g., 1 active + 1 paused + 1 completed), toolbar buttons apply only to applicable downloads. For example, clicking Pause sends `pauseDownload()` only for the active ones. The daemon returns errors for non-applicable downloads, which are silently ignored.

---

## MainWindow

Toolbar at top, table view in center, status bar at bottom.

### Toolbar

| Button | Icon (freedesktop) | Action | Enabled when |
|--------|-------------------|--------|-------------|
| Add URL | `list-add` | Opens AddDownloadDialog | Always |
| Pause | `media-playback-pause` | `pauseDownload()` on applicable selected | Selection has active downloads |
| Resume | `media-playback-start` | `resumeDownload()` on applicable selected | Selection has paused downloads |
| Retry | `view-refresh` | `retryDownload()` on applicable selected | Selection has error downloads |
| Delete | `edit-delete` | Confirmation dialog, then `deleteDownload()` | Any selection |
| Settings | `configure` | Opens SettingsDialog | Always |

Icons use `QIcon::fromTheme()` — native system icons.

### Delete Confirmation

The delete confirmation dialog includes a "Also delete downloaded file" checkbox (unchecked by default). This maps to the `delete_file` query parameter.

### Status Bar

- Connection indicator: "Connected" / "Disconnected — retrying..." / "Connecting..."
- Active download count: "3 downloading"
- Total speed: "25.4 MB/s" (sum of model's calculated speeds)

### Window Behavior

- Close button quits the application (no tray in V1)
- Window geometry saved/restored via `QSettings`
- Title: "Bolt Download Manager"

### State Handling

| State | Behavior |
|-------|----------|
| **First launch, daemon running** | Connect, poll, populate list |
| **First launch, daemon not running** | Show empty list, status bar "Disconnected — retrying...", retry every 3s |
| **Daemon disconnects while running** | Stop polling, status bar "Disconnected — retrying...", model stays visible (stale), retry every 3s |
| **Daemon reconnects** | Resume polling, model refreshed on first poll, status bar "Connected" |
| **Daemon restarts (new PID)** | Same as reconnect — next successful poll replaces model entirely |
| **Empty download list** | Show centered placeholder text: "No downloads yet. Click + to add one." |

---

## AddDownloadDialog

### Layout

```
URL:  [_________________________________] [Probe]

-- Probe Results --
Filename: [ubuntu-24.04-desktop-amd64.iso ]
Size:     6.2 GB
Resumable: Yes

-- Options --
Save to:    [~/Downloads              ] [Browse]
Segments:   [16]

                           [Cancel]  [Download]
```

### Flow

1. User pastes URL, clicks Probe (or auto-probe on Enter/paste with debounce)
2. `DaemonClient::probeUrl()` fires (sends `{"url": "<value>"}` as POST body), Probe button shows spinner
3. On `probeCompleted`: populate filename, size, resumable. Enable Download button.
4. On `probeFailed`: show inline error label, Download button still enabled (daemon re-probes on add)
5. User clicks Download → `DaemonClient::addDownload()` with `AddRequest`
6. On success (201): dialog closes
7. On `DUPLICATE_FILENAME` (409): show "File already exists. Download anyway?" with Yes (sets `force=true`, retries) / No
8. On other error: show inline error message

### Defaults

Segment count and save directory pre-filled from daemon config (fetched on dialog open via `fetchConfig()`).

### Clipboard Detection

On dialog open, check `QClipboard` for text starting with `http://` or `https://` and pre-fill the URL field.

---

## SettingsDialog

### Layout

```
Download directory: [~/Downloads     ] [Browse]
Max concurrent:     [3  ]  (1-10)
Default segments:   [16 ]  (1-32)
Global speed limit: [0       ] MB/s  (0 = unlimited)
Max retries:        [10 ]  (0-100)
Min segment size:   [1       ] MB
[x] Desktop notifications

                     [Cancel]  [Save]
```

### Behavior

- On open: `fetchConfig()` populates all fields
- On Save: diff current vs fetched, send only changed fields via `PUT /api/config`
- On success: emit `configUpdated()`, close dialog
- On failure: show inline error, don't close
- Cancel discards without sending
- If disconnected when Save is clicked: show error, don't close

---

## Build System

### CMakeLists.txt

- Qt6 components: `Core Gui Widgets Network`
- C++17 standard
- Source files in `bolt-qt/src/`

### Entry Point (main.cpp)

1. `QApplication` (standard quit-on-close behavior — no tray)
2. Create `DaemonClient`
3. Create `MainWindow` (takes `DaemonClient*`)
4. Show main window, enter event loop

### Makefile Integration

- `make build-qt` — cmake configure + build in `bolt-qt/build/`
- `make install` — copies `bolt-qt` to `~/.local/bin/`
- `make build-all` — includes `build-qt`
