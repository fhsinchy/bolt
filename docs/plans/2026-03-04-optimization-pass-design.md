# Optimization Pass Design

**Date:** 2026-03-04
**Type:** Proactive performance pass (no observed symptoms)
**Approach:** Targeted hot-path fixes — low-risk, high-confidence wins

---

## Scope

11 changes across Go backend and Svelte frontend. No API changes, no structural refactors, no new dependencies.

---

## Go Engine Optimizations

### 1.1 Speed Window Circular Buffer

**File:** `internal/engine/progress.go`

Replace `[]int64` slice with a fixed-size circular buffer. Currently `p.speeds = p.speeds[1:]` reallocates the underlying array on every progress emit (~2x/sec per download).

- Replace `speeds []int64` with `speeds [5]int64`, `speedIdx int`, `speedCount int`
- Write to `speeds[speedIdx]`, increment `speedIdx % 5`, cap `speedCount` at 5
- Average: sum `speeds[0:speedCount]` / `speedCount`

### 1.2 Fixed Report Channel Buffer

**File:** `internal/engine/engine.go`

Change `make(chan segmentReport, len(segments)*100)` to `make(chan segmentReport, 256)`. Reports arrive at ~500ms cadence; 256 slots is more than enough. Avoids scaling allocation with segment count (currently 1600 slots for 16 segments).

### 1.3 Skip Idle Progress Emits

**File:** `internal/engine/progress.go`

In the progress ticker case, skip `emitProgress()` if `totalDownloaded` hasn't changed since last emit. Paused/stalled downloads currently emit no-op events every 500ms.

- Add `lastEmittedBytes int64` field
- Compare before emitting; skip if unchanged

---

## Server & WebSocket Optimizations

### 2.1 Typed WebSocket Message Structs

**File:** `internal/server/websocket.go`

Replace `map[string]any` in `eventToWSMessage()` with typed structs (`wsProgressMsg`, `wsDownloadAddedMsg`, etc.). Progress events fire ~2x/sec per download — each currently allocates a fresh map on the heap.

### 2.2 Single Stats Query

**Files:** `internal/server/handlers.go`, `internal/db/downloads.go`

`handleGetStats` calls `CountByStatus` 3 times (3 separate SQLite queries). Replace with a single `SELECT status, COUNT(*) FROM downloads GROUP BY status`.

- Add `CountAllStatuses(ctx) (map[string]int, error)` to the store
- Use it in `handleGetStats`

### 2.3 Response Status Struct

**File:** `internal/server/handlers.go`

The `map[string]string{"status": "..."}` pattern appears 6+ times. Define a `statusResponse` struct to avoid repeated map allocations.

---

## Frontend Optimizations

### 3.1 Mutate-in-Place for Progress Updates

**File:** `frontend/src/lib/state/downloads.svelte.ts`

Replace object spread `downloads[idx] = { ...downloads[idx], ... }` with direct property mutation. Svelte 5's `$state` tracks property-level reactivity — spreading creates a full clone on every tick.

### 3.2 Module-Level Icon Map

**File:** `frontend/src/lib/components/DownloadRow.svelte`

The `fileIcon` derived block recreates 7 arrays and runs `includes()` on every render. Move the extension-to-icon mapping to a module-level `Map<string, string>` built once at import time. Single `map.get(ext)` per render instead of 7 `includes()` calls.

### 3.3 Loop-Based formatBytes

**File:** `frontend/src/lib/utils/format.ts`

Replace `Math.log(n) / Math.log(1024)` with a simple loop dividing by 1024. Same result, avoids transcendental math on the hot render path (~500 calls/sec with 50 downloads).

### 3.4 Throttle Drag-Over

**File:** `frontend/src/lib/components/DownloadList.svelte`

`handleDragOver` calls `getBoundingClientRect()` on every pixel movement (10-100 Hz). Add a timestamp guard to skip if <50ms since last update, capping at ~20 Hz.

### 3.5 Skip Polling for Completed Downloads

**File:** `frontend/src/lib/components/DownloadDetailsDialog.svelte`

The 1s polling interval runs even for completed/failed downloads where segments never change. Skip polling when status is terminal.

---

## Verification

```bash
go test ./...    # all existing tests pass
make build       # full binary builds
```

Manual check: start 3+ concurrent downloads, verify progress still updates smoothly in GUI and WebSocket clients.
