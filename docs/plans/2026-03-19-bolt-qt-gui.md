# bolt-qt GUI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Qt6 desktop GUI for the Bolt download manager daemon — thin client that polls REST over Unix socket.

**Architecture:** Standalone C++17/Qt6 binary. Single `QLocalSocket` sends HTTP/1.1 requests to the daemon's Unix socket. A 1-second poll timer refreshes the download list. All state lives in the daemon; the GUI only displays and sends commands.

**Tech Stack:** C++17, Qt6 (Core, Gui, Widgets, Network), CMake

**Spec:** `docs/specs/2026-03-19-bolt-qt-gui.md`

---

## File Structure

```
bolt-qt/
  CMakeLists.txt               Build configuration (update existing)
  src/
    main.cpp                   Entry point, QApplication setup
    types.h                    Data structs (Download, AddRequest, ProbeResult, Config, Stats) + JSON
    daemonclient.h             DaemonClient class declaration
    daemonclient.cpp           DaemonClient implementation (socket, HTTP, polling)
    downloadlistmodel.h        DownloadListModel class declaration
    downloadlistmodel.cpp      DownloadListModel implementation
    progressdelegate.h         ProgressDelegate class declaration
    progressdelegate.cpp       ProgressDelegate implementation
    mainwindow.h               MainWindow class declaration
    mainwindow.cpp             MainWindow implementation (toolbar, table, status bar)
    adddownloaddialog.h        AddDownloadDialog class declaration
    adddownloaddialog.cpp      AddDownloadDialog implementation
    settingsdialog.h           SettingsDialog class declaration
    settingsdialog.cpp         SettingsDialog implementation
```

### Modified Files

```
bolt-qt/CMakeLists.txt         Update: add source files, remove WebSockets dependency
Makefile                        Update: make build-qt actually builds, install copies bolt-qt
CLAUDE.md                       Update: add bolt-qt to package layout and commands
```

---

### Task 1: Build system and types

**Files:**
- Modify: `bolt-qt/CMakeLists.txt`
- Create: `bolt-qt/src/main.cpp`
- Create: `bolt-qt/src/types.h`

- [ ] **Step 1: Update CMakeLists.txt**

Replace the existing scaffold. Remove `WebSockets` from `find_package`. Uncomment and update `add_executable` and `target_link_libraries` with just `main.cpp` for now.

- [ ] **Step 2: Create types.h**

Create `bolt-qt/src/types.h` — header-only file with all data structs (`Download`, `AddRequest`, `ProbeResult`, `Config`, `Stats`) plus `fromJson`/`toJson` methods and formatting helpers (`formatBytes`, `formatSpeed`, `formatEta`, `statusDisplayText`). All `fromJson` methods read snake_case JSON keys and map to camelCase C++ fields. See spec section "Types (types.h)" for exact struct definitions and JSON field mappings.

Note: `AddRequest::toJson()` must omit empty/zero fields — only include non-default values in the JSON. The daemon fills in defaults for omitted fields.

Note: `Download::data()` for `Qt::ToolTipRole` should return "This download needs a new URL. Refresh UI planned for a future version." when `status == "refresh"`.

- [ ] **Step 3: Create minimal main.cpp**

Create `bolt-qt/src/main.cpp` — `QApplication` setup with app name/org. No widgets yet — just verify the build system works.

- [ ] **Step 4: Verify it builds**

Run: `cd bolt-qt && cmake -B build && cmake --build build`
Expected: Compiles successfully

- [ ] **Step 5: Update Makefile build-qt target**

Replace the stub `build-qt` target with: `cd bolt-qt && cmake -B build -DCMAKE_BUILD_TYPE=Release && cmake --build build`

Verify: `make build-qt` succeeds.

- [ ] **Step 6: Commit**

```bash
git add bolt-qt/CMakeLists.txt bolt-qt/src/main.cpp bolt-qt/src/types.h Makefile
git commit -m "feat(bolt-qt): add build system, types, and minimal entry point"
```

---

### Task 2: DaemonClient

**Files:**
- Create: `bolt-qt/src/daemonclient.h`
- Create: `bolt-qt/src/daemonclient.cpp`
- Modify: `bolt-qt/CMakeLists.txt` (add source files)

- [ ] **Step 1: Create daemonclient.h**

Declare `DaemonClient` class with:
- `QLocalSocket` member, `QTimer` for polling (1s) and reconnect (3s)
- Request queue (`QQueue<PendingRequest>`) for serialized HTTP
- HTTP response parser state machine (headers buffer, body buffer, content length, status code)
- All public API methods from spec (fetchDownloads, addDownload, deleteDownload, pauseDownload, resumeDownload, retryDownload, probeUrl, fetchConfig, updateConfig, fetchStats)
- All signals from spec (connected, disconnected, downloadsFetched, downloadAdded, probeCompleted, probeFailed, configFetched, configUpdated, statsFetched, requestFailed)
- Private slots: onSocketConnected, onSocketDisconnected, onSocketError, onReadyRead, tryConnect, poll

- [ ] **Step 2: Implement socket path resolution, connect/disconnect lifecycle**

In `daemonclient.cpp`, implement just the connection layer:
- `socketPath()`: check `$XDG_RUNTIME_DIR/bolt/bolt.sock`, fallback to `/tmp/bolt-<uid>/bolt.sock`
- Constructor: create socket, timers, connect socket signals (`connected`, `disconnected`, `errorOccurred`), call `tryConnect()`
- `tryConnect()`: connect to socket path. **Important:** use the full filesystem path with `QLocalSocket`. On Linux, `connectToServer(name)` may treat the argument as an abstract socket. Use the full path directly.
- `onSocketConnected()`: emit `connected()`
- `onSocketDisconnected()`: emit `disconnected()`, stop poll timer, clear parser state, start 3s reconnect timer
- `onSocketError()`: if not connected, start reconnect timer

At this point the client connects and reconnects but cannot send requests yet.

- [ ] **Step 3: Implement HTTP request serialization and one simple GET**

In `daemonclient.cpp`, add:
- `sendRequest(method, path, body, tag)`: enqueue `PendingRequest`, call `processQueue()`
- `processQueue()`: if `m_requestInFlight` or queue empty, return. Dequeue, build HTTP/1.1 request bytes:
  ```
  METHOD path HTTP/1.1\r\n
  Host: localhost\r\n
  Content-Type: application/json\r\n    (only for POST/PUT with body)
  Content-Length: N\r\n
  \r\n
  body
  ```
  Write to socket, set `m_requestInFlight = true`
- `onReadyRead()`: state machine — accumulate data into `m_headerBuffer` until `\r\n\r\n` found, extract status code and Content-Length, switch to body reading, accumulate into `m_bodyBuffer` until `m_contentLength` bytes read, call `handleResponse()`, reset state, call `processQueue()`
- `handleResponse()`: for now, just parse JSON and log it. Will be wired to signals in next step.
- `fetchStats()`: implement as the first real API method — `sendRequest("GET", "/api/stats", {}, "fetchStats")`. Add basic routing in `handleResponse()` to parse Stats and emit `statsFetched()`.

Verify at this point: `onSocketConnected()` calls `fetchStats()`, and the signal fires with real data.

- [ ] **Step 4: Implement response routing for all endpoints**

In `daemonclient.cpp`, flesh out `handleResponse()`:
- Check for error responses (status >= 400): extract `error` and `code` fields, emit `requestFailed()`
- Route by tag string to correct signal with envelope unwrapping per spec table
- Implement `fetchDownloads()`, `fetchConfig()`, `probeUrl()`, `addDownload()`, `updateConfig()` — each calls `sendRequest()` with the right method/path/body/tag
- Wire response handlers for each: parse envelope, emit typed signal

- [ ] **Step 5: Implement polling and action methods**

In `daemonclient.cpp`, add:
- `poll()`: if `m_pollInFlight`, return (skip). Otherwise set `m_pollInFlight = true`, call `fetchDownloads()` with tag `"poll"`. Clear flag in the response handler.
- Wire `onSocketConnected()` to start 1s poll timer and trigger initial `fetchDownloads()`
- Implement action methods: `pauseDownload()`, `resumeDownload()`, `retryDownload()`, `deleteDownload()` — each calls `sendRequest()` with appropriate POST/DELETE
- Action response handling: on success, trigger immediate `fetchDownloads()`. On failure, emit `requestFailed()`.

- [ ] **Step 6: Add to CMakeLists.txt**

Add `src/daemonclient.h` and `src/daemonclient.cpp` to `add_executable`.

- [ ] **Step 7: Verify it builds**

Run: `make build-qt`
Expected: Compiles without errors

- [ ] **Step 8: Smoke-test against running daemon**

Run the built binary directly. The DaemonClient constructor auto-connects, so if the daemon is running it should connect without any code changes. Verify by observing:
- No crash on startup
- `qDebug` output from DaemonClient (add a few permanent `qDebug() << "connected"` / `qDebug() << "poll: N downloads"` lines in the signal handlers — these are useful long-term for debugging, not temporary)
- If daemon is not running: verify reconnect timer fires (no crash, just retries silently)

- [ ] **Step 9: Commit**

```bash
git add bolt-qt/
git commit -m "feat(bolt-qt): add DaemonClient with REST-over-Unix-socket and polling"
```

---

### Task 3: DownloadListModel and ProgressDelegate

**Files:**
- Create: `bolt-qt/src/downloadlistmodel.h`
- Create: `bolt-qt/src/downloadlistmodel.cpp`
- Create: `bolt-qt/src/progressdelegate.h`
- Create: `bolt-qt/src/progressdelegate.cpp`
- Modify: `bolt-qt/CMakeLists.txt`

- [ ] **Step 1: Create downloadlistmodel.h**

Declare `DownloadListModel` with:
- `enum Column { ColFilename, ColSize, ColProgress, ColSpeed, ColEta, ColStatus, ColCount }`
- Standard `QAbstractTableModel` overrides: `rowCount`, `columnCount`, `data`, `headerData`
- Helper methods: `downloadAt(row)`, `downloadIdAt(row)`, `selectedIds(QModelIndexList)`
- Slot: `updateFromPoll(QVector<Download>)`
- Members: `QVector<Download> m_downloads`, `QHash<QString, qint64> m_prevDownloaded`

- [ ] **Step 2: Create downloadlistmodel.cpp**

Implement:
- `data()`: return appropriate display text per column. Progress column returns percentage int (0-100) for the delegate. Speed uses `formatSpeed()`, ETA uses `formatEta()`, Status uses `Download::statusDisplayText()`.
- `updateFromPoll()`: the core merge logic:
  1. Index incoming downloads by ID
  2. Walk existing rows backwards — remove any whose ID is missing from incoming
  3. Walk existing rows forward — update fields, calculate speed (EMA alpha=0.3), calculate ETA
  4. Insert any incoming downloads not already in the model, preserving daemon queue order (insert at the position matching `queueOrder` relative to existing rows, not blindly appending)
  5. Update `m_prevDownloaded` map
  6. First poll after startup: speed stays 0 (no previous sample)

- [ ] **Step 3: Create progressdelegate.h and progressdelegate.cpp**

Simple `QStyledItemDelegate` that reads percentage from `index.data(Qt::DisplayRole).toInt()`, creates a `QStyleOptionProgressBar`, and draws via `QApplication::style()->drawControl(QStyle::CE_ProgressBar, ...)`.

- [ ] **Step 4: Add to CMakeLists.txt and verify build**

Run: `make build-qt`
Expected: Compiles

- [ ] **Step 5: Commit**

```bash
git add bolt-qt/
git commit -m "feat(bolt-qt): add DownloadListModel with poll-based speed/ETA and ProgressDelegate"
```

---

### Task 4: MainWindow

**Files:**
- Create: `bolt-qt/src/mainwindow.h`
- Create: `bolt-qt/src/mainwindow.cpp`
- Modify: `bolt-qt/src/main.cpp`
- Modify: `bolt-qt/CMakeLists.txt`

- [ ] **Step 1: Create mainwindow.h**

Declare `MainWindow : QMainWindow` with:
- Members: `DaemonClient*`, `DownloadListModel*`, `QTableView*`, `ProgressDelegate*`
- Toolbar actions: add, pause, resume, retry, delete, settings
- Status bar labels: connection, active count, speed
- Empty state label
- Slots for connected/disconnected/downloadsFetched/requestFailed/selectionChanged/toolbar actions

- [ ] **Step 2: Implement constructor, toolbar, and status bar**

In `mainwindow.cpp`:
- Constructor: set window title "Bolt Download Manager", create model, table view, progress delegate
- `setupToolbar()`: create 6 actions with `QIcon::fromTheme()` icons per spec table. Connect action `triggered` signals to slots.
- `setupStatusBar()`: create 3 labels — connection state ("Connecting..."), active count, total speed. Initial state is "Connecting..." (shown before first connect/disconnect signal).
- Table view: set model, set delegate on progress column, configure multi-row selection, set reasonable column widths
- Save/restore geometry via `QSettings` in constructor and `closeEvent`

- [ ] **Step 3: Implement connection/poll handlers and empty state**

In `mainwindow.cpp`:
- `onConnected()`: update connection label to "Connected"
- `onDisconnected()`: update connection label to "Disconnected — retrying..."
- `onDownloadsFetched()`: call `m_model->updateFromPoll()`, update active count label (count status == "active"), update total speed label (sum of model speeds), toggle empty state label visibility (show "No downloads yet. Click + to add one." when model is empty)
- Empty state: `QLabel` overlaid on the table view, centered, visible only when model has 0 rows

- [ ] **Step 4: Implement toolbar state and action handlers**

In `mainwindow.cpp`:
- `onSelectionChanged()` → `updateToolbarState()`: iterate selected rows, check download statuses per spec toolbar behavior table, enable/disable Pause/Resume/Retry/Delete
- `onPause()`/`onResume()`/`onRetry()`: filter selected downloads by applicable status (client-side). If no selected rows are applicable, do nothing (no-op — don't fire requests). Otherwise send actions only for valid ones. Track success/failure count across responses. If some fail, show "N of M actions failed" in status bar. Guard: if `!m_client->isConnected()`, show brief error and return.
- `onDelete()`: show custom `QDialog` (not `QMessageBox::question`, which doesn't support checkboxes) with message, "Also delete downloaded file" checkbox (unchecked by default), and OK/Cancel. On OK, call `deleteDownload()` for each selected. Same disconnected guard.
- `onRequestFailed()`: show brief error message in status bar (auto-clear after 5s)

- [ ] **Step 5: Implement onAddUrl and onSettings stubs**

Stub `onAddUrl()` and `onSettings()` — they will be wired to dialogs in Tasks 5 and 6.

- [ ] **Step 6: Update main.cpp**

Replace minimal main with full setup: create `DaemonClient`, create `MainWindow(&client)`, show window, run event loop.

- [ ] **Step 7: Add to CMakeLists.txt and verify build**

Run: `make build-qt`
Expected: Compiles. Window shows with toolbar and empty table.

- [ ] **Step 8: Manual test against daemon**

Run bolt-qt with daemon running. Verify:
- Status bar shows "Connected"
- Download list populates from daemon
- Progress bars update every second
- Speed and ETA columns show values for active downloads
- Toolbar buttons enable/disable based on selection
- Pause/Resume/Delete work
- Status bar shows "Disconnected — retrying..." when daemon stops, "Connected" when it restarts

- [ ] **Step 9: Commit**

```bash
git add bolt-qt/
git commit -m "feat(bolt-qt): add MainWindow with toolbar, download list, and status bar"
```

---

### Task 5: AddDownloadDialog

**Files:**
- Create: `bolt-qt/src/adddownloaddialog.h`
- Create: `bolt-qt/src/adddownloaddialog.cpp`
- Modify: `bolt-qt/CMakeLists.txt`

- [ ] **Step 1: Create adddownloaddialog.h**

Declare `AddDownloadDialog : QDialog` with:
- Members: `DaemonClient*`, URL edit, probe button, filename edit, size/resumable labels, dir edit, browse button, segments spin, error label, download/cancel buttons
- Slots: onProbe, onProbeCompleted, onProbeFailed, onDownload, onDownloadAdded, onRequestFailed, onConfigFetched

- [ ] **Step 2: Create adddownloaddialog.cpp**

Implement per spec:
- Constructor: build layout (URL + Probe, probe results group, options group, buttons). Check clipboard for URL. Call `fetchConfig()` for defaults. Connect signals. Connect URL edit's `returnPressed` to `onProbe()` for auto-probe on Enter. Optionally add a `QTimer::singleShot` debounce on paste (300ms).
- `onProbe()`: guard if disconnected. Call `probeUrl(url)`, disable probe button
- `onProbeCompleted()`: populate filename, size (`formatBytes`), resumable text, re-enable probe
- `onProbeFailed()`: show error in `m_errorLabel`, re-enable probe
- `onDownload()`: build `AddRequest` from form fields, call `addDownload()`
- `onDownloadAdded()`: `accept()` to close dialog
- `onRequestFailed()`: if `DUPLICATE_FILENAME`, show `QMessageBox::question` "File exists. Download anyway?", on Yes set `force=true` and retry. Otherwise show error in `m_errorLabel`.
- Disconnect DaemonClient signals in destructor

- [ ] **Step 3: Wire to MainWindow::onAddUrl()**

Create dialog with `Qt::WA_DeleteOnClose`, call `open()`.

- [ ] **Step 4: Add to CMakeLists.txt and verify build**

Run: `make build-qt`

- [ ] **Step 5: Manual test**

Test the full add flow: paste URL, probe, download. Test duplicate handling.

- [ ] **Step 6: Commit**

```bash
git add bolt-qt/
git commit -m "feat(bolt-qt): add AddDownloadDialog with probing and duplicate handling"
```

---

### Task 6: SettingsDialog

**Files:**
- Create: `bolt-qt/src/settingsdialog.h`
- Create: `bolt-qt/src/settingsdialog.cpp`
- Modify: `bolt-qt/CMakeLists.txt`

- [ ] **Step 1: Create settingsdialog.h**

Declare `SettingsDialog : QDialog` with:
- Members: `DaemonClient*`, `Config m_originalConfig`, all form widgets (dir edit, spinboxes, checkbox), error label, save/cancel buttons
- Slots: onConfigFetched, onConfigUpdated, onRequestFailed, onSave, onBrowse

- [ ] **Step 2: Create settingsdialog.cpp**

Implement per spec:
- Constructor: build layout with `QFormLayout`. Call `fetchConfig()`. Connect signals. Disconnect DaemonClient signals in destructor (same pattern as AddDownloadDialog).
- `onConfigFetched()`: store as `m_originalConfig`, populate fields. Speed limit and min segment size: convert from bytes to MB for display.
- `onSave()`: build `QJsonObject` with only changed fields (compare against `m_originalConfig`). Convert MB inputs back to bytes. Call `updateConfig()`.
- `onConfigUpdated()`: `accept()` to close dialog
- `onRequestFailed()`: show error in `m_errorLabel`
- `onBrowse()`: `QFileDialog::getExistingDirectory()`

- [ ] **Step 3: Wire to MainWindow::onSettings()**

Create dialog with `Qt::WA_DeleteOnClose`, call `open()`.

- [ ] **Step 4: Add to CMakeLists.txt and verify build**

Run: `make build-qt`

- [ ] **Step 5: Manual test**

Open settings, change values, save. Verify daemon config updated via `curl --unix-socket ... /api/config`.

- [ ] **Step 6: Commit**

```bash
git add bolt-qt/
git commit -m "feat(bolt-qt): add SettingsDialog with config read/write"
```

---

### Task 7: Final integration and docs

**Files:**
- Modify: `Makefile`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update Makefile install target**

Add bolt-qt binary install after bolt-host install. Since Qt6 dev packages may not be installed on all systems, bolt-qt install is conditional — it copies the binary only if it was already built:
```makefile
	@if [ -f bolt-qt/build/bolt-qt ]; then \
		cp bolt-qt/build/bolt-qt ~/.local/bin/; \
		echo "Installed bolt-qt"; \
	else \
		echo "Note: bolt-qt not built (run 'make build-qt' first, requires Qt6 dev packages)"; \
	fi
```

- [ ] **Step 2: Update CLAUDE.md**

Add bolt-qt to package layout section. Update `make build-qt` description to note it requires Qt6 dev packages.

- [ ] **Step 3: Full build verification**

Run: `make build && make build-host && make build-qt && make test`
Expected: All succeed. Go tests unaffected by Qt changes.

- [ ] **Step 4: Full manual test**

1. Ensure daemon is running: `systemctl --user start bolt`
2. Run: `./bolt-qt/build/bolt-qt`
3. Verify connected status, empty state placeholder
4. Add a download via Add URL dialog — verify probe, download, progress
5. Pause, resume, delete the download
6. Open Settings, change max concurrent, save
7. Close and reopen — verify window geometry restored
8. Kill daemon — verify "Disconnected" status, retry behavior
9. Start daemon — verify reconnection and list refresh

- [ ] **Step 5: Commit**

```bash
git add Makefile CLAUDE.md
git commit -m "chore: update Makefile and CLAUDE.md for bolt-qt"
```
