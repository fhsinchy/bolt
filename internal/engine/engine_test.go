package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/testutil"
)

func setupEngine(t *testing.T) (*Engine, *db.Store, *event.Bus, string) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmpDir
	cfg.MaxRetries = 3
	cfg.MinSegmentSize = 1024

	bus := event.NewBus()
	eng := New(store, cfg, bus)
	return eng, store, bus, tmpDir
}

func TestEngine_AddAndComplete(t *testing.T) {
	const fileSize = 1024 * 50 // 50 KB
	ts := testutil.NewTestServer(fileSize)
	defer ts.Close()

	eng, store, bus, tmpDir := setupEngine(t)
	eng.client = ts.Client()

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	if dl.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if dl.TotalSize != fileSize {
		t.Errorf("TotalSize = %d, want %d", dl.TotalSize, fileSize)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}

	timeout := time.After(15 * time.Second)
	completed := false
	for !completed {
		select {
		case evt := <-ch:
			if _, ok := evt.(event.DownloadCompleted); ok {
				completed = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for completion")
		}
	}

	got, err := store.GetDownload(ctx, dl.ID)
	if err != nil {
		t.Fatalf("GetDownload: %v", err)
	}
	if got.Status != model.StatusCompleted {
		t.Errorf("status = %s, want completed", got.Status)
	}

	filePath := filepath.Join(tmpDir, dl.Filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	expected := testutil.GenerateData(fileSize)
	if len(data) != len(expected) {
		t.Fatalf("file size = %d, want %d", len(data), len(expected))
	}
	for i := range data {
		if data[i] != expected[i] {
			t.Fatalf("byte mismatch at %d", i)
		}
	}
}

func TestEngine_PauseAndResume(t *testing.T) {
	const fileSize = 1024 * 500 // 500 KB
	// Each segment request takes 50ms, giving time to pause
	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(50*time.Millisecond))
	defer ts.Close()

	eng, store, bus, tmpDir := setupEngine(t)
	eng.client = ts.Client()

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}

	// Wait for some progress to arrive
	time.Sleep(100 * time.Millisecond)

	if err := eng.PauseDownload(ctx, dl.ID); err != nil {
		t.Fatalf("PauseDownload: %v", err)
	}

	got, err := store.GetDownload(ctx, dl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusPaused {
		t.Errorf("status = %s, want paused", got.Status)
	}

	// Resume
	if err := eng.ResumeDownload(ctx, dl.ID); err != nil {
		t.Fatalf("ResumeDownload: %v", err)
	}

	timeout := time.After(60 * time.Second)
	completed := false
	for !completed {
		select {
		case evt := <-ch:
			if _, ok := evt.(event.DownloadCompleted); ok {
				completed = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for completion after resume")
		}
	}

	filePath := filepath.Join(tmpDir, dl.Filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := testutil.GenerateData(fileSize)
	if len(data) != len(expected) {
		t.Fatalf("file size = %d, want %d", len(data), len(expected))
	}
	for i := range data {
		if data[i] != expected[i] {
			t.Fatalf("byte mismatch at %d after resume", i)
		}
	}
}

func TestEngine_CancelWithFileDelete(t *testing.T) {
	const fileSize = 1024 * 10
	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(20*time.Millisecond))
	defer ts.Close()

	eng, _, _, tmpDir := setupEngine(t)
	eng.client = ts.Client()

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := eng.CancelDownload(ctx, dl.ID, true); err != nil {
		t.Fatalf("CancelDownload: %v", err)
	}

	filePath := filepath.Join(tmpDir, dl.Filename)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}

	_, err = eng.store.GetDownload(ctx, dl.ID)
	if err != model.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEngine_SingleConnectionFallback(t *testing.T) {
	const fileSize = 1024 * 10
	ts := testutil.NewTestServer(fileSize, testutil.WithNoRangeSupport())
	defer ts.Close()

	eng, _, bus, _ := setupEngine(t)
	eng.client = ts.Client()

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 8,
	})
	if err != nil {
		t.Fatal(err)
	}

	if dl.SegmentCount != 1 {
		t.Errorf("segment count = %d, want 1 (no range support)", dl.SegmentCount)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(10 * time.Second)
	completed := false
	for !completed {
		select {
		case evt := <-ch:
			if _, ok := evt.(event.DownloadCompleted); ok {
				completed = true
			}
		case <-timeout:
			t.Fatal("timed out")
		}
	}
}

func TestEngine_CrashRecovery(t *testing.T) {
	const fileSize = 1024 * 500 // 500 KB
	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(50*time.Millisecond))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	var dlID string

	// Phase 1: Start a download, shutdown mid-progress
	{
		store, err := db.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}

		cfg := config.DefaultConfig()
		cfg.DownloadDir = tmpDir
		cfg.MaxRetries = 3
		cfg.MinSegmentSize = 1024
		bus := event.NewBus()
		eng := New(store, cfg, bus)
		eng.client = ts.Client()

		ctx := context.Background()
		dl, err := eng.AddDownload(ctx, model.AddRequest{
			URL:      ts.URL + "/file.bin",
			Segments: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		dlID = dl.ID

		if err := eng.StartDownload(ctx, dl.ID); err != nil {
			t.Fatal(err)
		}

		// Wait for some progress
		time.Sleep(100 * time.Millisecond)

		if err := eng.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}

		got, err := store.GetDownload(ctx, dl.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != model.StatusPaused {
			t.Errorf("after shutdown: status = %s, want paused", got.Status)
		}

		store.Close()
	}

	// Phase 2: New engine, simulate crash recovery by setting status to active
	{
		store, err := db.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		cfg := config.DefaultConfig()
		cfg.DownloadDir = tmpDir
		cfg.MaxRetries = 3
		cfg.MinSegmentSize = 1024
		bus := event.NewBus()
		eng := New(store, cfg, bus)
		eng.client = ts.Client()

		ch, subID := bus.Subscribe()
		defer bus.Unsubscribe(subID)

		ctx := context.Background()

		// Set download to active so Start() picks it up (simulating crash mid-download)
		_ = store.UpdateDownloadStatus(ctx, dlID, model.StatusActive, "")

		if err := eng.Start(ctx); err != nil {
			t.Fatal(err)
		}

		timeout := time.After(60 * time.Second)
		completed := false
		for !completed {
			select {
			case evt := <-ch:
				if _, ok := evt.(event.DownloadCompleted); ok {
					completed = true
				}
			case <-timeout:
				t.Fatal("timed out waiting for crash recovery completion")
			}
		}

		// Verify file integrity
		dl, err := store.GetDownload(ctx, dlID)
		if err != nil {
			t.Fatal(err)
		}
		filePath := filepath.Join(tmpDir, dl.Filename)
		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatal(err)
		}
		expected := testutil.GenerateData(fileSize)
		if len(data) != len(expected) {
			t.Fatalf("file size = %d, want %d", len(data), len(expected))
		}
		for i := range data {
			if data[i] != expected[i] {
				t.Fatalf("byte mismatch at %d after recovery", i)
			}
		}
	}
}

func TestEngine_RefreshURL(t *testing.T) {
	const fileSize = 1024 * 10
	ts := testutil.NewTestServer(fileSize)
	defer ts.Close()

	eng, store, _, _ := setupEngine(t)
	eng.client = ts.Client()

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = store.UpdateDownloadStatus(ctx, dl.ID, model.StatusError, "link expired")

	if err := eng.RefreshURL(ctx, dl.ID, ts.URL+"/new-file.bin", nil); err != nil {
		t.Fatalf("RefreshURL: %v", err)
	}

	got, err := store.GetDownload(ctx, dl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusPaused {
		t.Errorf("status = %s, want paused", got.Status)
	}
}

func TestEngine_RefreshURL_SizeMismatch(t *testing.T) {
	ts1 := testutil.NewTestServer(1024 * 10)
	defer ts1.Close()
	ts2 := testutil.NewTestServer(1024 * 20)
	defer ts2.Close()

	eng, store, _, _ := setupEngine(t)
	eng.client = ts1.Client()

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts1.URL + "/file.bin",
		Segments: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = store.UpdateDownloadStatus(ctx, dl.ID, model.StatusError, "expired")

	eng.client = ts2.Client()
	err = eng.RefreshURL(ctx, dl.ID, ts2.URL+"/bigger.bin", nil)
	if err == nil {
		t.Fatal("expected size mismatch error")
	}
}

func TestComputeSegments(t *testing.T) {
	segs := computeSegments("d_test", 100, 3)
	if len(segs) != 3 {
		t.Fatalf("len = %d, want 3", len(segs))
	}

	total := int64(0)
	for i, seg := range segs {
		if seg.Index != i {
			t.Errorf("seg[%d].Index = %d", i, seg.Index)
		}
		size := seg.EndByte - seg.StartByte + 1
		total += size
		if i > 0 {
			if seg.StartByte != segs[i-1].EndByte+1 {
				t.Errorf("gap between segments %d and %d", i-1, i)
			}
		}
	}
	if total != 100 {
		t.Errorf("total coverage = %d, want 100", total)
	}
}

func TestAddDownload_ZeroContentLength(t *testing.T) {
	// Server returns Content-Length: 0 with Accept-Ranges: bytes on HEAD.
	// The engine should fall back to a single segment because TotalSize <= 0.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	eng, _, _, _ := setupEngine(t)
	eng.client = ts.Client()

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/empty.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	if dl.SegmentCount != 1 {
		t.Errorf("SegmentCount = %d, want 1 (zero content-length should force single segment)", dl.SegmentCount)
	}
}

func TestAddDownload_RedirectChain(t *testing.T) {
	const fileSize = 1024 * 10 // 10 KB

	// File server that serves the actual content.
	fileServer := testutil.NewTestServer(fileSize)
	defer fileServer.Close()

	// Redirect server that 302-redirects to the file server.
	redirectServer := testutil.NewRedirectServer(fileServer.URL + "/final.bin")
	defer redirectServer.Close()

	eng, _, _, _ := setupEngine(t)
	// Use a plain http.Client so it can reach both httptest servers.
	eng.client = &http.Client{}

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      redirectServer.URL + "/start.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	if dl.TotalSize != fileSize {
		t.Errorf("TotalSize = %d, want %d", dl.TotalSize, fileSize)
	}
}

func TestValidateAddRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     model.AddRequest
		wantErr bool
	}{
		{"valid", model.AddRequest{URL: "https://example.com/file.bin"}, false},
		{"empty URL", model.AddRequest{URL: ""}, true},
		{"invalid scheme", model.AddRequest{URL: "ftp://example.com/file"}, true},
		{"segments too high", model.AddRequest{URL: "https://example.com/file", Segments: 33}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAddRequest(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAddRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
