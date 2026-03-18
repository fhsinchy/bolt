package db

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/fhsinchy/bolt/internal/model"
)

func newTestDownload(id string) *model.Download {
	return &model.Download{
		ID:           id,
		URL:          "https://example.com/file.zip",
		Filename:     "file.zip",
		Dir:          "/tmp/downloads",
		TotalSize:    1024 * 1024,
		Downloaded:   0,
		Status:       model.StatusQueued,
		SegmentCount: 4,
		SpeedLimit:   0,
		Headers:      map[string]string{"Authorization": "Bearer token123"},
		RefererURL:   "https://example.com",
		Checksum:     &model.Checksum{Algorithm: "sha256", Value: "abc123"},
		ETag:         `"etag-value"`,
		LastModified: "Mon, 01 Jan 2024 00:00:00 GMT",
		Error:        "",
		QueueOrder:   1,
	}
}

func TestInsertAndGetDownload(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_test001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_test001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.ID != d.ID {
		t.Errorf("ID = %q, want %q", got.ID, d.ID)
	}
	if got.URL != d.URL {
		t.Errorf("URL = %q, want %q", got.URL, d.URL)
	}
	if got.Filename != d.Filename {
		t.Errorf("Filename = %q, want %q", got.Filename, d.Filename)
	}
	if got.Dir != d.Dir {
		t.Errorf("Dir = %q, want %q", got.Dir, d.Dir)
	}
	if got.TotalSize != d.TotalSize {
		t.Errorf("TotalSize = %d, want %d", got.TotalSize, d.TotalSize)
	}
	if got.Status != d.Status {
		t.Errorf("Status = %q, want %q", got.Status, d.Status)
	}
	if got.SegmentCount != d.SegmentCount {
		t.Errorf("SegmentCount = %d, want %d", got.SegmentCount, d.SegmentCount)
	}
	if got.ETag != d.ETag {
		t.Errorf("ETag = %q, want %q", got.ETag, d.ETag)
	}
	if got.RefererURL != d.RefererURL {
		t.Errorf("RefererURL = %q, want %q", got.RefererURL, d.RefererURL)
	}
	if got.QueueOrder != d.QueueOrder {
		t.Errorf("QueueOrder = %d, want %d", got.QueueOrder, d.QueueOrder)
	}
}

func TestGetDownload_Headers(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_hdr001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_hdr001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(got.Headers) != 1 {
		t.Fatalf("Headers length = %d, want 1", len(got.Headers))
	}
	if got.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("Headers[Authorization] = %q, want %q", got.Headers["Authorization"], "Bearer token123")
	}
}

func TestGetDownload_Checksum(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_chk001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_chk001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Checksum == nil {
		t.Fatal("Checksum is nil")
	}
	if got.Checksum.Algorithm != "sha256" {
		t.Errorf("Checksum.Algorithm = %q, want %q", got.Checksum.Algorithm, "sha256")
	}
	if got.Checksum.Value != "abc123" {
		t.Errorf("Checksum.Value = %q, want %q", got.Checksum.Value, "abc123")
	}
}

func TestGetDownload_NilChecksum(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_nilchk")
	d.Checksum = nil

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_nilchk")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Checksum != nil {
		t.Errorf("Checksum = %+v, want nil", got.Checksum)
	}
}

func TestGetDownload_EmptyHeaders(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_emptyhdr")
	d.Headers = nil

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_emptyhdr")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Headers == nil {
		t.Fatal("Headers is nil, want empty map")
	}
	if len(got.Headers) != 0 {
		t.Errorf("Headers length = %d, want 0", len(got.Headers))
	}
}

func TestGetDownload_NotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	_, err := store.GetDownload(ctx, "nonexistent")
	if err != model.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListDownloads_All(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	for i, id := range []string{"d_list001", "d_list002", "d_list003"} {
		d := newTestDownload(id)
		d.QueueOrder = i
		if err := store.InsertDownload(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	downloads, err := store.ListDownloads(ctx, "", 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(downloads) != 3 {
		t.Errorf("got %d downloads, want 3", len(downloads))
	}
}

func TestListDownloads_FilterByStatus(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	d1 := newTestDownload("d_flt001")
	d1.Status = model.StatusQueued
	d2 := newTestDownload("d_flt002")
	d2.Status = model.StatusActive

	for _, d := range []*model.Download{d1, d2} {
		if err := store.InsertDownload(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", d.ID, err)
		}
	}

	downloads, err := store.ListDownloads(ctx, string(model.StatusQueued), 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(downloads) != 1 {
		t.Fatalf("got %d downloads, want 1", len(downloads))
	}
	if downloads[0].ID != "d_flt001" {
		t.Errorf("ID = %q, want d_flt001", downloads[0].ID)
	}
}

func TestListDownloads_LimitOffset(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"d_pg001", "d_pg002", "d_pg003", "d_pg004", "d_pg005"} {
		d := newTestDownload(id)
		if err := store.InsertDownload(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	downloads, err := store.ListDownloads(ctx, "", 2, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(downloads) != 2 {
		t.Errorf("got %d downloads, want 2", len(downloads))
	}

	downloads, err = store.ListDownloads(ctx, "", 2, 2)
	if err != nil {
		t.Fatalf("list with offset: %v", err)
	}
	if len(downloads) != 2 {
		t.Errorf("got %d downloads with offset, want 2", len(downloads))
	}
}

func TestUpdateDownloadStatus(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_stat001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.UpdateDownloadStatus(ctx, "d_stat001", model.StatusActive, ""); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_stat001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.StatusActive {
		t.Errorf("Status = %q, want %q", got.Status, model.StatusActive)
	}
}

func TestUpdateDownloadStatus_WithError(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_staterr")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.UpdateDownloadStatus(ctx, "d_staterr", model.StatusError, "connection timeout"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_staterr")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.StatusError {
		t.Errorf("Status = %q, want %q", got.Status, model.StatusError)
	}
	if got.Error != "connection timeout" {
		t.Errorf("Error = %q, want %q", got.Error, "connection timeout")
	}
}

func TestUpdateDownloadStatus_NotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.UpdateDownloadStatus(ctx, "nonexistent", model.StatusActive, "")
	if err != model.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateDownloadURL(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_url001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	newHeaders := map[string]string{"X-Custom": "value"}
	if err := store.UpdateDownloadURL(ctx, "d_url001", "https://mirror.example.com/file.zip", newHeaders); err != nil {
		t.Fatalf("update url: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_url001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.URL != "https://mirror.example.com/file.zip" {
		t.Errorf("URL = %q, want %q", got.URL, "https://mirror.example.com/file.zip")
	}
	if got.Headers["X-Custom"] != "value" {
		t.Errorf("Headers[X-Custom] = %q, want %q", got.Headers["X-Custom"], "value")
	}
}

func TestUpdateDownloadProgress(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_prog001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.UpdateDownloadProgress(ctx, "d_prog001", 512*1024); err != nil {
		t.Fatalf("update progress: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_prog001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Downloaded != 512*1024 {
		t.Errorf("Downloaded = %d, want %d", got.Downloaded, 512*1024)
	}
}

func TestSetCompleted(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_comp001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.SetCompleted(ctx, "d_comp001"); err != nil {
		t.Fatalf("set completed: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_comp001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.StatusCompleted {
		t.Errorf("Status = %q, want %q", got.Status, model.StatusCompleted)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt is nil, want non-nil")
	}
}

func TestDeleteDownload(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_del001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.DeleteDownload(ctx, "d_del001"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := store.GetDownload(ctx, "d_del001")
	if err != model.ErrNotFound {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteDownload_NotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.DeleteDownload(ctx, "nonexistent")
	if err != model.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteDownload_CascadeSegments(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_casc001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert download: %v", err)
	}

	segments := []model.Segment{
		{DownloadID: "d_casc001", Index: 0, StartByte: 0, EndByte: 511},
		{DownloadID: "d_casc001", Index: 1, StartByte: 512, EndByte: 1023},
	}
	if err := store.InsertSegments(ctx, segments); err != nil {
		t.Fatalf("insert segments: %v", err)
	}

	// Verify segments exist.
	segs, err := store.GetSegments(ctx, "d_casc001")
	if err != nil {
		t.Fatalf("get segments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}

	// Delete the download.
	if err := store.DeleteDownload(ctx, "d_casc001"); err != nil {
		t.Fatalf("delete download: %v", err)
	}

	// Segments should be cascade-deleted.
	segs, err = store.GetSegments(ctx, "d_casc001")
	if err != nil {
		t.Fatalf("get segments after cascade: %v", err)
	}
	if len(segs) != 0 {
		t.Errorf("segments after cascade = %d, want 0", len(segs))
	}
}

func TestGetNextQueued(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// No queued downloads.
	got, err := store.GetNextQueued(ctx)
	if err != nil {
		t.Fatalf("get next queued (empty): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	d1 := newTestDownload("d_q001")
	d1.QueueOrder = 10
	d2 := newTestDownload("d_q002")
	d2.QueueOrder = 5
	d3 := newTestDownload("d_q003")
	d3.Status = model.StatusActive
	d3.QueueOrder = 1

	for _, d := range []*model.Download{d1, d2, d3} {
		if err := store.InsertDownload(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", d.ID, err)
		}
	}

	got, err = store.GetNextQueued(ctx)
	if err != nil {
		t.Fatalf("get next queued: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil, got nil")
	}
	// d_q002 has the lowest queue_order among queued downloads.
	if got.ID != "d_q002" {
		t.Errorf("ID = %q, want d_q002", got.ID)
	}
}

func TestCountByStatus(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	d1 := newTestDownload("d_cnt001")
	d1.Status = model.StatusQueued
	d2 := newTestDownload("d_cnt002")
	d2.Status = model.StatusQueued
	d3 := newTestDownload("d_cnt003")
	d3.Status = model.StatusActive

	for _, d := range []*model.Download{d1, d2, d3} {
		if err := store.InsertDownload(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", d.ID, err)
		}
	}

	count, err := store.CountByStatus(ctx, model.StatusQueued)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	count, err = store.CountByStatus(ctx, model.StatusActive)
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if count != 1 {
		t.Errorf("active count = %d, want 1", count)
	}

	count, err = store.CountByStatus(ctx, model.StatusCompleted)
	if err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if count != 0 {
		t.Errorf("completed count = %d, want 0", count)
	}
}

func TestGetDownload_CreatedAtParsed(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_time001")

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_time001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want non-zero (set by DEFAULT)")
	}
}

func TestConcurrentWrites(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Generate IDs on the main goroutine because model.NewDownloadID()
	// uses a shared MonotonicEntropy that is not goroutine-safe.
	ids := make([]string, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		ids[i] = model.NewDownloadID()
	}

	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(n int) {
			defer wg.Done()
			d := newTestDownload(ids[n])
			d.Filename = fmt.Sprintf("concurrent_%d.zip", n)
			if err := store.InsertDownload(ctx, d); err != nil {
				errs <- fmt.Errorf("goroutine %d: insert %s: %w", n, ids[n], err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent insert failed: %v", err)
	}

	downloads, err := store.ListDownloads(ctx, "", 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(downloads) != numGoroutines {
		t.Errorf("got %d downloads, want %d", len(downloads), numGoroutines)
	}
}

func TestUpdateDownloadChecksum(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_updchk001")
	d.Checksum = nil

	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Set checksum
	cs := &model.Checksum{Algorithm: "sha256", Value: "deadbeef"}
	if err := store.UpdateDownloadChecksum(ctx, "d_updchk001", cs); err != nil {
		t.Fatalf("update checksum: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_updchk001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Checksum == nil {
		t.Fatal("Checksum is nil after update")
	}
	if got.Checksum.Algorithm != "sha256" {
		t.Errorf("Algorithm = %q, want sha256", got.Checksum.Algorithm)
	}
	if got.Checksum.Value != "deadbeef" {
		t.Errorf("Value = %q, want deadbeef", got.Checksum.Value)
	}

	// Clear checksum
	if err := store.UpdateDownloadChecksum(ctx, "d_updchk001", nil); err != nil {
		t.Fatalf("clear checksum: %v", err)
	}
	got, err = store.GetDownload(ctx, "d_updchk001")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got.Checksum != nil {
		t.Errorf("Checksum = %+v after clear, want nil", got.Checksum)
	}
}

func TestUpdateDownloadChecksum_NotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.UpdateDownloadChecksum(ctx, "nonexistent", &model.Checksum{Algorithm: "sha256", Value: "abc"})
	if err != model.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestInsertDownloadWithSegments(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_atomic001")

	segments := []model.Segment{
		{DownloadID: "d_atomic001", Index: 0, StartByte: 0, EndByte: 511},
		{DownloadID: "d_atomic001", Index: 1, StartByte: 512, EndByte: 1023},
	}

	if err := store.InsertDownloadWithSegments(ctx, d, segments); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetDownload(ctx, "d_atomic001")
	if err != nil {
		t.Fatalf("get download: %v", err)
	}
	if got.ID != "d_atomic001" {
		t.Errorf("ID = %q, want d_atomic001", got.ID)
	}

	segs, err := store.GetSegments(ctx, "d_atomic001")
	if err != nil {
		t.Fatalf("get segments: %v", err)
	}
	if len(segs) != 2 {
		t.Errorf("segments = %d, want 2", len(segs))
	}
}

func TestInsertDownloadWithSegments_InvalidSegmentRollback(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	d := newTestDownload("d_atomic002")

	// Create a segment referencing a different download ID, which won't cause
	// an FK error since the download is in the same tx. Instead, we test
	// rollback by inserting the download first (so a duplicate insert fails).
	if err := store.InsertDownload(ctx, d); err != nil {
		t.Fatalf("pre-insert: %v", err)
	}

	// Attempting InsertDownloadWithSegments with the same ID should fail
	// (duplicate PK), and segments should not be affected.
	d2 := newTestDownload("d_atomic002")
	segs := []model.Segment{
		{DownloadID: "d_atomic002", Index: 0, StartByte: 0, EndByte: 511},
	}
	err := store.InsertDownloadWithSegments(ctx, d2, segs)
	if err == nil {
		t.Fatal("expected error on duplicate insert, got nil")
	}

	// Segments should not have been inserted (tx rolled back).
	gotSegs, err := store.GetSegments(ctx, "d_atomic002")
	if err != nil {
		t.Fatalf("get segments: %v", err)
	}
	if len(gotSegs) != 0 {
		t.Errorf("segments after rollback = %d, want 0", len(gotSegs))
	}
}

func TestCountAll(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	count, err := store.CountAll(ctx)
	if err != nil {
		t.Fatalf("count all (empty): %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	for _, id := range []string{"d_ca001", "d_ca002", "d_ca003"} {
		d := newTestDownload(id)
		if err := store.InsertDownload(ctx, d); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	count, err = store.CountAll(ctx)
	if err != nil {
		t.Fatalf("count all: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestUnicodeFilenames(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		filename string
	}{
		{"Japanese", "ファイル.zip"},
		{"Chinese", "档案.tar.gz"},
		{"Russian", "файл_данных.bin"},
		{"Greek", "αρχείο.pdf"},
		{"Emoji", "emoji_🎉_test.bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := model.NewDownloadID()
			d := newTestDownload(id)
			d.Filename = tt.filename

			if err := store.InsertDownload(ctx, d); err != nil {
				t.Fatalf("insert: %v", err)
			}

			got, err := store.GetDownload(ctx, id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			if got.Filename != tt.filename {
				t.Errorf("Filename = %q, want %q", got.Filename, tt.filename)
			}
		})
	}
}
