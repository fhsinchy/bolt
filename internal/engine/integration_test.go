package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/testutil"
)

// TestIntegration_ExitCriteria validates the Phase 1 exit criteria:
// Download a file in 16 segments, pause mid-download, shutdown the engine,
// create a new engine from the same DB, resume, and verify byte-for-byte integrity.
func TestIntegration_ExitCriteria(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	const fileSize = 10 * 1024 * 1024 // 10 MB
	const segmentCount = 16

	// Serve 10MB of deterministic data with enough latency to reliably pause
	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(5*time.Millisecond))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "bolt.db")
	expected := testutil.GenerateData(fileSize)

	var dlID string
	var dlFilename string

	// Phase 1: Start download, wait for ~50% progress, then shutdown
	{
		store, err := db.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}

		cfg := config.DefaultConfig()
		cfg.DownloadDir = tmpDir
		cfg.MaxRetries = 5
		cfg.MinSegmentSize = 1024

		tc := newTestCallbacks()
		eng := New(store, func() config.Config { return *cfg }, tc.callbacks)
		eng.client = ts.Client()

		ctx := context.Background()
		dl, err := eng.AddDownload(ctx, model.AddRequest{
			URL:      ts.URL + "/large-file.bin",
			Segments: segmentCount,
		})
		if err != nil {
			t.Fatalf("AddDownload: %v", err)
		}
		dlID = dl.ID
		dlFilename = dl.Filename

		if dl.SegmentCount != segmentCount {
			t.Fatalf("segment count = %d, want %d", dl.SegmentCount, segmentCount)
		}

		if err := eng.StartDownload(ctx, dl.ID); err != nil {
			t.Fatalf("StartDownload: %v", err)
		}

		// Wait for ~50% progress
		target := int64(fileSize / 2)
		timeout := time.After(60 * time.Second)
		reached := false
		for !reached {
			select {
			case p := <-tc.progressCh:
				if p.Downloaded >= target {
					reached = true
				}
			case <-tc.completedCh:
				// Download finished before we could pause — still valid
				reached = true
			case <-timeout:
				t.Fatal("timed out waiting for 50% progress")
			}
		}

		// Shutdown (simulating process kill and restart)
		if err := eng.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}

		// Verify persisted state
		got, err := store.GetDownload(ctx, dl.ID)
		if err != nil {
			t.Fatalf("GetDownload after shutdown: %v", err)
		}
		t.Logf("Phase 1 - Status: %s, Downloaded: %d/%d (%.0f%%)",
			got.Status, got.Downloaded, got.TotalSize,
			float64(got.Downloaded)/float64(got.TotalSize)*100)

		segments, err := store.GetSegments(ctx, dl.ID)
		if err != nil {
			t.Fatal(err)
		}

		// Verify segments: some should be partial, some possibly complete
		var doneCount, partialCount int
		for _, seg := range segments {
			if seg.Done {
				doneCount++
			} else if seg.Downloaded > 0 {
				partialCount++
			}
		}
		t.Logf("Phase 1 - Segments: %d done, %d partial, %d total",
			doneCount, partialCount, len(segments))

		store.Close()
	}

	// Phase 2: New engine, resume from DB
	{
		store, err := db.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		cfg := config.DefaultConfig()
		cfg.DownloadDir = tmpDir
		cfg.MaxRetries = 5
		cfg.MinSegmentSize = 1024

		tc := newTestCallbacks()
		eng := New(store, func() config.Config { return *cfg }, tc.callbacks)
		eng.client = ts.Client()

		ctx := context.Background()

		// Resume the download
		if err := eng.ResumeDownload(ctx, dlID); err != nil {
			t.Fatalf("ResumeDownload: %v", err)
		}

		// Wait for completion
		timeout := time.After(120 * time.Second)
		select {
		case <-tc.completedCh:
		case id := <-tc.failedCh:
			t.Fatalf("Download failed after resume: %s", id)
		case <-timeout:
			t.Fatal("timed out waiting for completion after resume")
		}

		// Verify DB status
		got, err := store.GetDownload(ctx, dlID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != model.StatusCompleted {
			t.Errorf("final status = %s, want completed", got.Status)
		}
		t.Logf("Phase 2 - Status: %s, Downloaded: %d/%d",
			got.Status, got.Downloaded, got.TotalSize)

		// Verify all segments are done
		segments, err := store.GetSegments(ctx, dlID)
		if err != nil {
			t.Fatal(err)
		}
		for _, seg := range segments {
			if !seg.Done {
				t.Errorf("segment %d not done after completion", seg.Index)
			}
		}

		// Verify file byte-for-byte
		filePath := filepath.Join(tmpDir, dlFilename)
		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("reading file: %v", err)
		}
		if len(data) != len(expected) {
			t.Fatalf("file size = %d, want %d", len(data), len(expected))
		}

		mismatches := 0
		for i := range data {
			if data[i] != expected[i] {
				if mismatches == 0 {
					t.Errorf("first byte mismatch at offset %d: got %d, want %d", i, data[i], expected[i])
				}
				mismatches++
			}
		}
		if mismatches > 0 {
			t.Errorf("total byte mismatches: %d out of %d", mismatches, len(data))
		} else {
			t.Log("Phase 2 - File integrity verified: all bytes match")
		}
	}
}

// TestIntegration_MultiSegmentComplete verifies a straightforward download
// with multiple segments completes correctly.
func TestIntegration_MultiSegmentComplete(t *testing.T) {
	const fileSize = 1024 * 1024 // 1 MB
	const segmentCount = 8

	ts := testutil.NewTestServer(fileSize)
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "bolt.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmpDir
	cfg.MaxRetries = 3
	cfg.MinSegmentSize = 1024

	tc := newTestCallbacks()
	eng := New(store, func() config.Config { return *cfg }, tc.callbacks)
	eng.client = ts.Client()

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/data.bin",
		Segments: segmentCount,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatal(err)
	}

	select {
	case <-tc.completedCh:
	case id := <-tc.failedCh:
		t.Fatalf("download failed: %s", id)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out")
	}

	// Verify integrity
	expected := testutil.GenerateData(fileSize)
	data, err := os.ReadFile(filepath.Join(tmpDir, dl.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(expected) {
		t.Fatalf("file size = %d, want %d", len(data), len(expected))
	}
	for i := range data {
		if data[i] != expected[i] {
			t.Fatalf("byte mismatch at %d", i)
		}
	}
}

// TestIntegration_RetryOnTransientErrors verifies that segment workers
// retry on transient errors and eventually complete.
func TestIntegration_RetryOnTransientErrors(t *testing.T) {
	const fileSize = 1024 * 100 // 100 KB

	// Server fails after 10KB for the first 2 requests per segment
	ts := testutil.NewTestServer(fileSize, testutil.WithFailAfterBytes(10*1024, 8))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "bolt.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.DefaultConfig()
	cfg.DownloadDir = tmpDir
	cfg.MaxRetries = 10
	cfg.MinSegmentSize = 1024

	tc := newTestCallbacks()
	eng := New(store, func() config.Config { return *cfg }, tc.callbacks)
	eng.client = ts.Client()

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/file.bin",
		Segments: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatal(err)
	}

	select {
	case <-tc.completedCh:
	case id := <-tc.failedCh:
		t.Fatalf("download failed: %s", id)
	case <-time.After(60 * time.Second):
		t.Fatal("timed out")
	}

	expected := testutil.GenerateData(fileSize)
	data, err := os.ReadFile(filepath.Join(tmpDir, dl.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(expected) {
		t.Fatalf("file size = %d, want %d", len(data), len(expected))
	}
	for i := range data {
		if data[i] != expected[i] {
			t.Fatalf("byte mismatch at %d", i)
		}
	}
}
