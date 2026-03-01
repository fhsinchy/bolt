package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/model"
)

func (s *Server) handleAddDownload(w http.ResponseWriter, r *http.Request) {
	var req model.AddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	dl, err := s.engine.AddDownload(r.Context(), req)
	if err != nil {
		mapEngineError(w, err)
		return
	}

	s.queue.Enqueue(dl.ID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"download": dl,
	})
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")

	downloads, err := s.engine.ListDownloads(r.Context(), model.ListFilter{
		Status: status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	if downloads == nil {
		downloads = []model.Download{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"downloads": downloads,
		"total":     len(downloads),
	})
}

func (s *Server) handleGetDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	dl, segments, err := s.engine.GetDownload(r.Context(), id)
	if err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"download": dl,
		"segments": segments,
	})
}

func (s *Server) handleDeleteDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deleteFile := r.URL.Query().Get("delete_file") == "true"

	if err := s.engine.CancelDownload(r.Context(), id, deleteFile); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
	})
}

func (s *Server) handlePauseDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.engine.PauseDownload(r.Context(), id); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "paused",
	})
}

func (s *Server) handleResumeDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.engine.ResumeDownload(r.Context(), id); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "resumed",
	})
}

func (s *Server) handleRetryDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.engine.RetryDownload(r.Context(), id); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "retrying",
	})
}

func (s *Server) handleRefreshURL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required", "VALIDATION_ERROR")
		return
	}

	if err := s.engine.RefreshURL(r.Context(), id, body.URL, body.Headers); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "refreshed",
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	// Return config without the auth token.
	type safeConfig struct {
		DownloadDir      string              `json:"download_dir"`
		Categorize       bool                `json:"categorize"`
		MaxConcurrent    int                 `json:"max_concurrent"`
		DefaultSegments  int                 `json:"default_segments"`
		GlobalSpeedLimit int64               `json:"global_speed_limit"`
		ServerPort       int                 `json:"server_port"`
		MinimizeToTray   bool                `json:"minimize_to_tray"`
		ClipboardMonitor bool                `json:"clipboard_monitor"`
		SoundOnComplete  bool                `json:"sound_on_complete"`
		Theme            string              `json:"theme"`
		Proxy            string              `json:"proxy"`
		MaxRetries       int                 `json:"max_retries"`
		MinSegmentSize   int64               `json:"min_segment_size"`
		Categories       map[string][]string `json:"categories"`
	}

	writeJSON(w, http.StatusOK, safeConfig{
		DownloadDir:      s.cfg.DownloadDir,
		Categorize:       s.cfg.Categorize,
		MaxConcurrent:    s.cfg.MaxConcurrent,
		DefaultSegments:  s.cfg.DefaultSegments,
		GlobalSpeedLimit: s.cfg.GlobalSpeedLimit,
		ServerPort:       s.cfg.ServerPort,
		MinimizeToTray:   s.cfg.MinimizeToTray,
		ClipboardMonitor: s.cfg.ClipboardMonitor,
		SoundOnComplete:  s.cfg.SoundOnComplete,
		Theme:            s.cfg.Theme,
		Proxy:            s.cfg.Proxy,
		MaxRetries:       s.cfg.MaxRetries,
		MinSegmentSize:   s.cfg.MinSegmentSize,
		Categories:       s.cfg.Categories,
	})
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var partial struct {
		DownloadDir      *string `json:"download_dir"`
		MaxConcurrent    *int    `json:"max_concurrent"`
		DefaultSegments  *int    `json:"default_segments"`
		GlobalSpeedLimit *int64  `json:"global_speed_limit"`
		MaxRetries       *int    `json:"max_retries"`
		Theme            *string `json:"theme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&partial); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if partial.DownloadDir != nil {
		s.cfg.DownloadDir = *partial.DownloadDir
	}
	if partial.MaxConcurrent != nil {
		s.cfg.MaxConcurrent = *partial.MaxConcurrent
	}
	if partial.DefaultSegments != nil {
		s.cfg.DefaultSegments = *partial.DefaultSegments
	}
	if partial.GlobalSpeedLimit != nil {
		s.cfg.GlobalSpeedLimit = *partial.GlobalSpeedLimit
	}
	if partial.MaxRetries != nil {
		s.cfg.MaxRetries = *partial.MaxRetries
	}
	if partial.Theme != nil {
		s.cfg.Theme = *partial.Theme
	}

	if err := s.cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
		return
	}

	if err := s.cfg.Save(config.DefaultPath()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config", "INTERNAL_ERROR")
		return
	}

	if partial.MaxConcurrent != nil {
		s.queue.SetMaxConcurrent(*partial.MaxConcurrent)
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "updated",
	})
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	active, _ := s.store.CountByStatus(ctx, model.StatusActive)
	queued, _ := s.store.CountByStatus(ctx, model.StatusQueued)
	completed, _ := s.store.CountByStatus(ctx, model.StatusCompleted)

	writeJSON(w, http.StatusOK, map[string]any{
		"active_count":    active,
		"queued_count":    queued,
		"completed_count": completed,
		"version":         "0.3.0-dev",
	})
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required", "VALIDATION_ERROR")
		return
	}

	result, err := s.engine.ProbeURL(r.Context(), body.URL, body.Headers)
	if err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleShowWindow(w http.ResponseWriter, r *http.Request) {
	s.bus.Publish(event.WindowShow{})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// mapEngineError maps engine sentinel errors to HTTP status codes.
func mapEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
	case errors.Is(err, model.ErrAlreadyActive):
		writeError(w, http.StatusConflict, err.Error(), "CONFLICT")
	case errors.Is(err, model.ErrAlreadyPaused):
		writeError(w, http.StatusConflict, err.Error(), "CONFLICT")
	case errors.Is(err, model.ErrAlreadyCompleted):
		writeError(w, http.StatusConflict, err.Error(), "CONFLICT")
	case errors.Is(err, model.ErrDuplicateURL):
		writeError(w, http.StatusConflict, err.Error(), "CONFLICT")
	case errors.Is(err, model.ErrInvalidURL):
		writeError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
	case errors.Is(err, model.ErrInvalidSegments):
		writeError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
	case errors.Is(err, model.ErrSizeMismatch):
		writeError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
	case errors.Is(err, model.ErrProbeRejected):
		writeError(w, http.StatusBadGateway, err.Error(), "PROBE_FAILED")
	default:
		writeError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
	}
}
