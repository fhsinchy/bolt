# Optimization Pass Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Reduce CPU and memory overhead on hot paths — progress reporting, WebSocket serialization, DB queries, and frontend rendering.

**Architecture:** 10 targeted fixes across Go backend (engine, server, DB) and Svelte frontend (state, components, utils). No API changes, no structural refactors, no new dependencies. All existing tests must continue to pass.

**Tech Stack:** Go 1.23+, Svelte 5, TypeScript 5

**Design doc:** `docs/plans/2026-03-04-optimization-pass-design.md`

**Note:** Design item 3.5 (skip polling for completed downloads) is already implemented in `DownloadDetailsDialog.svelte` lines 72-81 and is excluded from this plan.

---

### Task 1: Speed Window Circular Buffer

**Files:**
- Modify: `internal/engine/progress.go:13-30` (struct fields), `internal/engine/progress.go:63-77` (constructor), `internal/engine/progress.go:150-174` (emitProgress)

**Step 1: Update the progressAggregator struct**

Replace the `speeds []int64` field with circular buffer fields:

```go
// In progressAggregator struct (line 24):
// REMOVE:
//   speeds          []int64
// ADD:
	speeds          [5]int64
	speedIdx        int
	speedCount      int
```

**Step 2: Update the constructor**

In `newProgressAggregator` (line 72), remove the `speeds` initialization:

```go
// REMOVE:
//   speeds:          make([]int64, 0, speedWindowSize),
// The [5]int64 array is zero-valued by default, speedIdx and speedCount default to 0.
```

**Step 3: Update emitProgress**

Replace lines 164-174 in `emitProgress`:

```go
// REMOVE:
//	p.speeds = append(p.speeds, speed)
//	if len(p.speeds) > speedWindowSize {
//		p.speeds = p.speeds[1:]
//	}
//	avgSpeed := int64(0)
//	for _, s := range p.speeds {
//		avgSpeed += s
//	}
//	if len(p.speeds) > 0 {
//		avgSpeed /= int64(len(p.speeds))
//	}

// ADD:
	p.speeds[p.speedIdx] = speed
	p.speedIdx = (p.speedIdx + 1) % speedWindowSize
	if p.speedCount < speedWindowSize {
		p.speedCount++
	}
	avgSpeed := int64(0)
	for i := 0; i < p.speedCount; i++ {
		avgSpeed += p.speeds[i]
	}
	if p.speedCount > 0 {
		avgSpeed /= int64(p.speedCount)
	}
```

**Step 4: Run tests**

Run: `go test ./internal/engine/ -v -run TestIntegration`
Expected: PASS — existing integration tests exercise progress aggregator end-to-end.

**Step 5: Commit**

```bash
git add internal/engine/progress.go
git commit -m "perf: replace speed window slice with circular buffer

Eliminates slice reallocation on every progress emit (~2x/sec per
active download). Fixed-size [5]int64 array with index wrapping."
```

---

### Task 2: Fixed Report Channel Buffer

**Files:**
- Modify: `internal/engine/engine.go:243`

**Step 1: Change the channel buffer size**

```go
// Line 243 — REPLACE:
	reportCh := make(chan segmentReport, len(segments)*100)
// WITH:
	reportCh := make(chan segmentReport, 256)
```

**Step 2: Run tests**

Run: `go test ./internal/engine/ -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/engine/engine.go
git commit -m "perf: use fixed 256-slot report channel buffer

Was segments*100 (1600 for 16 segments). 256 is more than sufficient
for the ~500ms report cadence and avoids scaling allocation."
```

---

### Task 3: Skip Idle Progress Emits

**Files:**
- Modify: `internal/engine/progress.go:13-30` (add field), `internal/engine/progress.go:138-142` (ticker case)

**Step 1: Add lastEmittedBytes field**

```go
// In progressAggregator struct, after totalDownloaded:
	lastEmittedBytes int64
```

**Step 2: Guard the ticker emit**

Replace lines 138-142:

```go
// REPLACE:
		case <-progressTicker.C:
			p.mu.Lock()
			td := p.totalDownloaded
			p.mu.Unlock()
			p.emitProgress(td)
// WITH:
		case <-progressTicker.C:
			p.mu.Lock()
			td := p.totalDownloaded
			p.mu.Unlock()
			if td != p.lastEmittedBytes {
				p.emitProgress(td)
				p.lastEmittedBytes = td
			}
```

**Step 3: Run tests**

Run: `go test ./internal/engine/ -v -count=1`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/engine/progress.go
git commit -m "perf: skip progress emit when no bytes downloaded since last emit

Paused or stalled downloads no longer produce no-op events every 500ms."
```

---

### Task 4: Typed WebSocket Message Structs

**Files:**
- Modify: `internal/server/websocket.go:64-120`

**Step 1: Define typed message structs**

Add before `eventToWSMessage` (after line 62):

```go
type wsProgressMsg struct {
	Type       string `json:"type"`
	DownloadID string `json:"download_id"`
	Downloaded int64  `json:"downloaded"`
	TotalSize  int64  `json:"total_size"`
	Speed      int64  `json:"speed"`
	ETA        int    `json:"eta"`
	Status     string `json:"status"`
}

type wsDownloadAddedMsg struct {
	Type       string `json:"type"`
	DownloadID string `json:"download_id"`
	Filename   string `json:"filename"`
	TotalSize  int64  `json:"total_size"`
}

type wsDownloadCompletedMsg struct {
	Type       string `json:"type"`
	DownloadID string `json:"download_id"`
	Filename   string `json:"filename"`
}

type wsDownloadFailedMsg struct {
	Type       string `json:"type"`
	DownloadID string `json:"download_id"`
	Error      string `json:"error"`
}

type wsDownloadIDMsg struct {
	Type       string `json:"type"`
	DownloadID string `json:"download_id"`
}
```

**Step 2: Update eventToWSMessage return type and body**

Change return type from `map[string]any` to `any`:

```go
func eventToWSMessage(evt event.Event) any {
	switch e := evt.(type) {
	case event.Progress:
		return wsProgressMsg{
			Type: "progress", DownloadID: e.DownloadID,
			Downloaded: e.Downloaded, TotalSize: e.TotalSize,
			Speed: e.Speed, ETA: e.ETA, Status: e.Status,
		}
	case event.DownloadAdded:
		return wsDownloadAddedMsg{
			Type: "download_added", DownloadID: e.DownloadID,
			Filename: e.Filename, TotalSize: e.TotalSize,
		}
	case event.DownloadCompleted:
		return wsDownloadCompletedMsg{
			Type: "download_completed", DownloadID: e.DownloadID,
			Filename: e.Filename,
		}
	case event.DownloadFailed:
		return wsDownloadFailedMsg{
			Type: "download_failed", DownloadID: e.DownloadID,
			Error: e.Error,
		}
	case event.DownloadRemoved:
		return wsDownloadIDMsg{Type: "download_removed", DownloadID: e.DownloadID}
	case event.DownloadPaused:
		return wsDownloadIDMsg{Type: "download_paused", DownloadID: e.DownloadID}
	case event.DownloadResumed:
		return wsDownloadIDMsg{Type: "download_resumed", DownloadID: e.DownloadID}
	case event.RefreshNeeded:
		return wsDownloadIDMsg{Type: "refresh_needed", DownloadID: e.DownloadID}
	default:
		return nil
	}
}
```

**Step 3: Run tests**

Run: `go test ./internal/server/ -v -count=1`
Expected: PASS — the WebSocket test (`TestWebSocket`) checks for `"type"` and `"download_id"` keys in the JSON, which are still present with struct tags.

**Step 4: Commit**

```bash
git add internal/server/websocket.go
git commit -m "perf: replace map[string]any with typed structs for WebSocket messages

Eliminates heap-allocated maps on every progress event (~2x/sec per
active download). Structs use json tags for identical wire format."
```

---

### Task 5: Single Stats Query

**Files:**
- Modify: `internal/db/downloads.go` (add method after `CountByStatus`)
- Modify: `internal/server/handlers.go:226-239` (update handler)
- Test: `internal/db/downloads_test.go` (add test)

**Step 1: Write the failing test**

Add to `internal/db/downloads_test.go`:

```go
func TestCountAllStatuses(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Insert downloads with different statuses.
	for i, status := range []model.Status{model.StatusActive, model.StatusActive, model.StatusQueued, model.StatusCompleted} {
		d := newTestDownload(fmt.Sprintf("d_count_%d", i))
		d.Status = status
		if err := store.InsertDownload(ctx, d); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	counts, err := store.CountAllStatuses(ctx)
	if err != nil {
		t.Fatalf("CountAllStatuses: %v", err)
	}

	if counts["active"] != 2 {
		t.Errorf("active = %d, want 2", counts["active"])
	}
	if counts["queued"] != 1 {
		t.Errorf("queued = %d, want 1", counts["queued"])
	}
	if counts["completed"] != 1 {
		t.Errorf("completed = %d, want 1", counts["completed"])
	}
	// Absent statuses should be 0.
	if counts["paused"] != 0 {
		t.Errorf("paused = %d, want 0", counts["paused"])
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -v -run TestCountAllStatuses`
Expected: FAIL — `CountAllStatuses` method does not exist.

**Step 3: Implement CountAllStatuses**

Add to `internal/db/downloads.go` after `CountByStatus` (after line 213):

```go
// CountAllStatuses returns the count of downloads grouped by status
// in a single query.
func (s *Store) CountAllStatuses(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM downloads GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("count all statuses: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan status count: %w", err)
		}
		counts[status] = count
	}
	return counts, rows.Err()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/db/ -v -run TestCountAllStatuses`
Expected: PASS

**Step 5: Update handleGetStats**

Replace lines 226-239 in `internal/server/handlers.go`:

```go
func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.CountAllStatuses(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active_count":    counts["active"],
		"queued_count":    counts["queued"],
		"completed_count": counts["completed"],
		"version":         "0.3.0-dev",
	})
}
```

**Step 6: Run all server tests**

Run: `go test ./internal/server/ -v -count=1`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/db/downloads.go internal/db/downloads_test.go internal/server/handlers.go
git commit -m "perf: consolidate 3 stats COUNT queries into single GROUP BY

Reduces /api/stats from 3 SQLite round-trips to 1."
```

---

### Task 6: Response Status Struct

**Files:**
- Modify: `internal/server/handlers.go` (add struct, replace 7 map literals)

**Step 1: Define the struct and update all usages**

Add near the top of `handlers.go` (after the imports):

```go
type statusResponse struct {
	Status string `json:"status"`
}
```

Then replace all `map[string]string{"status": "..."}` with `statusResponse{Status: "..."}`:

- Line 78-80: `writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})`
- Line 91-93: `writeJSON(w, http.StatusOK, statusResponse{Status: "paused"})`
- Line 104-106: `writeJSON(w, http.StatusOK, statusResponse{Status: "resumed"})`
- Line 117-119: `writeJSON(w, http.StatusOK, statusResponse{Status: "retrying"})`
- Line 139-141: `writeJSON(w, http.StatusOK, statusResponse{Status: "refreshed"})`
- Line 221-223: `writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})`
- Line 262: `writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})`
- Line 284-286: `writeJSON(w, http.StatusOK, statusResponse{Status: "reordered"})`

**Step 2: Run tests**

Run: `go test ./internal/server/ -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/server/handlers.go
git commit -m "perf: replace inline map[string]string with statusResponse struct

Eliminates 8 map heap allocations across handler responses."
```

---

### Task 7: Frontend — Mutate-in-Place for Progress Updates

**Files:**
- Modify: `frontend/src/lib/state/downloads.svelte.ts:98-122`

**Step 1: Replace object spread with direct mutation**

Replace `updateDownloadProgress` (lines 98-110):

```typescript
function updateDownloadProgress(update: ProgressUpdate) {
  const idx = downloads.findIndex((d) => d.id === update.id);
  if (idx >= 0) {
    downloads[idx].downloaded = update.downloaded;
    if (update.total_size) downloads[idx].total_size = update.total_size;
    downloads[idx].speed = update.speed;
    downloads[idx].eta = update.eta;
    if (update.status) downloads[idx].status = update.status;
  }
}
```

Replace `updateDownloadStatus` (lines 112-122):

```typescript
function updateDownloadStatus(id: string, status: Download["status"]) {
  const idx = downloads.findIndex((d) => d.id === id);
  if (idx >= 0) {
    downloads[idx].status = status;
    if (status !== "active") {
      downloads[idx].speed = 0;
      downloads[idx].eta = 0;
    }
  }
}
```

**Step 2: Verify build**

Run: `cd frontend && pnpm build`
Expected: Build succeeds with no errors.

**Step 3: Commit**

```bash
git add frontend/src/lib/state/downloads.svelte.ts
git commit -m "perf: mutate download properties in-place instead of object spread

Svelte 5 \$state tracks property-level reactivity, so direct mutation
triggers updates without creating a full object clone on every tick."
```

---

### Task 8: Frontend — Module-Level Icon Map

**Files:**
- Modify: `frontend/src/lib/components/DownloadRow.svelte:57-73`

**Step 1: Add module-level icon map**

Add in the `<script>` block, before the `Props` interface (after line 6):

```typescript
const ICON_MAP = new Map<string, string>([
  ...["mp4", "mkv", "avi", "mov", "wmv", "flv", "webm"].map(e => [e, "🎬"] as const),
  ...["mp3", "flac", "wav", "aac", "ogg", "m4a"].map(e => [e, "🎵"] as const),
  ...["jpg", "jpeg", "png", "gif", "bmp", "svg", "webp"].map(e => [e, "🖼"] as const),
  ...["zip", "tar", "gz", "bz2", "xz", "7z", "rar", "zst"].map(e => [e, "📦"] as const),
  ...["pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt"].map(e => [e, "📄"] as const),
  ...["exe", "msi", "dmg", "deb", "rpm", "appimage"].map(e => [e, "⚙"] as const),
  ...["iso", "img"].map(e => [e, "💿"] as const),
]);
```

**Step 2: Replace the fileIcon derived**

Replace lines 57-73:

```typescript
const fileIcon = $derived(ICON_MAP.get(ext) ?? "📁");
```

**Step 3: Verify build**

Run: `cd frontend && pnpm build`
Expected: Build succeeds.

**Step 4: Commit**

```bash
git add frontend/src/lib/components/DownloadRow.svelte
git commit -m "perf: hoist file icon extensions to module-level Map

Single Map.get() lookup per render instead of recreating 7 arrays
and running includes() on each."
```

---

### Task 9: Frontend — Loop-Based formatBytes

**Files:**
- Modify: `frontend/src/lib/utils/format.ts:3-8`

**Step 1: Replace Math.log implementation**

Replace the `formatBytes` function (lines 3-8):

```typescript
export function formatBytes(n: number): string {
  if (n <= 0) return "0 B";
  let i = 0;
  let val = n;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val < 10 ? val.toFixed(1) : Math.round(val)} ${units[i]}`;
}
```

**Step 2: Verify build**

Run: `cd frontend && pnpm build`
Expected: Build succeeds.

**Step 3: Commit**

```bash
git add frontend/src/lib/utils/format.ts
git commit -m "perf: replace Math.log in formatBytes with division loop

Avoids transcendental math on the hot render path (~500 calls/sec
with many downloads). Same output, simpler arithmetic."
```

---

### Task 10: Frontend — Throttle Drag-Over

**Files:**
- Modify: `frontend/src/lib/components/DownloadList.svelte:14-41`

**Step 1: Add throttle timestamp**

Add after the drag state declarations (after line 17):

```typescript
let lastDragOverTime = 0;
```

**Step 2: Add guard to handleDragOver**

Replace the `handleDragOver` function (lines 31-41):

```typescript
function handleDragOver(e: DragEvent, id: string) {
  if (!draggedId || draggedId === id || isSearching) return;
  e.preventDefault();
  if (e.dataTransfer) e.dataTransfer.dropEffect = "move";

  const now = performance.now();
  if (now - lastDragOverTime < 50) return;
  lastDragOverTime = now;

  const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
  const midY = rect.top + rect.height / 2;
  dropPosition = e.clientY < midY ? "above" : "below";
  dropTargetId = id;
}
```

Note: `e.preventDefault()` must remain before the throttle guard so the browser accepts the drop. The throttle only skips the expensive `getBoundingClientRect()` and state updates.

**Step 3: Verify build**

Run: `cd frontend && pnpm build`
Expected: Build succeeds.

**Step 4: Commit**

```bash
git add frontend/src/lib/components/DownloadList.svelte
git commit -m "perf: throttle drag-over to ~20Hz

Skips getBoundingClientRect() and state updates if <50ms since last
call. Prevents layout thrashing during drag operations."
```

---

### Task 11: Final Verification

**Step 1: Run all Go tests**

Run: `go test ./... -count=1`
Expected: All PASS.

**Step 2: Build full binary**

Run: `make build`
Expected: Builds successfully (frontend + Go binary).

**Step 3: Run with race detector**

Run: `go test ./... -race -count=1`
Expected: All PASS, no data races.

**Step 4: Final commit (squash frontend changes)**

All individual commits are already made. No squash needed — each is atomic and independently revertible.
