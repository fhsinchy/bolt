package queue

import (
	"context"
	"sync"

	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/model"
)

// StartFunc is a callback invoked when the queue decides to start a download.
type StartFunc func(ctx context.Context, id string) error

// PauseFn is a callback invoked when the queue needs to pause a download.
type PauseFn func(ctx context.Context, id string) error

// Manager implements a FIFO queue with configurable max concurrent downloads.
type Manager struct {
	store         *db.Store
	maxConcurrent int
	startFn       StartFunc
	pauseFn       PauseFn
	onResumed     func(id string)

	mu     sync.Mutex
	notify chan struct{}
}

// New creates a new queue Manager.
func New(store *db.Store, maxConcurrent int, startFn StartFunc, pauseFn PauseFn, onResumed func(id string)) *Manager {
	return &Manager{
		store:         store,
		maxConcurrent: maxConcurrent,
		startFn:       startFn,
		pauseFn:       pauseFn,
		onResumed:     onResumed,
		notify:        make(chan struct{}, 1),
	}
}

// Enqueue adds a download to the queue and signals evaluation.
func (m *Manager) Enqueue(id string) {
	m.signal()
}

// OnDownloadComplete signals the queue to evaluate whether the next
// queued download can start.
func (m *Manager) OnDownloadComplete(id string) {
	m.signal()
}

// Run is the main loop that evaluates the queue when signaled.
func (m *Manager) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.notify:
			m.evaluate(ctx)
		}
	}
}

func (m *Manager) evaluate(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for {
		activeCount, err := m.store.CountByStatus(ctx, model.StatusActive)
		if err != nil || activeCount >= m.maxConcurrent {
			return
		}

		dl, err := m.store.GetNextQueued(ctx)
		if err != nil || dl == nil {
			return
		}

		if err := m.startFn(ctx, dl.ID); err != nil {
			// If start fails, mark as error and try the next one
			_ = m.store.UpdateDownloadStatus(ctx, dl.ID, model.StatusError, err.Error())
			continue
		}
	}
}

// EnqueueResume sets a paused or errored download to queued status so the
// queue's evaluate loop can start it when a slot is available.
func (m *Manager) EnqueueResume(ctx context.Context, id string) error {
	dl, err := m.store.GetDownload(ctx, id)
	if err != nil {
		return err
	}
	if dl.Status == model.StatusActive {
		return model.ErrAlreadyActive
	}
	if dl.Status == model.StatusCompleted {
		return model.ErrAlreadyCompleted
	}
	if err := m.store.UpdateDownloadStatus(ctx, id, model.StatusQueued, ""); err != nil {
		return err
	}
	if m.onResumed != nil {
		m.onResumed(id)
	}
	m.signal()
	return nil
}

// EnqueueResumeAll sets all paused downloads to queued status.
func (m *Manager) EnqueueResumeAll(ctx context.Context) error {
	downloads, err := m.store.ListDownloads(ctx, string(model.StatusPaused), 0, 0)
	if err != nil {
		return err
	}
	for _, dl := range downloads {
		if err := m.store.UpdateDownloadStatus(ctx, dl.ID, model.StatusQueued, ""); err != nil {
			continue
		}
		if m.onResumed != nil {
			m.onResumed(dl.ID)
		}
	}
	m.signal()
	return nil
}

// SetMaxConcurrent updates the maximum number of concurrent downloads,
// pauses excess active downloads if needed, and re-evaluates the queue.
func (m *Manager) SetMaxConcurrent(max int) {
	m.mu.Lock()
	m.maxConcurrent = max
	m.mu.Unlock()
	m.pauseExcess(context.Background())
	m.signal()
}

// pauseExcess pauses active downloads that exceed the max concurrent limit.
// It pauses downloads with the highest queue_order first (newest).
func (m *Manager) pauseExcess(ctx context.Context) {
	active, err := m.store.ListDownloads(ctx, string(model.StatusActive), 0, 0)
	if err != nil {
		return
	}

	m.mu.Lock()
	excess := len(active) - m.maxConcurrent
	m.mu.Unlock()

	if excess <= 0 {
		return
	}

	// ListDownloads returns sorted by queue_order ASC.
	// Pause from the end (highest queue_order = newest).
	for i := len(active) - 1; i >= 0 && excess > 0; i-- {
		_ = m.pauseFn(ctx, active[i].ID)
		excess--
	}
}

// ActiveCount returns the current number of active downloads from the database.
func (m *Manager) ActiveCount() int {
	count, _ := m.store.CountByStatus(context.Background(), model.StatusActive)
	return count
}

func (m *Manager) signal() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}
