<p align="center">
  <img src="images/banner.png" alt="Bolt — Fast, segmented download manager for Linux" width="600" />
</p>

<p align="center">
  <strong>The Linux download manager.</strong> Fast, segmented downloads with a clean GUI, browser integration, and deep desktop integration.
</p>

<p align="center">
  <a href="https://github.com/fhsinchy/bolt/actions/workflows/ci.yml"><img src="https://github.com/fhsinchy/bolt/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/fhsinchy/bolt/releases/latest"><img src="https://img.shields.io/github/v/release/fhsinchy/bolt" alt="Release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/fhsinchy/bolt?link=LICENSE" alt="License" /></a>
</p>

## Screenshots

![Bolt in dark and light mode](bolt-light-dark.png)

## Features

- **Segmented downloading** — splits files into up to 32 concurrent connections (default 16)
- **Pause and resume** — segment progress persists to SQLite, survives process restarts
- **Auto-retry** — per-segment exponential backoff for transient failures
- **Download queue** — configurable max concurrent downloads with FIFO scheduling
- **Speed limiter** — global rate limiting across all active segments
- **Queue reordering** — drag and drop to reprioritize pending downloads
- **Dead link refresh** — automatic URL renewal for expired CDN links
- **Checksum verification** — SHA-256, SHA-512, SHA-1, MD5; verified on completion
- **Download details** — per-segment progress, URL refresh, checksum editing, full metadata view
- **Dark theme** — system, light, and dark modes
- **Desktop notifications** — completion and failure alerts via `notify-send`
- **Keyboard shortcuts** — Ctrl+N (add), Ctrl+V (paste URL), Delete (remove), Space (pause/resume), Ctrl+A (select all), Ctrl+Q (quit)
- **Batch URL import** — paste or load a text file of URLs
- **Browser extensions** — Chrome and Firefox extensions intercept downloads, with configurable filters and domain blocklist
- **REST API + WebSocket** — full HTTP API for scripting and browser extension integration

## Install

One-liner:

```bash
curl -fsSL https://raw.githubusercontent.com/fhsinchy/bolt/master/install.sh | sh
```

This downloads the latest release, installs the binary to `~/.local/bin`, sets up a systemd user service, desktop entry, and icon. Bolt starts automatically on login.

To uninstall:

```bash
curl -fsSL https://raw.githubusercontent.com/fhsinchy/bolt/master/install.sh | sh -s -- --uninstall
```

Make sure `~/.local/bin` is in your `PATH`.

### Manual install

Download the latest tarball from [GitHub Releases](https://github.com/fhsinchy/bolt/releases/latest):

```bash
tar xzf bolt-linux-amd64.tar.gz
cd bolt-linux-amd64

mkdir -p ~/.local/bin ~/.config/systemd/user ~/.local/share/applications ~/.local/share/icons/hicolor/256x256/apps
cp bolt ~/.local/bin/
cp bolt.service ~/.config/systemd/user/
sed "s|Exec=bolt|Exec=$HOME/.local/bin/bolt|" bolt.desktop > ~/.local/share/applications/bolt.desktop
cp appicon.png ~/.local/share/icons/hicolor/256x256/apps/bolt.png
gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor 2>/dev/null || true
update-desktop-database ~/.local/share/applications 2>/dev/null || true
systemctl --user daemon-reload
systemctl --user enable --now bolt
```

## Browser Extension

Bolt ships browser extensions for Chrome and Firefox that intercept downloads and forward them to Bolt.

**Chrome:** Open `chrome://extensions`, enable Developer mode, click "Load unpacked", and select `extensions/chrome/`.

**Firefox:** Open `about:debugging#/runtime/this-firefox`, click "Load Temporary Add-on", and select any file inside `extensions/firefox/`.

The extension popup lets you configure the server URL and auth token. On first install, a welcome page walks you through setup.

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

**Node.js** (see [nodejs.org/en/download](https://nodejs.org/en/download)):
```bash
curl -fsSL https://fnm.vercel.app/install | bash
fnm use --install-if-missing 20
```

**pnpm** (see [pnpm.io/installation](https://pnpm.io/installation)):
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

### Development

```bash
make dev           # hot-reload development
make test          # run all tests
make test-race     # run tests with race detector
make build         # production build
make install       # build + install locally with systemd service
make uninstall     # remove everything
```

## Architecture

Single GUI binary — Wails window + HTTP server + download engine.

```
cmd/bolt/           Entry point (GUI launch)
internal/
  engine/           Download engine (segmented downloading, retry, resume)
  queue/            Queue manager (concurrency control)
  server/           HTTP server (REST API + WebSocket)
  app/              Wails IPC bindings
  db/               SQLite data access layer
  config/           Configuration management
  event/            Event bus (pub/sub)
  tray/             System tray
  notify/           Desktop notifications
  model/            Shared types
frontend/           Svelte 5 + TypeScript + Tailwind CSS
extensions/         Chrome + Firefox browser extensions
```

## Tech Stack

| Component | Technology |
|-----------|------------|
| Backend | Go 1.23+ |
| GUI | Wails v2 |
| Frontend | Svelte 5, TypeScript 5, Vite 6, Tailwind CSS 4 |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO) |
| WebSocket | `nhooyr.io/websocket` |
| System tray | `energye/systray` |

## License

[MIT](LICENSE)
