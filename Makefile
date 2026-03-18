.PHONY: build build-extension build-extension-chrome test test-race test-v test-stress test-cover install uninstall clean

BINARY = bolt
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/bolt/

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

build-extension: build-extension-chrome

build-extension-chrome:
	mkdir -p dist
	cd extensions/chrome && zip -r ../../dist/bolt-capture-chrome.zip . -x ".*"

install: build
	mkdir -p ~/.local/bin
	cp $(BINARY) ~/.local/bin/
	mkdir -p ~/.config/systemd/user
	cp packaging/bolt.service ~/.config/systemd/user/
	mkdir -p ~/.local/share/applications
	sed 's|Exec=bolt|Exec=$(HOME)/.local/bin/bolt|' packaging/bolt.desktop > ~/.local/share/applications/bolt.desktop
	mkdir -p ~/.local/share/icons/hicolor/256x256/apps
	cp images/icon.png ~/.local/share/icons/hicolor/256x256/apps/bolt.png
	-gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor 2>/dev/null
	-update-desktop-database ~/.local/share/applications 2>/dev/null
	systemctl --user daemon-reload
	systemctl --user enable --now bolt

uninstall:
	-systemctl --user stop bolt
	-systemctl --user disable bolt
	rm -f ~/.local/bin/$(BINARY)
	rm -f ~/.config/systemd/user/bolt.service
	rm -f ~/.local/share/applications/bolt.desktop
	rm -f ~/.local/share/icons/hicolor/256x256/apps/bolt.png
	-gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor 2>/dev/null
	-update-desktop-database ~/.local/share/applications 2>/dev/null
	systemctl --user daemon-reload

clean:
	rm -f $(BINARY)
	rm -rf dist
	go clean -testcache
