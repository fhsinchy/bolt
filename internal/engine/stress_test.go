//go:build stress

package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/testutil"
)

func TestStress_ConcurrentQueuePressure(t *testing.T) {
	const (
		fileSize       = 50 * 1024 // 50 KB
		totalDownloads = 20
		maxConcurrent  = 3
	)

	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(10*time.Millisecond))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "stress.db")
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
	eng := NewWithClient(store, cfg, bus, ts.Client())

	queueMgr := queue.New(store, bus, maxConcurrent, func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	}, func(ctx context.Context, id string) error {
		return eng.PauseDownload(ctx, id)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go queueMgr.Run(ctx)

	// Subscribe to events
	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	// Add and enqueue all downloads
	ids := make(map[string]bool, totalDownloads)
	for i := 0; i < totalDownloads; i++ {
		dl, err := eng.AddDownload(ctx, model.AddRequest{
			URL:      fmt.Sprintf("%s/file-%d.bin", ts.URL, i),
			Segments: 4,
		})
		if err != nil {
			t.Fatalf("AddDownload %d: %v", i, err)
		}
		ids[dl.ID] = false
		queueMgr.Enqueue(dl.ID)
	}

	// Wait for all downloads to complete
	timeout := time.After(120 * time.Second)
	completedCount := 0
	for completedCount < totalDownloads {
		select {
		case evt := <-ch:
			switch e := evt.(type) {
			case event.DownloadCompleted:
				if _, ok := ids[e.DownloadID]; ok {
					ids[e.DownloadID] = true
					completedCount++
					queueMgr.OnDownloadComplete(e.DownloadID)
				}
			case event.DownloadFailed:
				t.Errorf("download %s failed: %s", e.DownloadID, e.Error)
				completedCount++ // count failures to avoid infinite loop
				queueMgr.OnDownloadComplete(e.DownloadID)
			}
		case <-timeout:
			t.Fatalf("timed out: %d/%d completed", completedCount, totalDownloads)
		}
	}

	// Verify all completed
	for id, done := range ids {
		if !done {
			t.Errorf("download %s did not complete", id)
		}
	}
}

func TestStress_RapidPauseResume(t *testing.T) {
	const (
		fileSize   = 200 * 1024 // 200 KB
		cycles     = 10
		cycleDelay = 50 * time.Millisecond
	)

	ts := testutil.NewTestServer(fileSize, testutil.WithLatency(20*time.Millisecond))
	defer ts.Close()

	eng, _, bus, tmpDir := setupEngine(t)
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

	// Rapid pause/resume cycles
	var mu sync.Mutex
	completed := false

	// Monitor completion in a separate goroutine so pause/resume loop
	// can detect early completion.
	go func() {
		for evt := range ch {
			if e, ok := evt.(event.DownloadCompleted); ok && e.DownloadID == dl.ID {
				mu.Lock()
				completed = true
				mu.Unlock()
				return
			}
		}
	}()

	for i := 0; i < cycles; i++ {
		mu.Lock()
		done := completed
		mu.Unlock()
		if done {
			break
		}

		time.Sleep(cycleDelay)

		if err := eng.PauseDownload(ctx, dl.ID); err != nil {
			// May already be completed
			mu.Lock()
			done = completed
			mu.Unlock()
			if done {
				break
			}
			t.Logf("PauseDownload cycle %d: %v", i, err)
			continue
		}

		time.Sleep(cycleDelay)

		if err := eng.ResumeDownload(ctx, dl.ID); err != nil {
			mu.Lock()
			done = completed
			mu.Unlock()
			if done {
				break
			}
			t.Logf("ResumeDownload cycle %d: %v", i, err)
			continue
		}
	}

	// Wait for completion if not already done
	mu.Lock()
	done := completed
	mu.Unlock()
	if !done {
		timeout := time.After(120 * time.Second)
		for {
			mu.Lock()
			done = completed
			mu.Unlock()
			if done {
				break
			}
			select {
			case <-time.After(100 * time.Millisecond):
				// poll again
			case <-timeout:
				t.Fatal("timed out waiting for download to complete after pause/resume cycles")
			}
		}
	}

	// Verify file integrity
	filePath := filepath.Join(tmpDir, dl.Filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	expected := testutil.GenerateData(fileSize)
	if !bytes.Equal(data, expected) {
		t.Fatalf("file content mismatch: got %d bytes, want %d bytes", len(data), len(expected))
	}
}

func TestStress_LargeFileIntegrity(t *testing.T) {
	const fileSize = 50 * 1024 * 1024 // 50 MB

	ts := testutil.NewTestServer(fileSize)
	defer ts.Close()

	eng, _, bus, tmpDir := setupEngine(t)
	eng.client = ts.Client()

	ch, subID := bus.Subscribe()
	defer bus.Unsubscribe(subID)

	ctx := context.Background()
	dl, err := eng.AddDownload(ctx, model.AddRequest{
		URL:      ts.URL + "/largefile.bin",
		Segments: 32,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	if err := eng.StartDownload(ctx, dl.ID); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}

	// Wait for completion
	timeout := time.After(120 * time.Second)
	for {
		select {
		case evt := <-ch:
			if e, ok := evt.(event.DownloadCompleted); ok && e.DownloadID == dl.ID {
				goto verify
			}
			if e, ok := evt.(event.DownloadFailed); ok && e.DownloadID == dl.ID {
				t.Fatalf("download failed: %s", e.Error)
			}
		case <-timeout:
			t.Fatal("timed out waiting for 50MB download to complete")
		}
	}

verify:
	filePath := filepath.Join(tmpDir, dl.Filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	expected := testutil.GenerateData(fileSize)
	if len(data) != len(expected) {
		t.Fatalf("file size = %d, want %d", len(data), len(expected))
	}
	if !bytes.Equal(data, expected) {
		// Find first mismatch for debugging
		for i := range data {
			if data[i] != expected[i] {
				t.Fatalf("byte mismatch at offset %d: got 0x%02x, want 0x%02x", i, data[i], expected[i])
			}
		}
	}
}
