package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/notify"
	"github.com/fhsinchy/bolt/internal/queue"
)

// Service is the coordination layer between the engine, queue, and clients.
type Service struct {
	engine  *engine.Engine
	queue   *queue.Manager
	store   *db.Store
	cfg     *config.Config
	cfgPath string
	clients *ClientHub
}

// New creates a new Service.
func New(eng *engine.Engine, q *queue.Manager, store *db.Store, cfg *config.Config, cfgPath string) *Service {
	return &Service{
		engine:  eng,
		queue:   q,
		store:   store,
		cfg:     cfg,
		cfgPath: cfgPath,
		clients: NewClientHub(),
	}
}

// SetEngine sets the engine (for deferred wiring during daemon startup).
func (s *Service) SetEngine(eng *engine.Engine) {
	s.engine = eng
}

// SetQueue sets the queue manager (for deferred wiring during daemon startup).
func (s *Service) SetQueue(q *queue.Manager) {
	s.queue = q
}

// --- Download Operations ---

func (s *Service) AddDownload(ctx context.Context, req model.AddRequest) (*model.Download, error) {
	dl, err := s.engine.AddDownload(ctx, req)
	if err != nil {
		return nil, err
	}
	s.queue.Enqueue(dl.ID)
	return dl, nil
}

func (s *Service) PauseDownload(ctx context.Context, id string) error {
	return s.engine.PauseDownload(ctx, id)
}

func (s *Service) ResumeDownload(ctx context.Context, id string) error {
	return s.queue.EnqueueResume(ctx, id)
}

func (s *Service) RetryDownload(ctx context.Context, id string) error {
	return s.queue.EnqueueResume(ctx, id)
}

func (s *Service) CancelDownload(ctx context.Context, id string, deleteFile bool) error {
	return s.engine.CancelDownload(ctx, id, deleteFile)
}

func (s *Service) RemoveDownload(ctx context.Context, id string, deleteFile bool) error {
	return s.engine.CancelDownload(ctx, id, deleteFile)
}

func (s *Service) RefreshURL(ctx context.Context, id string, newURL string, headers map[string]string) error {
	return s.engine.RefreshURL(ctx, id, newURL, headers)
}

func (s *Service) SetRefreshStatus(ctx context.Context, id string) error {
	return s.engine.SetRefreshStatus(ctx, id)
}

func (s *Service) UpdateChecksum(ctx context.Context, id string, checksum *model.Checksum) error {
	return s.engine.UpdateChecksum(ctx, id, checksum)
}

// --- Query Operations ---

func (s *Service) GetDownload(ctx context.Context, id string) (*model.Download, []model.Segment, error) {
	return s.engine.GetDownload(ctx, id)
}

func (s *Service) ListDownloads(ctx context.Context, filter model.ListFilter) ([]model.Download, error) {
	return s.engine.ListDownloads(ctx, filter)
}

func (s *Service) GetStats(ctx context.Context) map[string]any {
	active, _ := s.store.CountByStatus(ctx, model.StatusActive)
	queued, _ := s.store.CountByStatus(ctx, model.StatusQueued)
	completed, _ := s.store.CountByStatus(ctx, model.StatusCompleted)
	return map[string]any{
		"active_count":    active,
		"queued_count":    queued,
		"completed_count": completed,
		"version":         "0.4.0-dev",
	}
}

// --- Probe ---

func (s *Service) ProbeURL(ctx context.Context, rawURL string, headers map[string]string) (*model.ProbeResult, error) {
	return s.engine.ProbeURL(ctx, rawURL, headers)
}

// --- Config ---

func (s *Service) GetConfig() *config.Config {
	return s.cfg
}

func (s *Service) UpdateConfig(ctx context.Context, apply func(cfg *config.Config)) error {
	apply(s.cfg)
	if err := s.cfg.Validate(); err != nil {
		return err
	}
	return s.cfg.Save(s.cfgPath)
}

func (s *Service) SetMaxConcurrent(max int) {
	s.queue.SetMaxConcurrent(max)
}

func (s *Service) SetSpeedLimit(bytesPerSec int64) {
	s.engine.SetSpeedLimit(bytesPerSec)
}

// --- Queue Operations ---

func (s *Service) PauseAll(ctx context.Context) error {
	downloads, err := s.store.ListDownloads(ctx, string(model.StatusActive), 0, 0)
	if err != nil {
		return err
	}
	for _, dl := range downloads {
		_ = s.engine.PauseDownload(ctx, dl.ID)
	}
	// Also pause queued downloads
	queued, err := s.store.ListDownloads(ctx, string(model.StatusQueued), 0, 0)
	if err != nil {
		return err
	}
	for _, dl := range queued {
		_ = s.store.UpdateDownloadStatus(ctx, dl.ID, model.StatusPaused, "")
	}
	return nil
}

func (s *Service) ResumeAll(ctx context.Context) error {
	return s.queue.EnqueueResumeAll(ctx)
}

func (s *Service) ClearCompleted(ctx context.Context) error {
	downloads, err := s.store.ListDownloads(ctx, string(model.StatusCompleted), 0, 0)
	if err != nil {
		return err
	}
	for _, dl := range downloads {
		_ = s.store.DeleteDownload(ctx, dl.ID)
	}
	return nil
}

func (s *Service) ReorderDownloads(ctx context.Context, orderedIDs []string) error {
	return s.store.ReorderDownloads(ctx, orderedIDs)
}

// --- Client Hub ---

func (s *Service) RegisterClient() (int, <-chan []byte) {
	return s.clients.Register()
}

func (s *Service) UnregisterClient(id int) {
	s.clients.Unregister(id)
}

// --- Callbacks ---

// EngineCallbacks returns the engine.Callbacks that wire completion events
// to the queue manager and broadcast to WebSocket clients.
func (s *Service) EngineCallbacks() *engine.Callbacks {
	return &engine.Callbacks{
		OnProgress: func(id string, update model.ProgressUpdate) {
			s.broadcastEvent("progress", map[string]any{
				"download_id": id,
				"downloaded":  update.Downloaded,
				"total_size":  update.TotalSize,
				"speed":       update.Speed,
				"eta":         update.ETA,
				"status":      update.Status,
			})
		},
		OnCompleted: func(id string, dl model.Download) {
			s.queue.OnDownloadComplete(id)
			s.broadcastEvent("download_completed", map[string]any{
				"download_id": id,
				"filename":    dl.Filename,
			})
			if s.cfg.Notifications {
				_ = notify.Send("Download Complete", dl.Filename)
			}
		},
		OnFailed: func(id string, dl model.Download, err error) {
			s.queue.OnDownloadComplete(id)
			s.broadcastEvent("download_failed", map[string]any{
				"download_id": id,
				"error":       err.Error(),
			})
			if s.cfg.Notifications {
				_ = notify.Send("Download Failed", dl.Filename+": "+err.Error())
			}
		},
		OnPaused: func(id string) {
			s.queue.OnDownloadComplete(id)
			s.broadcastEvent("download_paused", map[string]any{
				"download_id": id,
			})
		},
		OnResumed: func(id string) {
			s.broadcastEvent("download_resumed", map[string]any{
				"download_id": id,
			})
		},
		OnAdded: func(dl model.Download) {
			s.broadcastEvent("download_added", map[string]any{
				"download_id": dl.ID,
				"filename":    dl.Filename,
				"total_size":  dl.TotalSize,
			})
		},
		OnRemoved: func(id string) {
			s.broadcastEvent("download_removed", map[string]any{
				"download_id": id,
			})
		},
	}
}

// OnResumedCallback returns a callback for the queue manager's onResumed parameter.
func (s *Service) OnResumedCallback() func(id string) {
	return func(id string) {
		s.broadcastEvent("download_resumed", map[string]any{
			"download_id": id,
		})
	}
}

func (s *Service) broadcastEvent(eventType string, data map[string]any) {
	data["type"] = eventType
	msg, err := json.Marshal(data)
	if err != nil {
		slog.Error("marshal broadcast event", "error", err)
		return
	}
	s.clients.Broadcast(msg)
}
