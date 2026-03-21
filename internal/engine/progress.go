package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/model"
)

type progressAggregator struct {
	downloadID string
	traceID    string
	refererURL string
	totalSize  int64
	segInfos   []segInfo // immutable start/end info per segment
	reportCh   <-chan segmentReport
	onProgress func(string, model.ProgressUpdate)
	store      *db.Store

	mu              sync.Mutex
	segDownloaded   []int64 // per-segment downloaded bytes (aggregator's own copy)
	segDone         []bool  // per-segment done flags
	speeds          []int64
	lastBytes       int64
	lastTime        time.Time
	done            chan struct{}
	err             error
	totalDownloaded int64
}

type segInfo struct {
	DownloadID string
	Index      int
	StartByte  int64
	EndByte    int64
}

const (
	progressEmitInterval = 500 * time.Millisecond
	dbPersistInterval    = 2 * time.Second
	speedWindowSize      = 5
)

func newProgressAggregator(downloadID, traceID, refererURL string, totalSize int64, segments []model.Segment, reportCh <-chan segmentReport, onProgress func(string, model.ProgressUpdate), store *db.Store) *progressAggregator {
	infos := make([]segInfo, len(segments))
	downloaded := make([]int64, len(segments))
	done := make([]bool, len(segments))
	var totalDl int64

	for i, seg := range segments {
		infos[i] = segInfo{
			DownloadID: seg.DownloadID,
			Index:      seg.Index,
			StartByte:  seg.StartByte,
			EndByte:    seg.EndByte,
		}
		downloaded[i] = seg.Downloaded
		done[i] = seg.Done
		totalDl += seg.Downloaded
	}

	return &progressAggregator{
		downloadID:      downloadID,
		traceID:         traceID,
		refererURL:      refererURL,
		totalSize:       totalSize,
		segInfos:        infos,
		reportCh:        reportCh,
		onProgress:      onProgress,
		store:           store,
		segDownloaded:   downloaded,
		segDone:         done,
		speeds:          make([]int64, 0, speedWindowSize),
		lastTime:        time.Now(),
		lastBytes:       totalDl,
		totalDownloaded: totalDl,
		done:            make(chan struct{}),
	}
}

// Run processes segment reports and emits progress events.
// It returns when all segments are done, a permanent error occurs, or ctx is cancelled.
func (p *progressAggregator) Run(ctx context.Context) {
	defer close(p.done)

	progressTicker := time.NewTicker(progressEmitInterval)
	defer progressTicker.Stop()

	persistTicker := time.NewTicker(dbPersistInterval)
	defer persistTicker.Stop()

	segmentsDone := 0
	for _, d := range p.segDone {
		if d {
			segmentsDone++
		}
	}
	totalSegments := len(p.segInfos)

	for {
		select {
		case <-ctx.Done():
			p.persistProgress()
			return

		case report, ok := <-p.reportCh:
			if !ok {
				return
			}

			p.mu.Lock()
			if report.BytesRead > 0 {
				p.segDownloaded[report.Index] += report.BytesRead
				p.totalDownloaded += report.BytesRead
			}
			if report.Done && !p.segDone[report.Index] {
				p.segDone[report.Index] = true
				segmentsDone++
			}
			if report.Err != nil {
				p.err = report.Err
			}
			p.mu.Unlock()

			if report.Err != nil {
				p.persistProgress()
				return
			}

			if segmentsDone >= totalSegments {
				p.mu.Lock()
				td := p.totalDownloaded
				p.mu.Unlock()
				p.emitProgress(td)
				p.persistProgress()
				return
			}

		case <-progressTicker.C:
			p.mu.Lock()
			td := p.totalDownloaded
			p.mu.Unlock()
			p.emitProgress(td)

		case <-persistTicker.C:
			p.persistProgress()
		}
	}
}

func (p *progressAggregator) emitProgress(totalDownloaded int64) {
	now := time.Now()
	elapsed := now.Sub(p.lastTime).Seconds()

	var speed int64
	if elapsed > 0 {
		speed = int64(float64(totalDownloaded-p.lastBytes) / elapsed)
		if speed < 0 {
			speed = 0
		}
	}
	p.lastBytes = totalDownloaded
	p.lastTime = now

	p.speeds = append(p.speeds, speed)
	if len(p.speeds) > speedWindowSize {
		p.speeds = p.speeds[1:]
	}
	avgSpeed := int64(0)
	for _, s := range p.speeds {
		avgSpeed += s
	}
	if len(p.speeds) > 0 {
		avgSpeed /= int64(len(p.speeds))
	}

	eta := -1
	if avgSpeed > 0 && p.totalSize > 0 {
		remaining := p.totalSize - totalDownloaded
		if remaining > 0 {
			eta = int(remaining / avgSpeed)
		} else {
			eta = 0
		}
	}

	status := "active"
	p.mu.Lock()
	allDone := true
	for _, d := range p.segDone {
		if !d {
			allDone = false
			break
		}
	}
	p.mu.Unlock()

	if allDone {
		status = "completed"
	}

	if p.onProgress != nil {
		p.onProgress(p.downloadID, model.ProgressUpdate{
			Downloaded: totalDownloaded,
			TotalSize:  p.totalSize,
			Speed:      avgSpeed,
			ETA:        eta,
			Status:     model.Status(status),
		})
	}
}

func (p *progressAggregator) persistProgress() {
	p.mu.Lock()
	segs := make([]model.Segment, len(p.segInfos))
	var totalDl int64
	for i, info := range p.segInfos {
		segs[i] = model.Segment{
			DownloadID: info.DownloadID,
			Index:      info.Index,
			StartByte:  info.StartByte,
			EndByte:    info.EndByte,
			Downloaded: p.segDownloaded[i],
			Done:       p.segDone[i],
		}
		totalDl += p.segDownloaded[i]
	}
	p.mu.Unlock()

	bgCtx := context.Background()
	_ = p.store.BatchUpdateSegments(bgCtx, segs)
	_ = p.store.UpdateDownloadProgress(bgCtx, p.downloadID, totalDl)

	slog.Debug("progress persist",
		"id", p.downloadID, "trace", p.traceID,
		"downloaded", totalDl, "total", p.totalSize,
	)
}

// AllDone returns true if all segments have completed.
func (p *progressAggregator) AllDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.segDone {
		if !d {
			return false
		}
	}
	return true
}

// Err returns the first permanent error encountered, or nil.
func (p *progressAggregator) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Wait blocks until the aggregator's Run method has returned.
func (p *progressAggregator) Wait() {
	<-p.done
}
