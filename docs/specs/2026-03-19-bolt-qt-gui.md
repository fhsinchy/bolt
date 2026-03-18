# bolt-qt GUI Design Spec

**Goal:** Qt6 desktop GUI for the Bolt download manager daemon. Thin client — all state lives in the daemon. Communicates via REST + WebSocket over Unix socket.

**Scope (V1):**
- Download list with progress, speed, ETA
- Add URL dialog with probing
- Pause/Resume/Retry/Delete controls
- Queue management (max concurrent)
- Global speed limiter
- Settings dialog
- System tray icon (minimize to tray)
- Native system theme (no custom styling)

**Out of scope (V1):** Download categories/folders, download-complete dialog, drag-and-drop queue reorder, tray icon badges/overlays, per-download speed limit editing after creation.

---

## Architecture

bolt-qt is a standalone C++17/Qt6 binary. It is a thin client — the daemon owns all download state. The GUI never touches files, SQLite, or download logic.

### Components

```
bolt-qt/src/
  main.cpp                 Entry point, QApplication setup
  types.h                  Data structs (Download, AddRequest, ProbeResult, Config, Stats) + JSON parsing
  unixhttpclient.h/.cpp    HTTP/1.1 client over QLocalSocket (request queue, response parsing)
  daemonclient.h/.cpp      DaemonClient class (REST + WebSocket, signals)
  downloadlistmodel.h/.cpp DownloadListModel class
  progressdelegate.h/.cpp  QStyledItemDelegate for progress bar column
  mainwindow.h/.cpp        MainWindow class
  adddownloaddialog.h/.cpp AddDownloadDialog class
  settingsdialog.h/.cpp    SettingsDialog class
  trayicon.h/.cpp          TrayIcon class
```

### Data Flow

1. `DaemonClient` connects to the daemon's Unix socket via `UnixHttpClient`, sends HTTP requests, receives JSON responses
2. On connect, it fetches `GET /api/downloads` to populate the model, then opens a second `QLocalSocket` for WebSocket (manual HTTP upgrade + raw frame parsing)
3. `DownloadListModel` holds a `QVector<Download>` — REST responses replace entries, WebSocket progress updates patch them in-place
4. UI actions (pause, resume, etc.) call `DaemonClient` methods which fire HTTP requests; responses update the model
5. On disconnect, the model stays populated (stale but visible), status bar shows "Disconnected", client retries every 3 seconds

### Connection to Daemon

Two `QLocalSocket` instances:
- **REST socket:** serialized HTTP/1.1 requests via `UnixHttpClient`, one at a time, queued internally
- **WebSocket socket:** manual HTTP upgrade handshake over `QLocalSocket`, then raw WebSocket frame reading (RFC 6455 text frames only — the daemon sends JSON text frames). `QWebSocket` is not used because it does not support Unix domain sockets. The `Qt6::WebSockets` CMake dependency is removed.

Socket path: `$XDG_RUNTIME_DIR/bolt/bolt.sock`, fallback `/tmp/bolt-<uid>/bolt.sock`.

No shared code with the Go daemon. Types are redefined in C++ structs. JSON parsing uses `QJsonDocument`.

---

## Types (types.h)

C++ structs with `static fromJson(QJsonObject)` factory methods.

```cpp
struct Download {
    QString id;
    QString url;
    QString filename;
    QString dir;
    qint64 totalSize;
    qint64 downloaded;
    QString status;      // "queued"|"active"|"paused"|"completed"|"error"|"refresh"|"verifying"
    int segmentCount;
    qint64 speedLimit;
    QMap<QString,QString> headers;
    QString refererUrl;
    QString error;
    QString etag;
    QString lastModified;
    QDateTime createdAt;
    QDateTime completedAt; // null if not completed
    int queueOrder;

    // Transient fields (from WebSocket, not persisted)
    qint64 speed = 0;    // bytes/sec
    int eta = 0;          // seconds remaining

    static Download fromJson(const QJsonObject &obj);
};

struct AddRequest {
    QString url;
    QString filename;    // optional, daemon probes if empty
    QString dir;         // optional, daemon uses config default
    int segments = 0;    // 0 = use config default
    QMap<QString,QString> headers;
    QString refererUrl;
    qint64 speedLimit = 0;
    bool force = false;  // if true, ignore duplicate filename

    QJsonObject toJson() const;
};

struct ProbeResult {
    QString filename;
    qint64 totalSize;
    bool acceptsRanges;
    QString etag;
    QString lastModified;
    QString finalUrl;
    QString contentType;

    static ProbeResult fromJson(const QJsonObject &obj);
};

struct Config {
    QString downloadDir;
    int maxConcurrent;
    int defaultSegments;
    qint64 globalSpeedLimit;
    bool notifications;
    int maxRetries;
    qint64 minSegmentSize;

    static Config fromJson(const QJsonObject &obj);
};

struct Stats {
    int activeCount;
    int queuedCount;
    int completedCount;
    int totalCount;
    QString version;

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

---

## UnixHttpClient

Helper class that owns a `QLocalSocket` and provides a queued, async HTTP/1.1 client.

```cpp
class UnixHttpClient : public QObject {
    Q_OBJECT
public:
    UnixHttpClient(const QString &socketPath, QObject *parent = nullptr);
    void connectToServer();
    bool isConnected() const;

    // Enqueue a request. Callback fires with status code + body.
    using Callback = std::function<void(int statusCode, QByteArray body)>;
    void request(const QByteArray &method, const QByteArray &path,
                 const QByteArray &body, Callback cb);

signals:
    void connected();
    void disconnected();
    void connectionFailed();

private:
    // Internal: write request, read status line + headers + Content-Length body
    // One request at a time; queue pending requests
    QLocalSocket *m_socket;
    QQueue<PendingRequest> m_queue;
    // Parser state: reading status line → headers → body
};
```

Parsing strategy: read lines until `\r\n\r\n` for headers, extract `Content-Length`, then read exactly that many bytes for the body. No chunked transfer (the daemon always sends `Content-Length`). The parser is a simple state machine driven by `QLocalSocket::readyRead`.

---

## DaemonClient

Central communication layer. Owns the `UnixHttpClient` for REST and a second `QLocalSocket` for WebSocket.

### Connection Lifecycle

- On construction, starts a connect attempt via `UnixHttpClient`
- On success: emits `connected()`, fetches stats via `GET /api/stats` to confirm daemon is alive, then opens a second `QLocalSocket` for WebSocket (manual HTTP upgrade handshake)
- On disconnect: emits `disconnected()`, starts a `QTimer` that retries every 3 seconds

### WebSocket Implementation

The WebSocket connection is a raw `QLocalSocket` that:
1. Sends an HTTP upgrade request: `GET /ws HTTP/1.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: <base64>\r\nSec-WebSocket-Version: 13\r\n\r\n`
2. Reads the `101 Switching Protocols` response
3. Reads RFC 6455 text frames (the daemon only sends unmasked text frames with JSON payloads)
4. Emits typed signals for each event type

Client-to-server frames (if needed) must be masked per RFC 6455. In V1 the client does not send WebSocket frames — it is read-only.

### REST API

```cpp
// Request methods — all async, emit signals with results
void fetchDownloads();                           // GET /api/downloads
void fetchDownload(const QString &id);           // GET /api/downloads/{id}
void addDownload(const AddRequest &req);         // POST /api/downloads
void deleteDownload(const QString &id, bool deleteFile); // DELETE /api/downloads/{id}?delete_file=true
void pauseDownload(const QString &id);           // POST /api/downloads/{id}/pause
void resumeDownload(const QString &id);          // POST /api/downloads/{id}/resume
void retryDownload(const QString &id);           // POST /api/downloads/{id}/retry
void probeUrl(const QString &url, const QMap<QString,QString> &headers); // POST /api/probe
void fetchConfig();                              // GET /api/config
void updateConfig(const QJsonObject &partial);   // PUT /api/config
void fetchStats();                               // GET /api/stats
```

### Response Envelope Parsing

The daemon wraps responses in envelopes:
- `GET /api/downloads` → `{"downloads": [...], "total": N}` — extract `downloads` array
- `GET /api/downloads/{id}` → `{"download": {...}, "segments": [...]}` — extract `download` object
- `POST /api/downloads` → `{"download": {...}}` — extract `download` object
- `POST /api/probe` → flat `ProbeResult` object (no wrapper)
- `GET /api/config` → flat `Config` object
- `GET /api/stats` → flat `Stats` object
- Error responses → `{"error": "message", "code": "CODE"}` — extract `error` and `code`

`DaemonClient` handles all envelope unwrapping internally and emits clean typed signals.

### Signals

```cpp
signals:
    void connected();
    void disconnected();
    void downloadsFetched(QVector<Download> list);
    void downloadFetched(Download dl);
    void downloadAdded(Download dl);
    void probeResult(ProbeResult result);
    void configFetched(Config cfg);
    void statsFetched(Stats stats);
    void requestFailed(QString endpoint, int statusCode, QString errorCode, QString errorMessage);

    // WebSocket events
    void progressUpdated(QString id, qint64 downloaded, qint64 total, qint64 speed, int eta, QString status);
    void downloadCompleted(QString id);
    void downloadFailed(QString id, QString error);
    void downloadPaused(QString id);
    void downloadResumed(QString id);
    void wsDownloadAdded(QString id, QString url, QString filename);
    void downloadRemoved(QString id);
```

Note: `wsDownloadAdded` carries partial data (only `id`, `url`, `filename` from the daemon's broadcast). The model must call `fetchDownload(id)` to get the full record before inserting a row.

---

## DownloadListModel

`QAbstractTableModel` with these columns:

| Column | Data | Notes |
|--------|------|-------|
| Filename | `download.filename` | Elided if too long |
| Size | `download.totalSize` | Formatted: "1.5 GB" |
| Progress | `download.downloaded / totalSize` | Rendered as progress bar via delegate |
| Speed | `download.speed` | "10.5 MB/s" or blank if not active |
| ETA | `download.eta` | "2h30m" or blank |
| Status | `download.status` | Display text per status mapping table |

### Update Strategy

- `downloadsFetched` signal: full replace of internal `QVector<Download>`
- `progressUpdated` signal: find by ID, patch `downloaded`/`speed`/`eta`/`status` fields, emit `dataChanged` for that row
- `downloadCompleted`/`downloadFailed`/`downloadPaused`/`downloadResumed`: update status field
- `downloadRemoved`: `beginRemoveRows`/`endRemoveRows`
- `wsDownloadAdded`: call `DaemonClient::fetchDownload(id)`, on `downloadFetched` response insert the full row via `beginInsertRows`/`endInsertRows`

### Progress Delegate

A `QStyledItemDelegate` for the Progress column that draws a native `QProgressBar` style via `QStyle::drawControl(CE_ProgressBar, ...)`. No custom painting.

### Selection

Standard `QTableView` with single/multi-row selection. Selected rows drive toolbar button state.

---

## MainWindow

Classic download manager layout: toolbar at top, table view filling the center, status bar at bottom.

### Toolbar

| Button | Icon (freedesktop) | Action | Enabled when |
|--------|-------------------|--------|-------------|
| Add URL | `list-add` | Opens AddDownloadDialog | Always |
| Pause | `media-playback-pause` | `pauseDownload()` on selected | Selection has active downloads |
| Resume | `media-playback-start` | `resumeDownload()` on selected | Selection has paused downloads |
| Retry | `view-refresh` | `retryDownload()` on selected | Selection has failed downloads |
| Delete | `edit-delete` | `deleteDownload()` with confirmation | Any selection |
| Settings | `configure` | Opens SettingsDialog | Always |

Icons use `QIcon::fromTheme()` for native system theme integration.

### Status Bar

Left to right:
- Connection indicator (green/red dot with "Connected"/"Disconnected" text)
- Active download count: "3 active"
- Global speed: "25.4 MB/s" (sum of all active speeds from WebSocket updates)

### Window Behavior

- Close button hides to tray (`hide()` + `event->ignore()`)
- Window geometry saved/restored via `QSettings`
- Title: "Bolt Download Manager"

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
Speed limit: [0       ] MB/s  (0 = unlimited)

                           [Cancel]  [Download]
```

### Flow

1. User pastes URL, clicks Probe (or auto-probe after debounce)
2. `DaemonClient::probeUrl()` fires, dialog shows spinner
3. On `probeResult` signal: populate filename, size, resumable indicator
4. If probe fails: show inline error, user can still proceed
5. User adjusts options, clicks Download
6. `DaemonClient::addDownload()` fires with an `AddRequest`
7. On success: dialog closes
8. On `DUPLICATE_FILENAME` error: show message with option to force-add (`AddRequest.force = true`)

### Defaults

Segment count and save directory from daemon config (fetched on dialog open). Speed limit defaults to 0 (unlimited).

### Clipboard Detection

On dialog open, check `QClipboard` for a URL (starts with `http://` or `https://`) and pre-fill the URL field.

---

## SettingsDialog

### Layout

```
Download directory: [~/Downloads     ] [Browse]
Max concurrent:     [3  ]  (1-10)
Default segments:   [16 ]  (1-32)
Global speed limit: [0       ] MB/s
Max retries:        [10 ]  (0-100)
Min segment size:   [1       ] MB  (min 64 KB)
[x] Desktop notifications

                     [Cancel]  [Save]
```

### Behavior

- On open: `fetchConfig()` populates all fields
- On Save: diff current vs fetched, send only changed fields via `PUT /api/config`
- Changes take effect immediately in the daemon
- Cancel discards without sending

---

## TrayIcon

- `QSystemTrayIcon` with the bolt app icon
- Left-click: toggle window show/hide
- Context menu: Show/Hide, Quit
- `QApplication::setQuitOnLastWindowClosed(false)`
- Quit only happens from tray menu

No badge/count/speed overlay in V1.

---

## Build System

### CMakeLists.txt

- Qt6 components: `Core Gui Widgets Network` (no WebSockets — raw socket implementation)
- C++17 standard
- All source files in `bolt-qt/src/`

### Entry Point (main.cpp)

1. `QApplication` with `setQuitOnLastWindowClosed(false)`
2. Single-instance check via `QLockFile` in `$XDG_RUNTIME_DIR/bolt/bolt-qt.lock`
3. Create `DaemonClient`, `MainWindow`, `TrayIcon`
4. Show main window, enter event loop

### Makefile Integration

- `make build-qt` runs cmake configure + build
- `make install` copies `bolt-qt` to `~/.local/bin/`
- `make build-all` includes `build-qt`

---

## Task Dependency Graph

```
#13 Build system & entry point ──┐
                                  ├── #7 DaemonClient (depends on UnixHttpClient)
#10 AddDownloadDialog ───────────┤
#11 SettingsDialog ──────────────┤
                                  ├── #8 DownloadListModel ── #9 MainWindow ── #12 TrayIcon
```

All components depend on DaemonClient (#7). MainWindow depends on DownloadListModel. TrayIcon depends on MainWindow.
