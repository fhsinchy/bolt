<p align="center">
  <img src="images/banner.png" alt="Bolt — Fast, segmented download manager for Linux" width="600" />
</p>

<p align="center">
  <strong>The Linux download manager.</strong> Fast, segmented downloads with a clean GUI, browser integration, and deep Linux desktop integration.
</p>

<p align="center">
  <a href="https://github.com/fhsinchy/bolt/actions/workflows/ci.yml"><img src="https://github.com/fhsinchy/bolt/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/fhsinchy/bolt/releases/latest"><img src="https://img.shields.io/github/v/release/fhsinchy/bolt" alt="Release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/fhsinchy/bolt?link=LICENSE" alt="License" /></a>

</p>

## Features

- **Segmented downloading** — splits files into up to 32 concurrent connections (default 16)
- **Pause and resume** — segment progress persists to SQLite, survives process restarts
- **Auto-retry** — per-segment exponential backoff for transient failures
- **Download queue** — configurable max concurrent downloads with FIFO scheduling
- **Speed limiter** — global rate limiting across all active segments
- **Keyboard shortcuts** — Ctrl+N (add), Ctrl+V (paste URL), Delete (remove), Space (pause/resume), Ctrl+A (select all), Ctrl+Q (quit)
- **Batch URL import** — paste or load a text file of URLs, queued sequentially with per-URL progress
- **Queue reordering** — drag and drop to reprioritize pending downloads
- **Dead link refresh** — automatic URL renewal for expired CDN links
- **Dark theme** — system, light, and dark modes with live switching
- **Checksum verification** — SHA-256, SHA-512, SHA-1, MD5; set before or during download, verified on completion
- **Download details** — per-segment progress, URL refresh, checksum editing, full metadata view
- **Desktop notifications** — completion and failure alerts via native OS notifications
- **Browser extensions** — Chrome and Firefox extensions intercept downloads, with configurable file size limits, extension filters, and domain blocklist
- **REST API** — full CRUD over HTTP for scripting and extension integration
- **WebSocket** — real-time progress push
- **System tray** — minimize to tray, background operation

## Screenshots

![Bolt in dark and light mode](bolt-light-dark.png)

## Install

Download the latest release from [GitHub Releases](https://github.com/fhsinchy/bolt/releases/latest):

```bash
# Download and extract
tar xzf bolt-linux-amd64-v*.tar.gz
cd bolt-linux-amd64

# Install binary, systemd unit, desktop entry, and icon
mkdir -p ~/.local/bin ~/.config/systemd/user ~/.local/share/applications ~/.local/share/icons/hicolor/256x256/apps
cp bolt ~/.local/bin/
cp bolt.service ~/.config/systemd/user/
sed "s|Exec=bolt|Exec=$HOME/.local/bin/bolt|" bolt.desktop > ~/.local/share/applications/bolt.desktop
cp appicon.png ~/.local/share/icons/hicolor/256x256/apps/bolt.png
systemctl --user daemon-reload
systemctl --user enable --now bolt
```

Make sure `~/.local/bin` is in your `PATH`.

## Build from Source

### Prerequisites

**System dependencies:**

Fedora:
```bash
sudo dnf install golang gtk3-devel webkit2gtk4.1-devel gcc-c++
```

Ubuntu / Debian:
```bash
sudo apt install golang libgtk-3-dev libwebkit2gtk-4.1-dev build-essential
```

Arch:
```bash
sudo pacman -S go gtk3 webkit2gtk-4.1
```

**Node.js** (see [nodejs.org/en/download](https://nodejs.org/en/download) for other methods):
```bash
# Using fnm
curl -fsSL https://fnm.vercel.app/install | bash
fnm use --install-if-missing 20
```

**pnpm** (see [pnpm.io/installation](https://pnpm.io/installation) for other methods):
```bash
curl -fsSL https://get.pnpm.io/install.sh | sh -
```

**Wails CLI:**
```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

Verify your environment with `wails doctor`.

### Build

```bash
git clone https://github.com/fhsinchy/bolt.git
cd bolt
make build
```

This builds the frontend, embeds it into the Go binary, and produces a `./bolt` executable.

### Run

```bash
./bolt              # launch the GUI
```

## Browser Extension

Bolt ships browser extensions for Chrome and Firefox that intercept downloads and forward them to the running Bolt instance.

**Chrome:** Open `chrome://extensions`, enable Developer mode, click "Load unpacked", and select `extensions/chrome/`.

**Firefox:** Open `about:debugging#/runtime/this-firefox`, click "Load Temporary Add-on", and select any file inside `extensions/firefox/`.

The extension popup lets you configure the server URL and auth token. On first install, a welcome page walks you through setup.

## Development

```bash
make dev           # hot-reload GUI development (wails dev)
make test          # run all tests
make test-race     # run tests with race detector
make test-v        # verbose test output
make test-stress   # include stress tests (~2 min)
make test-cover    # generate coverage report
make clean         # remove binary and clear caches
```

Tests do not require Wails build tags or CGO — `go test ./...` works on any system with Go installed.

## Architecture

Bolt runs as a single GUI binary — Wails window + system tray + HTTP server + download engine.

```
cmd/bolt/           Entry point (GUI launch)
internal/
  engine/           Download engine (segmented downloading, retry, resume)
  queue/            Queue manager (concurrency control)
  server/           HTTP server (REST API + WebSocket, for browser extension)
  app/              Wails IPC bindings
  db/               SQLite data access layer
  config/           Configuration management
  event/            Event bus (pub/sub)
  tray/             System tray
  notify/           Desktop notifications
  model/            Shared types
frontend/           Svelte 5 + TypeScript + Tailwind CSS
```

## Roadmap

| Phase | Description | Status |
|-------|-------------|--------|
| 1. Engine | Segmented downloads, pause/resume, retry, queue | Complete |
| 2. HTTP Server | REST API, WebSocket progress | Complete |
| 3. Desktop GUI | Wails app, system tray, Svelte frontend | Complete |
| 4. Browser Extension | Chrome + Firefox download interception | Complete |
| 5. Linux-Only Shift | Remove cross-platform code, update docs, Linux-only focus | Complete |
| 6. P1 Features | Speed limiter, dark theme, notifications, keyboard shortcuts, batch import, download details | Complete |

See `docs/STATUS.md` for detailed per-feature status.

## Tech Stack

| Component | Technology |
|-----------|------------|
| Backend | Go 1.23+ |
| GUI framework | Wails v2 |
| Frontend | Svelte 5, TypeScript 5, Vite 6, Tailwind CSS 4 |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO) |
| WebSocket | `nhooyr.io/websocket` |
| System tray | `energye/systray` |
| ID generation | ULID via `github.com/oklog/ulid/v2` |

## License

[MIT](LICENSE)
