.PHONY: build build-gui build-extension dev test test-race test-v test-stress test-cover clean

BINARY = bolt

# Build tags required by Wails.
# webkit2_41 is needed on systems with webkit2gtk-4.1 (Fedora 39+, etc.)
WAILS_TAGS = desktop,production,webkit2_41

build:
	cd frontend && pnpm build
	CGO_ENABLED=1 go build -tags $(WAILS_TAGS) -o $(BINARY) ./cmd/bolt/

build-gui:
	wails build -tags webkit2_41

dev:
	wails dev -tags webkit2_41

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

build-extension:
	mkdir -p dist
	cd extension && zip -r ../dist/bolt-capture.zip . -x ".*"

clean:
	rm -f $(BINARY)
	rm -rf frontend/dist dist
	go clean -testcache
