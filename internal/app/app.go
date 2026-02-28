package app

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
	"github.com/fhsinchy/bolt/internal/queue"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails application struct. Its exported methods are available
// as IPC bindings from the frontend.
type App struct {
	ctx    context.Context
	engine *engine.Engine
	store  *db.Store
	cfg    *config.Config
	bus    *event.Bus
	queue  *queue.Manager
	subID  int
}

// New creates a new App.
func New(eng *engine.Engine, store *db.Store, cfg *config.Config, bus *event.Bus, queueMgr *queue.Manager) *App {
	return &App{
		engine: eng,
		store:  store,
		cfg:    cfg,
		bus:    bus,
		queue:  queueMgr,
	}
}

// OnStartup is called when the Wails app starts.
func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx

	ch, subID := a.bus.Subscribe()
	a.subID = subID

	go func() {
		for evt := range ch {
			switch e := evt.(type) {
			case event.Progress:
				wailsRuntime.EventsEmit(ctx, "progress", model.ProgressUpdate{
					DownloadID: e.DownloadID,
					Downloaded: e.Downloaded,
					TotalSize:  e.TotalSize,
					Speed:      e.Speed,
					ETA:        e.ETA,
					Status:     model.Status(e.Status),
				})
			case event.DownloadAdded:
				wailsRuntime.EventsEmit(ctx, "download_added", map[string]any{
					"id":         e.DownloadID,
					"filename":   e.Filename,
					"total_size": e.TotalSize,
				})
			case event.DownloadCompleted:
				wailsRuntime.EventsEmit(ctx, "download_completed", map[string]any{
					"id":       e.DownloadID,
					"filename": e.Filename,
				})
			case event.DownloadFailed:
				wailsRuntime.EventsEmit(ctx, "download_failed", map[string]any{
					"id":    e.DownloadID,
					"error": e.Error,
				})
			case event.DownloadPaused:
				wailsRuntime.EventsEmit(ctx, "download_paused", map[string]any{
					"id": e.DownloadID,
				})
			case event.DownloadResumed:
				wailsRuntime.EventsEmit(ctx, "download_resumed", map[string]any{
					"id": e.DownloadID,
				})
			case event.DownloadRemoved:
				wailsRuntime.EventsEmit(ctx, "download_removed", map[string]any{
					"id": e.DownloadID,
				})
			case event.RefreshNeeded:
				wailsRuntime.EventsEmit(ctx, "refresh_needed", map[string]any{
					"id": e.DownloadID,
				})
			}
		}
	}()
}

// OnShutdown is called when the Wails app is closing.
func (a *App) OnShutdown(ctx context.Context) {
	a.bus.Unsubscribe(a.subID)
}

// --- Download operations ---

// AddDownload creates a new download and enqueues it.
func (a *App) AddDownload(req model.AddRequest) (*model.Download, error) {
	dl, err := a.engine.AddDownload(context.Background(), req)
	if err != nil {
		return nil, err
	}
	a.queue.Enqueue(dl.ID)
	return dl, nil
}

// ListDownloads returns downloads matching the filter.
func (a *App) ListDownloads(status string, limit, offset int) ([]model.Download, error) {
	downloads, err := a.engine.ListDownloads(context.Background(), model.ListFilter{
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, err
	}
	if downloads == nil {
		downloads = []model.Download{}
	}
	return downloads, nil
}

// GetDownload returns a download and its segments.
func (a *App) GetDownload(id string) (*model.Download, error) {
	dl, _, err := a.engine.GetDownload(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return dl, nil
}

// PauseDownload pauses an active download.
func (a *App) PauseDownload(id string) error {
	return a.engine.PauseDownload(context.Background(), id)
}

// ResumeDownload resumes a paused download.
func (a *App) ResumeDownload(id string) error {
	return a.engine.ResumeDownload(context.Background(), id)
}

// CancelDownload stops and removes a download.
func (a *App) CancelDownload(id string, deleteFile bool) error {
	return a.engine.CancelDownload(context.Background(), id, deleteFile)
}

// RetryDownload retries a failed download.
func (a *App) RetryDownload(id string) error {
	return a.engine.RetryDownload(context.Background(), id)
}

// RefreshURL updates the URL for a failed download.
func (a *App) RefreshURL(id string, newURL string) error {
	return a.engine.RefreshURL(context.Background(), id, newURL)
}

// Probe sends a HEAD request to discover metadata about a URL.
func (a *App) Probe(url string, headers map[string]string) (*model.ProbeResult, error) {
	if headers == nil {
		headers = make(map[string]string)
	}
	return a.engine.ProbeURL(context.Background(), url, headers)
}

// --- Config operations ---

// SafeConfig is the config without the auth token.
type SafeConfig struct {
	DownloadDir      string `json:"download_dir"`
	MaxConcurrent    int    `json:"max_concurrent"`
	DefaultSegments  int    `json:"default_segments"`
	GlobalSpeedLimit int64  `json:"global_speed_limit"`
	ServerPort       int    `json:"server_port"`
	MinimizeToTray   bool   `json:"minimize_to_tray"`
	MaxRetries       int    `json:"max_retries"`
	Theme            string `json:"theme"`
}

// GetConfig returns the current configuration (without auth token).
func (a *App) GetConfig() SafeConfig {
	return SafeConfig{
		DownloadDir:      a.cfg.DownloadDir,
		MaxConcurrent:    a.cfg.MaxConcurrent,
		DefaultSegments:  a.cfg.DefaultSegments,
		GlobalSpeedLimit: a.cfg.GlobalSpeedLimit,
		ServerPort:       a.cfg.ServerPort,
		MinimizeToTray:   a.cfg.MinimizeToTray,
		MaxRetries:       a.cfg.MaxRetries,
		Theme:            a.cfg.Theme,
	}
}

// ConfigUpdate holds optional config fields to update.
type ConfigUpdate struct {
	DownloadDir      *string `json:"download_dir"`
	MaxConcurrent    *int    `json:"max_concurrent"`
	DefaultSegments  *int    `json:"default_segments"`
	GlobalSpeedLimit *int64  `json:"global_speed_limit"`
	MaxRetries       *int    `json:"max_retries"`
	MinimizeToTray   *bool   `json:"minimize_to_tray"`
	Theme            *string `json:"theme"`
}

// UpdateConfig applies a partial config update, validates, and saves.
func (a *App) UpdateConfig(partial ConfigUpdate) error {
	if partial.DownloadDir != nil {
		a.cfg.DownloadDir = *partial.DownloadDir
	}
	if partial.MaxConcurrent != nil {
		a.cfg.MaxConcurrent = *partial.MaxConcurrent
	}
	if partial.DefaultSegments != nil {
		a.cfg.DefaultSegments = *partial.DefaultSegments
	}
	if partial.GlobalSpeedLimit != nil {
		a.cfg.GlobalSpeedLimit = *partial.GlobalSpeedLimit
	}
	if partial.MaxRetries != nil {
		a.cfg.MaxRetries = *partial.MaxRetries
	}
	if partial.MinimizeToTray != nil {
		a.cfg.MinimizeToTray = *partial.MinimizeToTray
	}
	if partial.Theme != nil {
		a.cfg.Theme = *partial.Theme
	}

	if err := a.cfg.Validate(); err != nil {
		return err
	}

	if err := a.cfg.Save(config.DefaultPath()); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	if partial.MaxConcurrent != nil {
		a.queue.SetMaxConcurrent(*partial.MaxConcurrent)
	}

	return nil
}

// GetAuthToken returns the auth token.
func (a *App) GetAuthToken() string {
	return a.cfg.AuthToken
}

// --- Stats ---

// Stats holds download count statistics.
type Stats struct {
	Active    int `json:"active"`
	Queued    int `json:"queued"`
	Completed int `json:"completed"`
}

// GetStats returns download count statistics.
func (a *App) GetStats() Stats {
	ctx := context.Background()
	active, _ := a.store.CountByStatus(ctx, model.StatusActive)
	queued, _ := a.store.CountByStatus(ctx, model.StatusQueued)
	completed, _ := a.store.CountByStatus(ctx, model.StatusCompleted)
	return Stats{
		Active:    active,
		Queued:    queued,
		Completed: completed,
	}
}

// --- Bulk operations ---

// PauseAll pauses all active downloads.
func (a *App) PauseAll() error {
	downloads, err := a.engine.ListDownloads(context.Background(), model.ListFilter{
		Status: string(model.StatusActive),
	})
	if err != nil {
		return err
	}
	for _, dl := range downloads {
		_ = a.engine.PauseDownload(context.Background(), dl.ID)
	}
	return nil
}

// ResumeAll resumes all paused downloads.
func (a *App) ResumeAll() error {
	downloads, err := a.engine.ListDownloads(context.Background(), model.ListFilter{
		Status: string(model.StatusPaused),
	})
	if err != nil {
		return err
	}
	for _, dl := range downloads {
		_ = a.engine.ResumeDownload(context.Background(), dl.ID)
	}
	return nil
}

// ClearCompleted removes all completed downloads.
func (a *App) ClearCompleted() error {
	downloads, err := a.engine.ListDownloads(context.Background(), model.ListFilter{
		Status: string(model.StatusCompleted),
	})
	if err != nil {
		return err
	}
	for _, dl := range downloads {
		_ = a.engine.CancelDownload(context.Background(), dl.ID, false)
	}
	return nil
}

// --- File operations ---

// SelectDirectory opens a native directory picker dialog.
func (a *App) SelectDirectory() (string, error) {
	dir, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Download Directory",
	})
	if err != nil {
		return "", err
	}
	return dir, nil
}

// OpenFile opens a file with the system default application.
func (a *App) OpenFile(path string) error {
	return openPath(path)
}

// OpenFolder opens the parent directory of a file.
func (a *App) OpenFolder(path string) error {
	return openPath(filepath.Dir(path))
}

func openPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
