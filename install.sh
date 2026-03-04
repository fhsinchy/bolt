#!/bin/sh
set -eu

# Bolt Download Manager — Userspace Install Script
# https://github.com/fhsinchy/bolt
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/fhsinchy/bolt/master/install.sh | sh
#   ./install.sh                  # install latest release
#   ./install.sh --uninstall      # remove bolt

REPO="fhsinchy/bolt"
BINARY_NAME="bolt"

INSTALL_DIR="${HOME}/.local/bin"
DESKTOP_DIR="${HOME}/.local/share/applications"
ICON_DIR="${HOME}/.local/share/icons/hicolor/256x256/apps"

# --- Helpers ---

info() {
    printf '\033[1;34m::\033[0m %s\n' "$1"
}

success() {
    printf '\033[1;32m::\033[0m %s\n' "$1"
}

error() {
    printf '\033[1;31merror:\033[0m %s\n' "$1" >&2
    exit 1
}

warn() {
    printf '\033[1;33mwarning:\033[0m %s\n' "$1" >&2
}

need_cmd() {
    if ! command -v "$1" > /dev/null 2>&1; then
        error "need '$1' (command not found)"
    fi
}

# --- Uninstall ---

uninstall() {
    info "Uninstalling Bolt..."

    if command -v systemctl > /dev/null 2>&1; then
        systemctl --user stop bolt 2>/dev/null || true
        systemctl --user disable bolt 2>/dev/null || true
    fi

    rm -f "${INSTALL_DIR}/${BINARY_NAME}"
    rm -f "${HOME}/.config/systemd/user/bolt.service"
    rm -f "${DESKTOP_DIR}/bolt.desktop"
    rm -f "${ICON_DIR}/bolt.png"

    if command -v systemctl > /dev/null 2>&1; then
        systemctl --user daemon-reload 2>/dev/null || true
    fi

    success "Bolt has been uninstalled."
    exit 0
}

# --- Detect architecture ---

detect_arch() {
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64)   echo "amd64" ;;
        aarch64|arm64)   echo "arm64" ;;
        *)               error "unsupported architecture: ${arch}" ;;
    esac
}

# --- Detect system dependencies ---

check_deps() {
    missing=""

    if ! pkg-config --exists gtk+-3.0 2>/dev/null; then
        missing="${missing}  - GTK3 (gtk3-devel / libgtk-3-dev)\n"
    fi

    if ! pkg-config --exists webkit2gtk-4.1 2>/dev/null && \
       ! pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
        missing="${missing}  - WebKit2GTK (webkit2gtk4.1-devel / libwebkit2gtk-4.1-dev)\n"
    fi

    if [ -n "$missing" ]; then
        warn "Missing system dependencies (required for the GUI):"
        printf '%b' "$missing" >&2
        printf '\n' >&2
        printf '  Install them with your package manager, e.g.:\n' >&2
        printf '    Fedora:  sudo dnf install gtk3-devel webkit2gtk4.1-devel\n' >&2
        printf '    Ubuntu:  sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev\n' >&2
        printf '    Arch:    sudo pacman -S gtk3 webkit2gtk-4.1\n' >&2
        printf '\n' >&2

        printf 'Continue anyway? [y/N] '
        read -r reply
        case "$reply" in
            y|Y|yes|YES) ;;
            *) exit 1 ;;
        esac
    fi
}

# --- Fetch latest release tag ---

get_latest_version() {
    if command -v curl > /dev/null 2>&1; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
            | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
    elif command -v wget > /dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" \
            | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
    else
        error "need 'curl' or 'wget' to download"
    fi
}

# --- Download file ---

download() {
    url="$1"
    dest="$2"
    if command -v curl > /dev/null 2>&1; then
        curl -fSL --progress-bar -o "$dest" "$url"
    elif command -v wget > /dev/null 2>&1; then
        wget -q --show-progress -O "$dest" "$url"
    fi
}

# --- Main ---

main() {
    # Handle flags
    for arg in "$@"; do
        case "$arg" in
            --uninstall|uninstall) uninstall ;;
            --help|-h)
                printf 'Usage: %s [--uninstall]\n' "$0"
                printf '\nInstalls Bolt download manager to ~/.local/\n'
                exit 0
                ;;
        esac
    done

    info "Bolt Download Manager — Installer"
    printf '\n'

    # Prerequisites
    if ! command -v curl > /dev/null 2>&1 && ! command -v wget > /dev/null 2>&1; then
        error "need 'curl' or 'wget' to download"
    fi
    need_cmd tar

    check_deps

    # Detect arch
    arch="$(detect_arch)"
    info "Detected architecture: ${arch}"

    # Get latest version
    info "Fetching latest release..."
    version="$(get_latest_version)"
    if [ -z "$version" ]; then
        error "could not determine latest release version"
    fi
    info "Latest version: ${version}"

    # Build download URL
    tarball="bolt-linux-${arch}.tar.gz"
    url="https://github.com/${REPO}/releases/download/${version}/${tarball}"

    # Download to temp directory
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT

    info "Downloading ${tarball}..."
    download "$url" "${tmpdir}/${tarball}"

    # Extract
    info "Extracting..."
    tar -xzf "${tmpdir}/${tarball}" -C "$tmpdir"

    # Install binary
    mkdir -p "$INSTALL_DIR"
    install -m 755 "${tmpdir}/bolt" "${INSTALL_DIR}/${BINARY_NAME}"
    info "Installed binary to ${INSTALL_DIR}/${BINARY_NAME}"

    # Install desktop entry (substitute absolute path for Exec)
    mkdir -p "$DESKTOP_DIR"
    if [ -f "${tmpdir}/bolt.desktop" ]; then
        sed "s|Exec=bolt|Exec=${INSTALL_DIR}/bolt|" "${tmpdir}/bolt.desktop" \
            > "${DESKTOP_DIR}/bolt.desktop"
    else
        # Generate inline if not in tarball
        cat > "${DESKTOP_DIR}/bolt.desktop" << DESKTOP
[Desktop Entry]
Name=Bolt
Comment=Fast, segmented download manager
Exec=${INSTALL_DIR}/bolt
Icon=bolt
Terminal=false
Type=Application
Categories=Network;FileTransfer;
StartupWMClass=bolt
DESKTOP
    fi
    info "Installed desktop entry"

    # Install icon
    mkdir -p "$ICON_DIR"
    if [ -f "${tmpdir}/appicon.png" ]; then
        cp "${tmpdir}/appicon.png" "${ICON_DIR}/bolt.png"
        info "Installed icon"
    else
        warn "Icon not found in release — Wayland app icon may not display"
    fi

    # Install and enable systemd user unit
    if command -v systemctl > /dev/null 2>&1; then
        unit_dir="${HOME}/.config/systemd/user"
        mkdir -p "$unit_dir"
        if [ -f "${tmpdir}/bolt.service" ]; then
            cp "${tmpdir}/bolt.service" "${unit_dir}/bolt.service"
        else
            # Generate inline if not in tarball
            cat > "${unit_dir}/bolt.service" << 'UNIT'
[Unit]
Description=Bolt Download Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%h/.local/bin/bolt start --headless
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
UNIT
        fi
        systemctl --user daemon-reload
        systemctl --user enable --now bolt
        info "Installed and enabled systemd user unit"
    else
        warn "systemctl not found — Bolt won't auto-start on boot"
    fi

    # Update icon cache if available
    if command -v gtk-update-icon-cache > /dev/null 2>&1; then
        gtk-update-icon-cache -f -t "${HOME}/.local/share/icons/hicolor" 2>/dev/null || true
    fi

    # Verify PATH
    printf '\n'
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            warn "${INSTALL_DIR} is not in your PATH"
            printf '  Add it to your shell profile:\n'
            printf '    export PATH="%s:$PATH"\n' "$INSTALL_DIR"
            printf '\n'
            ;;
    esac

    success "Bolt ${version} installed successfully!"
    printf '\n'
    printf '  Run:        bolt\n'
    printf '  Uninstall:  %s --uninstall\n' "$0"
    printf '\n'
}

main "$@"
