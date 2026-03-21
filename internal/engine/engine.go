package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/model"
	"golang.org/x/time/rate"
)

// Engine orchestrates the full download lifecycle.
type Engine struct {
	store     *db.Store
	cfgFn     func() config.Config // returns a consistent snapshot
	callbacks *Callbacks
	client    *http.Client
	limiter   *rate.Limiter

	mu     sync.Mutex
	active map[string]*activeDownload
}

type activeDownload struct {
	download   *model.Download
	segments   []model.Segment
	cancel     context.CancelFunc
	progressCh chan segmentReport
	progress   *progressAggregator
	file       *os.File
	wg         sync.WaitGroup
}

// New creates a new Engine.
func New(store *db.Store, cfgFn func() config.Config, callbacks *Callbacks) *Engine {
	cfg := cfgFn()
	e := &Engine{
		store:     store,
		cfgFn:     cfgFn,
		callbacks: callbacks,
		client:    newHTTPClient(),
		active:    make(map[string]*activeDownload),
	}
	e.setLimiter(cfg.GlobalSpeedLimit)
	return e
}

// NewWithClient creates a new Engine with a custom HTTP client (for testing).
func NewWithClient(store *db.Store, cfgFn func() config.Config, callbacks *Callbacks, client *http.Client) *Engine {
	cfg := cfgFn()
	e := &Engine{
		store:     store,
		cfgFn:     cfgFn,
		callbacks: callbacks,
		client:    client,
		active:    make(map[string]*activeDownload),
	}
	e.setLimiter(cfg.GlobalSpeedLimit)
	return e
}

// setLimiter creates a rate limiter for the given bytes-per-second value.
// A value of 0 or negative means unlimited (limiter is set to nil).
func (e *Engine) setLimiter(bytesPerSec int64) {
	if bytesPerSec <= 0 {
		e.limiter = nil
		return
	}
	burst := int(bytesPerSec)
	if burst < readBufSize {
		burst = readBufSize
	}
	e.limiter = rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

// SetSpeedLimit updates the global speed limit at runtime.
func (e *Engine) SetSpeedLimit(bytesPerSec int64) {
	if bytesPerSec <= 0 {
		e.limiter = nil
		return
	}
	burst := int(bytesPerSec)
	if burst < readBufSize {
		burst = readBufSize
	}
	if e.limiter != nil {
		e.limiter.SetLimit(rate.Limit(bytesPerSec))
		e.limiter.SetBurst(burst)
	} else {
		e.limiter = rate.NewLimiter(rate.Limit(bytesPerSec), burst)
	}
}

// AddDownload validates the request, probes the URL, creates the download
// and its segments in the DB, and returns the Download.
func (e *Engine) AddDownload(ctx context.Context, req model.AddRequest) (*model.Download, error) {
	if err := validateAddRequest(req); err != nil {
		return nil, err
	}

	// Snapshot config once for this operation
	cfg := e.cfgFn()

	// Probe the URL
	probeResult, err := Probe(ctx, e.client, req.URL, req.Headers)
	if err != nil {
		if !errors.Is(err, model.ErrProbeRejected) {
			return nil, fmt.Errorf("probe: %w", err)
		}
		slog.Warn("probe rejected, proceeding with minimal metadata",
			"url", req.URL, "error", err)
		probeResult = &model.ProbeResult{
			TotalSize:     -1,
			AcceptsRanges: false,
			FinalURL:      req.URL,
		}
	}

	// Detect filename
	filename := DetectFilename(req.Filename, probeResult.Filename, probeResult.FinalURL)
	slog.Debug("filename detected", "filename", filename, "url", req.URL)

	// Determine directory
	dir := req.Dir
	if dir == "" {
		dir = cfg.DownloadDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating download directory: %w", err)
	}

	// Check for duplicate filename (before deduplication)
	if !req.Force {
		existing, err := e.store.FindByFilename(ctx, filename, dir)
		if err != nil {
			return nil, fmt.Errorf("checking duplicates: %w", err)
		}
		if existing != nil {
			return nil, &model.DuplicateDownloadError{
				Existing:   existing,
				NewURL:     probeResult.FinalURL,
				NewHeaders: req.Headers,
			}
		}
	}

	// Deduplicate filename
	filename = DeduplicateFilename(dir, filename)

	// Determine segment count
	segCount := req.Segments
	if segCount <= 0 {
		segCount = cfg.DefaultSegments
	}

	// Fallback to single connection if no range support or unknown size
	if !probeResult.AcceptsRanges || probeResult.TotalSize <= 0 {
		segCount = 1
	}

	// Respect minimum segment size
	if probeResult.TotalSize > 0 && cfg.MinSegmentSize > 0 {
		maxSegs := probeResult.TotalSize / cfg.MinSegmentSize
		if maxSegs < 1 {
			maxSegs = 1
		}
		if int64(segCount) > maxSegs {
			segCount = int(maxSegs)
		}
	}

	// Get next queue order so new downloads appear at the bottom
	queueOrder, err := e.store.NextQueueOrder(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting queue order: %w", err)
	}

	// Generate ID
	id := model.NewDownloadID()

	// Extract referer from headers if not set explicitly
	refererURL := req.RefererURL
	if refererURL == "" && req.Headers != nil {
		if ref, ok := req.Headers["Referer"]; ok {
			refererURL = ref
		}
	}

	dl := &model.Download{
		ID:           id,
		URL:          probeResult.FinalURL,
		Filename:     filename,
		Dir:          dir,
		TotalSize:    probeResult.TotalSize,
		Downloaded:   0,
		Status:       model.StatusQueued,
		SegmentCount: segCount,
		SpeedLimit:   req.SpeedLimit,
		Headers:      req.Headers,
		RefererURL:   refererURL,
		TraceID:      req.TraceID,
		Checksum:     req.Checksum,
		ETag:         probeResult.ETag,
		LastModified: probeResult.LastModified,
		CreatedAt:    time.Now(),
		QueueOrder:   queueOrder,
	}

	// Compute segments and insert download + segments atomically
	segments := computeSegments(id, probeResult.TotalSize, segCount)
	if err := e.store.InsertDownloadWithSegments(ctx, dl, segments); err != nil {
		return nil, fmt.Errorf("inserting download: %w", err)
	}

	slog.Info("download added",
		"id", dl.ID, "trace", dl.TraceID,
		"url", dl.URL, "referer_url", dl.RefererURL,
		"filename", dl.Filename, "size", dl.TotalSize,
		"segments", dl.SegmentCount,
	)

	if e.callbacks != nil && e.callbacks.OnAdded != nil {
		e.callbacks.OnAdded(*dl)
	}

	return dl, nil
}

// StartDownload begins downloading the given download ID.
func (e *Engine) StartDownload(ctx context.Context, id string) error {
	dl, err := e.store.GetDownload(ctx, id)
	if err != nil {
		return err
	}

	segments, err := e.store.GetSegments(ctx, id)
	if err != nil {
		return fmt.Errorf("loading segments: %w", err)
	}

	return e.startDownload(ctx, dl, segments)
}

func (e *Engine) startDownload(ctx context.Context, dl *model.Download, segments []model.Segment) error {
	// Snapshot config once for this download
	cfg := e.cfgFn()
	e.mu.Lock()
	if _, exists := e.active[dl.ID]; exists {
		e.mu.Unlock()
		return model.ErrAlreadyActive
	}
	e.mu.Unlock()

	// Update status to active
	if err := e.store.UpdateDownloadStatus(context.Background(), dl.ID, model.StatusActive, ""); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	dl.Status = model.StatusActive

	// Open file
	filePath := filepath.Join(dl.Dir, dl.Filename)
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}

	if dl.TotalSize > 0 {
		if err := file.Truncate(dl.TotalSize); err != nil {
			file.Close()
			return fmt.Errorf("pre-allocating file: %w", err)
		}
	}

	dlCtx, cancel := context.WithCancel(ctx)

	reportCh := make(chan segmentReport, len(segments)*100)
	var onProgress func(string, model.ProgressUpdate)
	if e.callbacks != nil {
		onProgress = e.callbacks.OnProgress
	}
	agg := newProgressAggregator(dl.ID, dl.TraceID, dl.RefererURL, dl.TotalSize, segments, reportCh, onProgress, e.store)

	ad := &activeDownload{
		download:   dl,
		segments:   segments,
		cancel:     cancel,
		progressCh: reportCh,
		progress:   agg,
		file:       file,
	}

	e.mu.Lock()
	e.active[dl.ID] = ad
	e.mu.Unlock()

	// Launch segment workers
	for i := range segments {
		if segments[i].Done {
			reportCh <- segmentReport{Index: segments[i].Index, Done: true}
			continue
		}
		ad.wg.Add(1)
		go func(idx int) {
			defer ad.wg.Done()
			w := &segmentWorker{
				download: dl,
				segment:  &segments[idx],
				client:   e.client,
				reportCh: reportCh,
				file:     file,
				limiter:  e.limiter,
			}
			w.RunWithRetry(dlCtx, cfg.MaxRetries)
		}(i)
	}

	// Launch progress aggregator
	go func() {
		agg.Run(dlCtx)
	}()

	// Wait for completion in background
	go func() {
		ad.wg.Wait()
		// Small delay to let final reports drain
		time.Sleep(100 * time.Millisecond)
		close(reportCh)
		agg.Wait()

		file.Close()

		if agg.AllDone() {
			filePath := filepath.Join(dl.Dir, dl.Filename)
			if dl.Checksum != nil {
				_ = e.store.UpdateDownloadStatus(context.Background(), dl.ID, model.StatusVerifying, "")
				if err := verifyChecksum(filePath, dl.Checksum); err != nil {
					_ = e.store.UpdateDownloadStatus(context.Background(), dl.ID, model.StatusError, err.Error())
					slog.Info("download failed",
						"id", dl.ID, "trace", dl.TraceID,
						"referer_url", dl.RefererURL,
						"error", err.Error(),
					)
					if e.callbacks != nil && e.callbacks.OnFailed != nil {
						refreshed, _ := e.store.GetDownload(context.Background(), dl.ID)
						if refreshed != nil {
							e.callbacks.OnFailed(dl.ID, *refreshed, err)
						}
					}
				} else {
					_ = e.store.SetCompleted(context.Background(), dl.ID)
					slog.Info("download completed",
						"id", dl.ID, "trace", dl.TraceID,
						"referer_url", dl.RefererURL,
						"filename", dl.Filename, "size", dl.TotalSize,
						"duration", time.Since(dl.CreatedAt).Round(time.Second).String(),
					)
					if e.callbacks != nil && e.callbacks.OnCompleted != nil {
						refreshed, _ := e.store.GetDownload(context.Background(), dl.ID)
						if refreshed != nil {
							e.callbacks.OnCompleted(dl.ID, *refreshed)
						}
					}
				}
			} else {
				_ = e.store.SetCompleted(context.Background(), dl.ID)
				slog.Info("download completed",
					"id", dl.ID, "trace", dl.TraceID,
					"referer_url", dl.RefererURL,
					"filename", dl.Filename, "size", dl.TotalSize,
					"duration", time.Since(dl.CreatedAt).Round(time.Second).String(),
				)
				if e.callbacks != nil && e.callbacks.OnCompleted != nil {
					refreshed, _ := e.store.GetDownload(context.Background(), dl.ID)
					if refreshed != nil {
						e.callbacks.OnCompleted(dl.ID, *refreshed)
					}
				}
			}
		} else if agg.Err() != nil {
			aggErr := agg.Err()
			_ = e.store.UpdateDownloadStatus(context.Background(), dl.ID, model.StatusError, aggErr.Error())
			slog.Info("download failed",
				"id", dl.ID, "trace", dl.TraceID,
				"referer_url", dl.RefererURL,
				"error", aggErr.Error(),
			)
			if e.callbacks != nil && e.callbacks.OnFailed != nil {
				refreshed, _ := e.store.GetDownload(context.Background(), dl.ID)
				if refreshed != nil {
					e.callbacks.OnFailed(dl.ID, *refreshed, aggErr)
				}
			}
		}

		e.mu.Lock()
		delete(e.active, dl.ID)
		e.mu.Unlock()
	}()

	return nil
}

// RequeueDownload stops an active download and sets its status to queued.
// Unlike PauseDownload, this is for automatic queue management (e.g. when
// max_concurrent is reduced) rather than explicit user action.
func (e *Engine) RequeueDownload(ctx context.Context, id string) error {
	e.mu.Lock()
	ad, exists := e.active[id]
	e.mu.Unlock()

	if !exists {
		if err := e.store.UpdateDownloadStatus(ctx, id, model.StatusQueued, ""); err != nil {
			return err
		}
		slog.Info("download requeued", "id", id)
		return nil
	}

	ad.cancel()
	ad.wg.Wait()
	ad.progress.persistProgress()

	if err := e.store.UpdateDownloadStatus(ctx, id, model.StatusQueued, ""); err != nil {
		return err
	}

	ad.file.Close()

	e.mu.Lock()
	delete(e.active, id)
	e.mu.Unlock()

	slog.Info("download requeued", "id", id, "trace", ad.download.TraceID, "referer_url", ad.download.RefererURL)

	return nil
}

// PauseDownload cancels an active download and persists its state.
func (e *Engine) PauseDownload(ctx context.Context, id string) error {
	e.mu.Lock()
	ad, exists := e.active[id]
	e.mu.Unlock()

	if !exists {
		// Not actively running — just update DB status
		dl, err := e.store.GetDownload(ctx, id)
		if err != nil {
			return err
		}
		if dl.Status == model.StatusPaused {
			return model.ErrAlreadyPaused
		}
		if dl.Status == model.StatusCompleted {
			return model.ErrAlreadyCompleted
		}
		if err := e.store.UpdateDownloadStatus(ctx, id, model.StatusPaused, ""); err != nil {
			return err
		}
		slog.Info("download paused", "id", id, "trace", dl.TraceID, "referer_url", dl.RefererURL)
		if e.callbacks != nil && e.callbacks.OnPaused != nil {
			e.callbacks.OnPaused(id)
		}
		return nil
	}

	// Cancel the context to stop all workers
	ad.cancel()
	ad.wg.Wait()

	// Persist progress
	ad.progress.persistProgress()

	if err := e.store.UpdateDownloadStatus(ctx, id, model.StatusPaused, ""); err != nil {
		return err
	}

	ad.file.Close()

	e.mu.Lock()
	delete(e.active, id)
	e.mu.Unlock()

	slog.Info("download paused", "id", id, "trace", ad.download.TraceID, "referer_url", ad.download.RefererURL)

	if e.callbacks != nil && e.callbacks.OnPaused != nil {
		e.callbacks.OnPaused(id)
	}

	return nil
}

// ResumeDownload resumes a paused or errored download.
func (e *Engine) ResumeDownload(ctx context.Context, id string) error {
	dl, err := e.store.GetDownload(ctx, id)
	if err != nil {
		return err
	}

	if dl.Status == model.StatusActive {
		return model.ErrAlreadyActive
	}
	if dl.Status == model.StatusCompleted {
		return model.ErrAlreadyCompleted
	}

	segments, err := e.store.GetSegments(ctx, id)
	if err != nil {
		return fmt.Errorf("loading segments: %w", err)
	}

	if e.callbacks != nil && e.callbacks.OnResumed != nil {
		e.callbacks.OnResumed(id)
	}

	return e.startDownload(ctx, dl, segments)
}

// CancelDownload stops and removes a download.
func (e *Engine) CancelDownload(ctx context.Context, id string, deleteFile bool) error {
	// Pause first if active
	e.mu.Lock()
	ad, exists := e.active[id]
	e.mu.Unlock()

	if exists {
		ad.cancel()
		ad.wg.Wait()
		ad.file.Close()
		e.mu.Lock()
		delete(e.active, id)
		e.mu.Unlock()
	}

	dl, err := e.store.GetDownload(ctx, id)
	if err != nil {
		return err
	}

	if deleteFile {
		filePath := filepath.Join(dl.Dir, dl.Filename)
		_ = os.Remove(filePath)
	}

	if err := e.store.DeleteDownload(ctx, id); err != nil {
		return err
	}

	slog.Info("download deleted", "id", id, "trace", dl.TraceID, "referer_url", dl.RefererURL)

	if e.callbacks != nil && e.callbacks.OnRemoved != nil {
		e.callbacks.OnRemoved(id)
	}
	return nil
}

// RetryDownload retries a failed download.
func (e *Engine) RetryDownload(ctx context.Context, id string) error {
	return e.ResumeDownload(ctx, id)
}

// GetDownload returns a download and its segments.
func (e *Engine) GetDownload(ctx context.Context, id string) (*model.Download, []model.Segment, error) {
	dl, err := e.store.GetDownload(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	segments, err := e.store.GetSegments(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return dl, segments, nil
}

// ListDownloads returns downloads matching the filter.
func (e *Engine) ListDownloads(ctx context.Context, filter model.ListFilter) ([]model.Download, error) {
	return e.store.ListDownloads(ctx, filter.Status, filter.Limit, filter.Offset)
}

// Start recovers stale downloads on startup (crash recovery).
// Active downloads are re-queued. Downloads interrupted during checksum
// verification are also re-queued so they restart and re-verify.
// The queue manager will start them according to the concurrency limit.
func (e *Engine) Start(ctx context.Context) error {
	var requeued int
	for _, status := range []model.Status{model.StatusActive, model.StatusVerifying} {
		downloads, err := e.store.ListDownloads(ctx, string(status), 0, 0)
		if err != nil {
			return fmt.Errorf("loading %s downloads: %w", status, err)
		}
		for i := range downloads {
			_ = e.store.UpdateDownloadStatus(ctx, downloads[i].ID, model.StatusQueued, "")
			requeued++
		}
	}
	if requeued > 0 {
		slog.Info("crash recovery", "requeued", requeued)
	}
	return nil
}

// Shutdown gracefully stops all active downloads.
func (e *Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	ids := make([]string, 0, len(e.active))
	for id := range e.active {
		ids = append(ids, id)
	}
	e.mu.Unlock()

	for _, id := range ids {
		e.mu.Lock()
		ad, exists := e.active[id]
		e.mu.Unlock()
		if !exists {
			continue
		}
		ad.cancel()
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		for _, id := range ids {
			e.mu.Lock()
			ad, exists := e.active[id]
			e.mu.Unlock()
			if !exists {
				continue
			}
			ad.wg.Wait()
			ad.progress.persistProgress()
			_ = e.store.UpdateDownloadStatus(context.Background(), id, model.StatusPaused, "")
			ad.file.Close()
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}

	e.mu.Lock()
	e.active = make(map[string]*activeDownload)
	e.mu.Unlock()

	return nil
}

// ProbeURL sends a HEAD request (with GET fallback) to discover metadata
// about the resource at rawURL. This method wraps the package-level Probe
// function, providing the engine's HTTP client.
func (e *Engine) ProbeURL(ctx context.Context, rawURL string, headers map[string]string) (*model.ProbeResult, error) {
	return Probe(ctx, e.client, rawURL, headers)
}

// UpdateChecksum updates the checksum for a download.
// Rejects updates on completed downloads. Active downloads are allowed
// so the checksum is verified on completion.
func (e *Engine) UpdateChecksum(ctx context.Context, id string, checksum *model.Checksum) error {
	dl, err := e.store.GetDownload(ctx, id)
	if err != nil {
		return err
	}
	if dl.Status == model.StatusCompleted {
		return model.ErrAlreadyCompleted
	}
	if err := e.store.UpdateDownloadChecksum(ctx, id, checksum); err != nil {
		return err
	}
	// Update in-memory state so active downloads verify on completion
	e.mu.Lock()
	if ad, exists := e.active[id]; exists {
		ad.download.Checksum = checksum
	}
	e.mu.Unlock()
	return nil
}

// SetRefreshStatus sets a download's status to "refresh".
// Only valid for error or paused downloads.
func (e *Engine) SetRefreshStatus(ctx context.Context, id string) error {
	dl, err := e.store.GetDownload(ctx, id)
	if err != nil {
		return err
	}
	if dl.Status != model.StatusError && dl.Status != model.StatusPaused {
		return fmt.Errorf("can only set refresh status on error or paused downloads")
	}
	return e.store.UpdateDownloadStatus(ctx, id, model.StatusRefresh, "")
}

// IsActive returns whether a download is currently running.
func (e *Engine) IsActive(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, exists := e.active[id]
	return exists
}

func validateAddRequest(req model.AddRequest) error {
	if req.URL == "" {
		return model.ErrInvalidURL
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return model.ErrInvalidURL
	}
	if !strings.Contains(u.Host, ".") && u.Host != "localhost" && !strings.HasPrefix(u.Host, "localhost:") && !strings.HasPrefix(u.Host, "127.") {
		return model.ErrInvalidURL
	}
	if req.Segments < 0 || req.Segments > 32 {
		return model.ErrInvalidSegments
	}
	return nil
}

func computeSegments(downloadID string, totalSize int64, count int) []model.Segment {
	if totalSize <= 0 {
		// Unknown size — single segment
		return []model.Segment{{
			DownloadID: downloadID,
			Index:      0,
			StartByte:  0,
			EndByte:    -1, // sentinel: read until EOF
		}}
	}

	segments := make([]model.Segment, count)
	segSize := totalSize / int64(count)
	remainder := totalSize % int64(count)

	var offset int64
	for i := 0; i < count; i++ {
		size := segSize
		if int64(i) < remainder {
			size++
		}
		segments[i] = model.Segment{
			DownloadID: downloadID,
			Index:      i,
			StartByte:  offset,
			EndByte:    offset + size - 1,
		}
		offset += size
	}

	return segments
}
