package queue

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
)

func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func insertQueuedDownload(t *testing.T, store *db.Store, id string, order int) {
	t.Helper()
	ctx := context.Background()
	dl := &model.Download{
		ID:           id,
		URL:          "https://example.com/" + id,
		Filename:     id + ".bin",
		Dir:          t.TempDir(),
		TotalSize:    1024,
		Status:       model.StatusQueued,
		SegmentCount: 1,
		QueueOrder:   order,
	}
	if err := store.InsertDownload(ctx, dl); err != nil {
		t.Fatal(err)
	}
}

func insertDownload(t *testing.T, store *db.Store, id string, order int, status model.Status) {
	t.Helper()
	ctx := context.Background()
	dl := &model.Download{
		ID:           id,
		URL:          "https://example.com/" + id,
		Filename:     id + ".bin",
		Dir:          t.TempDir(),
		TotalSize:    1024,
		Status:       status,
		SegmentCount: 1,
		QueueOrder:   order,
	}
	if err := store.InsertDownload(ctx, dl); err != nil {
		t.Fatal(err)
	}
}

func noopPauseFn(ctx context.Context, id string) error { return nil }

// startFnThatSetsActive returns a startFn that sets download status to active
// (mimicking what engine.StartDownload does) and records started IDs.
func startFnThatSetsActive(store *db.Store, mu *sync.Mutex, started *[]string) StartFunc {
	return func(ctx context.Context, id string) error {
		_ = store.UpdateDownloadStatus(ctx, id, model.StatusActive, "")
		mu.Lock()
		*started = append(*started, id)
		mu.Unlock()
		return nil
	}
}

func TestQueue_MaxConcurrent(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	var mu sync.Mutex
	started := make([]string, 0)

	mgr := New(store, bus, 3, startFnThatSetsActive(store, &mu, &started), noopPauseFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	// Insert 5 queued downloads
	for i := 0; i < 5; i++ {
		id := model.NewDownloadID()
		insertQueuedDownload(t, store, id, i)
		mgr.Enqueue(id)
	}

	// Wait for evaluation
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count := len(started)
	mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 started, got %d", count)
	}

	if mgr.ActiveCount() != 3 {
		t.Errorf("active count = %d, want 3", mgr.ActiveCount())
	}
}

func TestQueue_CompleteTriggersNext(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	var mu sync.Mutex
	started := make([]string, 0)

	mgr := New(store, bus, 2, startFnThatSetsActive(store, &mu, &started), noopPauseFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	ids := make([]string, 4)
	for i := 0; i < 4; i++ {
		ids[i] = model.NewDownloadID()
		insertQueuedDownload(t, store, ids[i], i)
		mgr.Enqueue(ids[i])
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count1 := len(started)
	mu.Unlock()
	if count1 != 2 {
		t.Fatalf("expected 2 started initially, got %d", count1)
	}

	// Complete one download (set status so it's no longer active)
	_ = store.UpdateDownloadStatus(ctx, ids[0], model.StatusCompleted, "")
	mgr.OnDownloadComplete(ids[0])

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count2 := len(started)
	mu.Unlock()
	if count2 != 3 {
		t.Errorf("expected 3 started after completion, got %d", count2)
	}
}

func TestQueue_EmptyQueue(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	called := false
	startFn := func(ctx context.Context, id string) error {
		called = true
		return nil
	}

	mgr := New(store, bus, 3, startFn, noopPauseFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	// Signal with empty queue
	mgr.signal()
	time.Sleep(100 * time.Millisecond)

	if called {
		t.Error("startFn should not have been called with empty queue")
	}
}

func TestMaxConcurrentChanged_MidFlight(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	var mu sync.Mutex
	started := make([]string, 0)

	mgr := New(store, bus, 2, startFnThatSetsActive(store, &mu, &started), noopPauseFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	// Insert 5 queued downloads and enqueue them.
	for i := 0; i < 5; i++ {
		id := model.NewDownloadID()
		insertQueuedDownload(t, store, id, i)
		mgr.Enqueue(id)
	}

	// Wait for initial evaluation with MaxConcurrent=2.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count1 := len(started)
	mu.Unlock()
	if count1 != 2 {
		t.Fatalf("expected 2 started initially, got %d", count1)
	}

	// Raise concurrency limit mid-flight.
	mgr.SetMaxConcurrent(5)

	// Wait for re-evaluation.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count2 := len(started)
	mu.Unlock()
	if count2 < 4 {
		t.Errorf("expected at least 4 started after SetMaxConcurrent(5), got %d", count2)
	}
}

func TestSetMaxConcurrent_PausesExcess(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	var mu sync.Mutex
	paused := make([]string, 0)

	startFn := func(ctx context.Context, id string) error {
		return nil
	}
	pauseFn := func(ctx context.Context, id string) error {
		_ = store.UpdateDownloadStatus(ctx, id, model.StatusPaused, "")
		mu.Lock()
		paused = append(paused, id)
		mu.Unlock()
		return nil
	}

	mgr := New(store, bus, 4, startFn, pauseFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	// Insert 4 downloads as active (simulating 4 running downloads)
	for i := 0; i < 4; i++ {
		id := model.NewDownloadID()
		insertDownload(t, store, id, i, model.StatusActive)
	}

	// Reduce max concurrent to 2
	mgr.SetMaxConcurrent(2)

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	pausedCount := len(paused)
	mu.Unlock()

	if pausedCount != 2 {
		t.Errorf("expected 2 paused, got %d", pausedCount)
	}
}

func TestEnqueueResume_RespectsLimit(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	var mu sync.Mutex
	started := make([]string, 0)

	mgr := New(store, bus, 1, startFnThatSetsActive(store, &mu, &started), noopPauseFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	// Insert 1 active download (at capacity)
	activeID := model.NewDownloadID()
	insertDownload(t, store, activeID, 0, model.StatusActive)

	// Insert a paused download
	pausedID := model.NewDownloadID()
	insertDownload(t, store, pausedID, 1, model.StatusPaused)

	// EnqueueResume should set it to queued but not start it (at capacity)
	err := mgr.EnqueueResume(ctx, pausedID)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify: download should be queued, not started
	dl, err := store.GetDownload(ctx, pausedID)
	if err != nil {
		t.Fatal(err)
	}
	if dl.Status != model.StatusQueued {
		t.Errorf("expected status queued, got %s", dl.Status)
	}

	mu.Lock()
	startedCount := len(started)
	mu.Unlock()
	if startedCount != 0 {
		t.Errorf("expected 0 started (at capacity), got %d", startedCount)
	}
}

func TestEnqueueResumeAll(t *testing.T) {
	store := openTestStore(t)
	bus := event.NewBus()

	var mu sync.Mutex
	started := make([]string, 0)

	mgr := New(store, bus, 2, startFnThatSetsActive(store, &mu, &started), noopPauseFn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.Run(ctx)

	// Insert 3 paused downloads
	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		ids[i] = model.NewDownloadID()
		insertDownload(t, store, ids[i], i, model.StatusPaused)
	}

	err := mgr.EnqueueResumeAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	// Only 2 should start (max concurrent = 2)
	mu.Lock()
	startedCount := len(started)
	mu.Unlock()
	if startedCount != 2 {
		t.Errorf("expected 2 started, got %d", startedCount)
	}

	// Third should be queued
	dl, err := store.GetDownload(ctx, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if dl.Status != model.StatusQueued {
		t.Errorf("expected third download to be queued, got %s", dl.Status)
	}
}
