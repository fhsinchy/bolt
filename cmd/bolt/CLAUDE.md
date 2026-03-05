# cmd/bolt

GUI-only entry point. Launches the Wails desktop application.

## Mode Detection

- `bolt` (no args) or `bolt start` → `launchGUI()` via `gui.go`
- `bolt version` → print version and exit
- `bolt help` → print usage and exit
- Any other subcommand → print error and exit

## Startup Sequence (`setupDaemon`)

Called by `launchGUI()` to initialize backend resources before the Wails window opens:

1. Load config (create defaults if missing)
2. Open SQLite database, run migrations
3. Create event bus + engine + queue manager
4. Wire queue completion (subscribe to bus, call OnDownloadComplete on completed/failed/paused)
5. Create HTTP server (for browser extension compatibility)
6. Start queue manager goroutine
7. Start HTTP server goroutine
8. Resume interrupted downloads

## Existing Instance Detection (`raiseExistingWindow`)

Before starting the GUI, checks if another Bolt instance is already running by probing the HTTP server at the configured port. If reachable, sends `POST /api/window/show` to bring the existing window to front, then exits.

## GUI Launch (`launchGUI` in `gui.go`)

1. Call `raiseExistingWindow()` — exit if another instance is running
2. Call `setupDaemon()` — initialize backend resources
3. Create Wails app with IPC bindings (`internal/app/`)
4. Configure window options (title, size, icon)
5. Set up system tray via `energye/systray` (`internal/tray/`)
6. Launch Wails window (blocks until quit)
7. On quit: shutdown server → engine → cancel context

## Files

| File | Purpose |
|---|---|
| `main.go` | Entry point, subcommand dispatch (GUI / version / help) |
| `gui.go` | `launchGUI()`, `setupDaemon()`, `raiseExistingWindow()`, Wails window + tray setup |
| `appicon.png` | Embedded app icon for `linux.Options{Icon}` (X11 fallback) |
