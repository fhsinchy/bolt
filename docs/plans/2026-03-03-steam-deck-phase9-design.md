# Phase 9: Steam Deck Optimization — Design

**Date:** 2026-03-03
**Status:** Approved
**Author:** Farhan Hasin Chowdhury

---

## Overview

Phase 9 makes Bolt a first-class citizen on the Steam Deck. It targets both Desktop Mode (touch-friendly Wails GUI) and Gaming Mode (Decky Loader QAM panel), packaged for SteamOS's read-only root filesystem.

The four components are largely independent and can be implemented in parallel.

---

## Component 1: Gamescope / Window Setup

### Goals

- Correct default window size for the 7" 1280×800 Steam Deck display
- Maximized window when running on Deck
- Graceful behavior in gamescope (e.g., when launched as a non-Steam game from Gaming Mode)

### Changes

**`cmd/bolt/gui.go`:**
- Set Wails window defaults: `Width: 1280, Height: 800, MinWidth: 800, MinHeight: 600`
- Detect Steam Deck at startup by reading `/etc/os-release` for `VARIANT_ID=steamdeck`
- If on Deck: call `runtime.WindowMaximise(ctx)` after window shows; skip system tray initialization

**Gamescope notes:**
- No code changes required for gamescope compatibility. The existing `ProgramName: "bolt"` (sets Wayland `app_id`) and `bolt.desktop` with `StartupWMClass=bolt` already satisfy gamescope's window identification.
- The system tray (`energye/systray`) fails gracefully if no tray is available — existing behavior is fine.

---

## Component 2: Touch-Optimized Svelte Frontend

### Goals

- All interactive elements meet the 44px minimum tap target size
- Action buttons visible without hover on touch screens
- Drag-and-drop queue reordering works on touch (or replaced with ↑/↓ buttons)
- URL inputs show the correct virtual keyboard type
- No hover-only UI states

### Changes

**Global CSS (`frontend/src/app.css`):**
```css
/* Eliminate 300ms tap delay */
a, button, [role="button"] {
  touch-action: manipulation;
}

/* Minimum tap target */
@media (pointer: coarse) {
  button, [role="button"], input, select {
    min-height: 44px;
    min-width: 44px;
  }
}
```

**`ActionButtons.svelte`:**
- `pointer: fine` (mouse): buttons appear on parent row hover (current behavior)
- `pointer: coarse` (touch): buttons always visible with 44px height

**`DownloadRow.svelte`:**
- Row height increases on `pointer: coarse` to accommodate always-visible action buttons
- Remove hover-only visibility from action area

**`DownloadList.svelte` — Queue reordering:**
- `pointer: fine`: keep existing HTML5 DnD grip-handle behavior
- `pointer: coarse`: hide drag handle; show ↑/↓ buttons per row that call `ReorderDownloads` IPC method directly
- Detection: `window.matchMedia('(pointer: coarse)').matches` on mount, stored as a reactive variable

**`AddDownloadDialog.svelte` + `BatchImportDialog.svelte`:**
- URL inputs: `inputmode="url" autocomplete="off" autocorrect="off" autocapitalize="none"`
- Steam's virtual keyboard activates automatically for focused inputs on Wayland — no custom integration needed

---

## Component 3: `make install-deck` — SteamOS User-Space Install

### Goals

- Install Bolt without root or disabling SteamOS read-only root
- All files go into user home directory paths
- Systemd user service runs with absolute binary path (no PATH dependency)

### New Makefile Targets

```makefile
install-deck: build
	mkdir -p ~/.local/bin \
	         ~/.local/share/applications \
	         ~/.local/share/icons/hicolor/256x256/apps \
	         ~/.config/systemd/user
	cp bolt ~/.local/bin/bolt
	cp build/appicon.png ~/.local/share/icons/hicolor/256x256/apps/bolt.png
	sed 's|Exec=bolt|Exec=$(HOME)/.local/bin/bolt|' dist/bolt.desktop \
	    > ~/.local/share/applications/bolt.desktop
	sed 's|ExecStart=.*|ExecStart=$(HOME)/.local/bin/bolt|' dist/bolt.service \
	    > ~/.config/systemd/user/bolt.service
	systemctl --user daemon-reload
	systemctl --user enable --now bolt.service

uninstall-deck:
	-systemctl --user stop bolt.service
	-systemctl --user disable bolt.service
	rm -f ~/.local/bin/bolt \
	      ~/.local/share/applications/bolt.desktop \
	      ~/.local/share/icons/hicolor/256x256/apps/bolt.png \
	      ~/.config/systemd/user/bolt.service
	systemctl --user daemon-reload
```

### Key Differences vs `make install`

| Concern | `make install` | `make install-deck` |
|---------|---------------|---------------------|
| Binary location | `/usr/local/bin/bolt` | `~/.local/bin/bolt` |
| Requires sudo | Yes | No |
| .desktop location | `/usr/share/applications/` | `~/.local/share/applications/` |
| Icon location | `/usr/share/icons/hicolor/…` | `~/.local/share/icons/hicolor/…` |
| Service ExecStart | `/usr/local/bin/bolt` | `$(HOME)/.local/bin/bolt` (patched) |
| SteamOS compatible | No (read-only root) | Yes |

---

## Component 4: Decky Loader Plugin

### Goals

- Show active/queued downloads in the Quick Access Menu (QAM) while in Gaming Mode
- Pause/resume individual downloads from the QAM panel
- Start/stop Bolt daemon from within Gaming Mode
- Live progress via WebSocket

### Directory Structure

```
extensions/decky/
  plugin.json           # Decky plugin metadata
  main.py               # Python backend (config read, daemon control)
  package.json          # React frontend dependencies
  tsconfig.json
  src/
    index.tsx           # Main QAM PanelSection component
    components/
      DownloadItem.tsx  # Single download row with progress + pause/resume
      SpeedBadge.tsx    # Total speed display
```

### `plugin.json`

```json
{
  "name": "Bolt",
  "author": "fhsinchy",
  "flags": [],
  "publish": {
    "tags": ["download", "tools"],
    "description": "Manage your Bolt downloads without leaving Gaming Mode"
  }
}
```

### Python Backend (`main.py`)

Exposes three methods to the React frontend via Decky's `call_plugin_function`:

| Method | Description |
|--------|-------------|
| `get_bolt_config()` | Reads `~/.config/bolt/config.json`, returns `{ auth_token, server_port }` |
| `start_daemon()` | Runs `systemctl --user start bolt.service` |
| `stop_daemon()` | Runs `systemctl --user stop bolt.service` |

The frontend makes all API calls directly (no Python proxy needed — Bolt's API is localhost).

### React QAM Frontend (`src/index.tsx`)

**On mount:**
1. Call `get_bolt_config()` to get `auth_token` and `server_port`
2. Connect to `ws://localhost:<port>/ws?token=<token>`
3. On WS message: update download list state
4. On WS error/close: fall back to polling `GET /api/downloads` every 2s

**UI layout (320px wide QAM panel):**

```
┌─────────────────────────────────┐
│ Bolt                    ● 12 MB/s│
├─────────────────────────────────┤
│ ubuntu-24.04.iso                 │
│ ████████████░░░░░  67%  4 MB/s  │
│                      [⏸] [✕]    │
├─────────────────────────────────┤
│ node-v22.tar.gz                  │
│ ████████████████░░  82%  8 MB/s │
│                      [⏸] [✕]    │
├─────────────────────────────────┤
│ [Pause All]  [Resume All]        │
│ [Open Bolt Desktop]              │
└─────────────────────────────────┘
```

**When Bolt is not running:**
```
┌─────────────────────────────────┐
│ Bolt                    ● Offline│
├─────────────────────────────────┤
│ Bolt daemon is not running.      │
│ [Start Bolt]                     │
└─────────────────────────────────┘
```

**Components:**
- `DownloadItem`: filename (truncated), `ProgressBar`, speed + ETA inline, Pause/Resume button
- `SpeedBadge`: total active speed in the panel header
- Uses Decky SDK: `PanelSection`, `PanelSectionRow`, `ButtonItem`, `ProgressBar`

**"Open Bolt Desktop" button:**
- Calls `SteamClient.Apps.RunGame` or `window.open('steam://rungameid/<id>')` to switch to Desktop Mode
- Alternatively: simpler — just show a reminder to switch to Desktop Mode manually (avoids needing a Steam App ID)

### Makefile Target

```makefile
build-decky:
	cd extensions/decky && pnpm install && pnpm build
```

Produces `extensions/decky/dist/index.js` (bundled React).

Installation: user copies `extensions/decky/` to `~/homebrew/plugins/bolt/` (standard Decky plugin install path).

---

## Out of Scope

- Full gamepad/D-pad navigation in the Wails GUI (trackpad-as-mouse covers Desktop Mode adequately)
- Flatpak packaging
- Steam Input API configuration (users can use the built-in Web Browser controller template)
- Decky plugin store submission (manual install only for now)

---

## Success Criteria

| Criterion | Verification |
|-----------|-------------|
| Wails GUI opens at 1280×800 on Deck | Launch app in Desktop Mode on Deck |
| All buttons are 44px+ tap targets | Inspect element sizes in dev tools |
| Queue reordering works with touch | Tap ↑/↓ buttons on Deck touchscreen |
| URL input shows URL keyboard | Focus URL field with touchscreen |
| `make install-deck` completes without sudo | Run on SteamOS without `sudo steamos-readonly disable` |
| Decky QAM shows live download progress | Add a download via CLI, open QAM |
| Pause/resume works from QAM | Tap pause button in QAM panel |
| Bolt offline state handled in QAM | Stop bolt service, open QAM |
