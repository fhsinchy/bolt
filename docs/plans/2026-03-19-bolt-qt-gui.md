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

- [ ] **Step 2: Create daemonclient.cpp**

Implement the full DaemonClient:

**Socket path resolution:** Check `$XDG_RUNTIME_DIR/bolt/bolt.sock`, fallback to `/tmp/bolt-<uid>/bolt.sock`. Use the filesystem socket path with `QLocalSocket`.

**Connection lifecycle:**
- Constructor: create socket, timers, connect socket signals, call `tryConnect()`
- `tryConnect()`: connect to socket path via `connectToServer()`
- `onSocketConnected()`: emit `connected()`, start 1s poll timer, do initial `fetchDownloads()`
- `onSocketDisconnected()`: emit `disconnected()`, stop poll timer, clear parser state, start 3s reconnect timer
- `onSocketError()`: if not connected, start reconnect timer

**HTTP request/response:**
- `sendRequest(method, path, body, tag)`: enqueue `PendingRequest`, call `processQueue()`
- `processQueue()`: if `m_requestInFlight` or queue empty, return. Dequeue, build HTTP/1.1 request bytes (`method path HTTP/1.1\r\nHost: localhost\r\nContent-Length: N\r\n\r\nbody`), write to socket, set `m_requestInFlight = true`
- `onReadyRead()`: state machine — accumulate data into `m_headerBuffer` until `\r\n\r\n` found, extract status code and Content-Length, switch to body reading, accumulate into `m_bodyBuffer` until `m_contentLength` bytes read, call `handleResponse()`, reset state, call `processQueue()`

**Response routing (`handleResponse`):**
- Parse JSON from body
- Check for error responses (status >= 400): extract `error` and `code` fields, emit `requestFailed()`
- Route by tag string to correct signal with envelope unwrapping per spec table
- Action tags (pause/resume/retry/delete): on success, trigger immediate `fetchDownloads()`

**Polling:**
- `poll()`: if `m_pollInFlight`, return (skip). Otherwise set `m_pollInFlight = true`, call `fetchDownloads()` with tag `"poll"`. Clear flag in the response handler.

- [ ] **Step 3: Add to CMakeLists.txt**

Add `src/daemonclient.h` and `src/daemonclient.cpp` to `add_executable`.

- [ ] **Step 4: Verify it builds**

Run: `make build-qt`
Expected: Compiles without errors

- [ ] **Step 5: Smoke-test against running daemon**

Temporarily add debug output to `main.cpp` — connect to `connected`/`downloadsFetched`/`disconnected` signals and print to qDebug. Run the binary with daemon running. Verify it connects, polls, and prints download counts.

- [ ] **Step 6: Revert smoke-test debug code from main.cpp**

- [ ] **Step 7: Commit**

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
  4. Append any incoming downloads not already in the model
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

- [ ] **Step 2: Create mainwindow.cpp**

Implement:
- Constructor: create all widgets, set up toolbar with `QIcon::fromTheme()` icons, status bar, connect signals
- Table view: set model, set delegate on progress column, configure selection behavior, column widths
- `onConnected()`/`onDisconnected()`: update connection label text
- `onDownloadsFetched()`: pass to model, update active count and total speed in status bar, toggle empty label visibility
- `updateToolbarState()`: iterate selected rows, check download statuses, enable/disable buttons per spec table
- `onPause()`/`onResume()`/`onRetry()`: filter selected downloads by applicable status, send only valid actions
- `onDelete()`: `QMessageBox::question` with "Also delete downloaded file" checkbox, call `deleteDownload()` for each selected
- Save/restore geometry via `QSettings`
- Empty state: `QLabel` centered in a `QStackedWidget` or overlaid on the table view

- [ ] **Step 3: Update main.cpp**

Replace minimal main with full setup: create `DaemonClient`, create `MainWindow(&client)`, show window, run event loop.

- [ ] **Step 4: Add to CMakeLists.txt and verify build**

Run: `make build-qt`
Expected: Compiles. Window shows with toolbar and empty table.

- [ ] **Step 5: Manual test against daemon**

Run bolt-qt with daemon running. Verify:
- Status bar shows "Connected"
- Download list populates from daemon
- Progress bars update every second
- Speed and ETA columns show values for active downloads
- Toolbar buttons enable/disable based on selection
- Pause/Resume/Delete work

- [ ] **Step 6: Commit**

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
- Constructor: build layout (URL + Probe, probe results group, options group, buttons). Check clipboard for URL. Call `fetchConfig()` for defaults. Connect signals.
- `onProbe()`: call `probeUrl(url)`, disable probe button
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
- Constructor: build layout with `QFormLayout`. Call `fetchConfig()`. Connect signals.
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

Add bolt-qt binary install after bolt-host install:
```makefile
	@if [ -f bolt-qt/build/bolt-qt ]; then cp bolt-qt/build/bolt-qt ~/.local/bin/; fi
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
