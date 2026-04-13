.PHONY: build build-host test test-race test-v test-stress test-cover install uninstall reinstall clean \
        build-qt run-qt build-all test-all

# --- Daemon (Go) ---

BINARY = bolt
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/bolt/

build-host:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bolt-host ./cmd/bolt-host/

test:
	go test ./... -count=1 -timeout 120s

test-race:
	go test ./... -race -count=1 -timeout 120s

test-v:
	go test ./... -v -count=1 -timeout 120s

test-stress:
	go test -tags=stress ./... -count=1 -timeout 300s

test-cover:
	go test ./... -count=1 -coverprofile=coverage.out -timeout 120s
	go tool cover -func=coverage.out

install: build build-host build-qt
	mkdir -p ~/.local/bin ~/.config/bolt ~/.local/share/bolt
	mkdir -p ~/.config/systemd/user ~/.local/share/applications ~/.local/share/icons/hicolor/256x256/apps
	cp $(BINARY) ~/.local/bin/
	cp bolt-host ~/.local/bin/
	ln -sf $(CURDIR)/bolt-qt/.venv/bin/bolt-qt ~/.local/bin/bolt-qt
	@for dir in ~/.config/google-chrome/NativeMessagingHosts ~/.config/chromium/NativeMessagingHosts ~/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts; do \
		mkdir -p $$dir; \
		sed 's|BOLT_HOST_PATH|$(HOME)/.local/bin/bolt-host|' packaging/com.fhsinchy.bolt.json > $$dir/com.fhsinchy.bolt.json; \
	done
	cp packaging/bolt.service ~/.config/systemd/user/
	sed 's|Exec=bolt-qt|Exec=$(HOME)/.local/bin/bolt-qt|' packaging/bolt.desktop > ~/.local/share/applications/bolt.desktop
	cp images/appicon.png ~/.local/share/icons/hicolor/256x256/apps/bolt.png
	-gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor 2>/dev/null
	-update-desktop-database ~/.local/share/applications 2>/dev/null
	systemctl --user daemon-reload
	systemctl --user enable --now bolt

uninstall:
	-pkill -f bolt-qt 2>/dev/null || true
	-systemctl --user stop bolt
	-systemctl --user disable bolt
	rm -f ~/.local/bin/$(BINARY)
	rm -f ~/.local/bin/bolt-host
	rm -f ~/.local/bin/bolt-qt
	rm -f ~/.config/google-chrome/NativeMessagingHosts/com.fhsinchy.bolt.json
	rm -f ~/.config/chromium/NativeMessagingHosts/com.fhsinchy.bolt.json
	rm -f ~/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts/com.fhsinchy.bolt.json
	rm -f ~/.config/systemd/user/bolt.service
	rm -f ~/.local/share/applications/bolt.desktop
	rm -f ~/.local/share/icons/hicolor/256x256/apps/bolt.png
	-gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor 2>/dev/null
	-update-desktop-database ~/.local/share/applications 2>/dev/null
	systemctl --user daemon-reload

reinstall: uninstall install

clean:
	rm -f $(BINARY)
	rm -f bolt-host
	rm -rf dist
	rm -rf bolt-qt/.venv
	go clean -testcache

# --- PySide6 GUI ---

build-qt:
	@if [ ! -f bolt-qt/.venv/bin/bolt-qt ]; then \
		python3 -m venv bolt-qt/.venv && bolt-qt/.venv/bin/pip install bolt-qt/; \
	fi

run-qt:
	bolt-qt/.venv/bin/bolt-qt

# --- Meta ---

build-all: build build-host build-qt

test-all: test
