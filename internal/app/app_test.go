package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/testutil"
)

// setupTestApp creates a fully wired App backed by a real SQLite database,
// event bus, engine, and queue manager rooted in a temporary directory.
func setupTestApp(t *testing.T) *App {
	t.Helper()

	tmpDir := t.TempDir()

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmpDir
	cfg.MaxRetries = 3

	bus := event.NewBus()

	eng := engine.New(store, cfg, bus)

	queueMgr := queue.New(store, bus, cfg.MaxConcurrent, func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	}, func(ctx context.Context, id string) error {
		return eng.PauseDownload(ctx, id)
	})

	// Run queue loop in background so enqueued downloads are started.
	queueCtx, queueCancel := context.WithCancel(context.Background())
	t.Cleanup(queueCancel)
	go queueMgr.Run(queueCtx)

	return New(eng, store, cfg, bus, queueMgr)
}

func TestAddDownload(t *testing.T) {
	const fileSize = 1024 * 50 // 50 KB
	ts := testutil.NewTestServer(fileSize)
	defer ts.Close()

	a := setupTestApp(t)
	// Replace engine with one that uses the test server's HTTP client.
	a.engine = engine.NewWithClient(a.store, a.cfg, a.bus, ts.Client())

	dl, err := a.AddDownload(model.AddRequest{
		URL:      ts.URL + "/testfile.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	if dl.Download == nil {
		t.Fatal("expected non-nil download")
	}
	if dl.Download.ID == "" {
		t.Fatal("expected non-empty download ID")
	}
	if dl.Download.Filename == "" {
		t.Fatal("expected non-empty filename")
	}
	if dl.Download.TotalSize != fileSize {
		t.Errorf("TotalSize = %d, want %d", dl.Download.TotalSize, fileSize)
	}
}

func TestListDownloads_Empty(t *testing.T) {
	a := setupTestApp(t)

	downloads, err := a.ListDownloads("", 0, 0)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if downloads == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(downloads) != 0 {
		t.Errorf("len = %d, want 0", len(downloads))
	}
}

func TestGetDownload_NotFound(t *testing.T) {
	a := setupTestApp(t)

	_, err := a.GetDownload("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent download, got nil")
	}
}

func TestGetConfig(t *testing.T) {
	a := setupTestApp(t)

	sc := a.GetConfig()

	if sc.DownloadDir != a.cfg.DownloadDir {
		t.Errorf("DownloadDir = %q, want %q", sc.DownloadDir, a.cfg.DownloadDir)
	}
	if sc.MaxConcurrent != a.cfg.MaxConcurrent {
		t.Errorf("MaxConcurrent = %d, want %d", sc.MaxConcurrent, a.cfg.MaxConcurrent)
	}
	if sc.DefaultSegments != a.cfg.DefaultSegments {
		t.Errorf("DefaultSegments = %d, want %d", sc.DefaultSegments, a.cfg.DefaultSegments)
	}
	if sc.GlobalSpeedLimit != a.cfg.GlobalSpeedLimit {
		t.Errorf("GlobalSpeedLimit = %d, want %d", sc.GlobalSpeedLimit, a.cfg.GlobalSpeedLimit)
	}
	if sc.ServerPort != a.cfg.ServerPort {
		t.Errorf("ServerPort = %d, want %d", sc.ServerPort, a.cfg.ServerPort)
	}
	if sc.MinimizeToTray != a.cfg.MinimizeToTray {
		t.Errorf("MinimizeToTray = %v, want %v", sc.MinimizeToTray, a.cfg.MinimizeToTray)
	}
	if sc.MaxRetries != a.cfg.MaxRetries {
		t.Errorf("MaxRetries = %d, want %d", sc.MaxRetries, a.cfg.MaxRetries)
	}
	if sc.Theme != a.cfg.Theme {
		t.Errorf("Theme = %q, want %q", sc.Theme, a.cfg.Theme)
	}
}

func TestUpdateConfig(t *testing.T) {
	a := setupTestApp(t)

	newMax := 5
	err := a.UpdateConfig(ConfigUpdate{
		MaxConcurrent: &newMax,
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	if a.cfg.MaxConcurrent != 5 {
		t.Errorf("MaxConcurrent = %d, want 5", a.cfg.MaxConcurrent)
	}
}

func TestUpdateConfig_Invalid(t *testing.T) {
	a := setupTestApp(t)

	invalid := 0
	err := a.UpdateConfig(ConfigUpdate{
		MaxConcurrent: &invalid,
	})
	if err == nil {
		t.Fatal("expected validation error for MaxConcurrent=0, got nil")
	}
}

func TestGetStats(t *testing.T) {
	a := setupTestApp(t)

	stats := a.GetStats()

	if stats.Active != 0 {
		t.Errorf("Active = %d, want 0", stats.Active)
	}
	if stats.Queued != 0 {
		t.Errorf("Queued = %d, want 0", stats.Queued)
	}
	if stats.Completed != 0 {
		t.Errorf("Completed = %d, want 0", stats.Completed)
	}
}

func TestPauseDownload_InvalidID(t *testing.T) {
	a := setupTestApp(t)

	err := a.PauseDownload("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for pausing nonexistent download, got nil")
	}
}

func TestResumeDownload_InvalidID(t *testing.T) {
	a := setupTestApp(t)

	err := a.ResumeDownload("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for resuming nonexistent download, got nil")
	}
}

func TestCancelDownload_InvalidID(t *testing.T) {
	a := setupTestApp(t)

	err := a.CancelDownload("nonexistent-id", false)
	if err == nil {
		t.Fatal("expected error for cancelling nonexistent download, got nil")
	}
}

func TestClearCompleted(t *testing.T) {
	const fileSize = 1024 * 50 // 50 KB
	ts := testutil.NewTestServer(fileSize)
	defer ts.Close()

	a := setupTestApp(t)
	// Replace engine with one that uses the test server's HTTP client.
	a.engine = engine.NewWithClient(a.store, a.cfg, a.bus, ts.Client())

	// Re-create queue manager so its startFn uses the new engine.
	queueCtx, queueCancel := context.WithCancel(context.Background())
	defer queueCancel()
	a.queue = queue.New(a.store, a.bus, a.cfg.MaxConcurrent, func(ctx context.Context, id string) error {
		return a.engine.StartDownload(ctx, id)
	}, func(ctx context.Context, id string) error {
		return a.engine.PauseDownload(ctx, id)
	})
	go a.queue.Run(queueCtx)

	// Subscribe to the event bus to wait for completion.
	ch, subID := a.bus.Subscribe()
	defer a.bus.Unsubscribe(subID)

	dl, err := a.AddDownload(model.AddRequest{
		URL:      ts.URL + "/testfile.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	// Wait for the download to complete.
	timeout := time.After(30 * time.Second)
	completed := false
	for !completed {
		select {
		case evt := <-ch:
			if ce, ok := evt.(event.DownloadCompleted); ok && ce.DownloadID == dl.Download.ID {
				completed = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for download completion")
		}
	}

	// Verify download is completed.
	got, err := a.GetDownload(dl.Download.ID)
	if err != nil {
		t.Fatalf("GetDownload: %v", err)
	}
	if got.Status != model.StatusCompleted {
		t.Fatalf("status = %s, want completed", got.Status)
	}

	// Clear completed downloads.
	if err := a.ClearCompleted(); err != nil {
		t.Fatalf("ClearCompleted: %v", err)
	}

	// Verify list is empty.
	downloads, err := a.ListDownloads("", 0, 0)
	if err != nil {
		t.Fatalf("ListDownloads after clear: %v", err)
	}
	if len(downloads) != 0 {
		t.Errorf("len = %d, want 0 after ClearCompleted", len(downloads))
	}
}

func TestGetAuthToken(t *testing.T) {
	a := setupTestApp(t)

	token := a.GetAuthToken()
	if token == "" {
		t.Fatal("expected non-empty auth token")
	}
	if token != a.cfg.AuthToken {
		t.Errorf("token = %q, want %q", token, a.cfg.AuthToken)
	}
}
