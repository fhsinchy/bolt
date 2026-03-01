package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"golang.org/x/time/rate"
)

// Engine orchestrates the full download lifecycle.
type Engine struct {
	store   *db.Store
	cfg     *config.Config
	bus     *event.Bus
	client  *http.Client
	limiter *rate.Limiter

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
func New(store *db.Store, cfg *config.Config, bus *event.Bus) *Engine {
	e := &Engine{
		store:  store,
		cfg:    cfg,
		bus:    bus,
		client: newHTTPClient(cfg),
		active: make(map[string]*activeDownload),
	}
	e.setLimiter(cfg.GlobalSpeedLimit)
	return e
}

// NewWithClient creates a new Engine with a custom HTTP client (for testing).
func NewWithClient(store *db.Store, cfg *config.Config, bus *event.Bus, client *http.Client) *Engine {
	e := &Engine{
		store:  store,
		cfg:    cfg,
		bus:    bus,
		client: client,
		active: make(map[string]*activeDownload),
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

	// Probe the URL
	probeResult, err := Probe(ctx, e.client, req.URL, req.Headers)
	if err != nil {
		return nil, fmt.Errorf("probe: %w", err)
	}

	// Detect filename
	filename := DetectFilename(req.Filename, probeResult.Filename, probeResult.FinalURL)

	// Determine directory
	dir := req.Dir
	if dir == "" {
		dir = e.cfg.DownloadDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating download directory: %w", err)
	}

	// Deduplicate filename
	filename = DeduplicateFilename(dir, filename)

	// Determine segment count
	segCount := req.Segments
	if segCount <= 0 {
		segCount = e.cfg.DefaultSegments
	}

	// Fallback to single connection if no range support or unknown size
	if !probeResult.AcceptsRanges || probeResult.TotalSize <= 0 {
		segCount = 1
	}

	// Respect minimum segment size
	if probeResult.TotalSize > 0 && e.cfg.MinSegmentSize > 0 {
		maxSegs := probeResult.TotalSize / e.cfg.MinSegmentSize
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
		RefererURL:   req.RefererURL,
		Checksum:     req.Checksum,
		ETag:         probeResult.ETag,
		LastModified: probeResult.LastModified,
		CreatedAt:    time.Now(),
		QueueOrder:   queueOrder,
	}

	if err := e.store.InsertDownload(ctx, dl); err != nil {
		return nil, fmt.Errorf("inserting download: %w", err)
	}

	// Compute segments
	segments := computeSegments(id, probeResult.TotalSize, segCount)
	if err := e.store.InsertSegments(ctx, segments); err != nil {
		return nil, fmt.Errorf("inserting segments: %w", err)
	}

	e.bus.Publish(event.DownloadAdded{
		DownloadID: id,
		Filename:   filename,
		TotalSize:  probeResult.TotalSize,
	})

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
	agg := newProgressAggregator(dl.ID, dl.TotalSize, segments, reportCh, e.bus, e.store)

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
			w.RunWithRetry(dlCtx, e.cfg.MaxRetries)
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
			_ = e.store.SetCompleted(context.Background(), dl.ID)
			e.bus.Publish(event.DownloadCompleted{
				DownloadID: dl.ID,
				Filename:   dl.Filename,
			})
		} else if agg.Err() != nil {
			_ = e.store.UpdateDownloadStatus(context.Background(), dl.ID, model.StatusError, agg.Err().Error())
			e.bus.Publish(event.DownloadFailed{
				DownloadID: dl.ID,
				Error:      agg.Err().Error(),
			})
		}

		e.mu.Lock()
		delete(e.active, dl.ID)
		e.mu.Unlock()
	}()

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
		e.bus.Publish(event.DownloadPaused{DownloadID: id})
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

	e.bus.Publish(event.DownloadPaused{DownloadID: id})

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

	e.bus.Publish(event.DownloadResumed{DownloadID: id})

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

	e.bus.Publish(event.DownloadRemoved{DownloadID: id})
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

// Start resumes all previously-active downloads from the DB (crash recovery).
func (e *Engine) Start(ctx context.Context) error {
	downloads, err := e.store.ListDownloads(ctx, string(model.StatusActive), 0, 0)
	if err != nil {
		return fmt.Errorf("loading active downloads: %w", err)
	}

	for i := range downloads {
		segments, err := e.store.GetSegments(ctx, downloads[i].ID)
		if err != nil {
			continue
		}
		_ = e.startDownload(ctx, &downloads[i], segments)
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
			EndByte:    0, // will be updated as we download
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
