package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/model"
)

func (s *Server) handleAddDownload(w http.ResponseWriter, r *http.Request) {
	var req model.AddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	dl, err := s.svc.AddDownload(r.Context(), req)
	if err != nil {
		var dupErr *model.DuplicateDownloadError
		if errors.As(err, &dupErr) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"code":     "DUPLICATE_FILENAME",
				"error":    dupErr.Error(),
				"existing": dupErr.Existing,
			})
			return
		}
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"download": dl,
	})
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")

	downloads, err := s.svc.ListDownloads(r.Context(), model.ListFilter{
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

	dl, segments, err := s.svc.GetDownload(r.Context(), id)
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

	if err := s.svc.CancelDownload(r.Context(), id, deleteFile); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
	})
}

func (s *Server) handlePauseDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.svc.PauseDownload(r.Context(), id); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "paused",
	})
}

func (s *Server) handleResumeDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.svc.ResumeDownload(r.Context(), id); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "resumed",
	})
}

func (s *Server) handleRetryDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.svc.RetryDownload(r.Context(), id); err != nil {
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

	if err := s.svc.RefreshURL(r.Context(), id, body.URL, body.Headers); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "refreshed",
	})
}

func (s *Server) handleSetRefresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.svc.SetRefreshStatus(r.Context(), id); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "refresh",
	})
}

func (s *Server) handleUpdateChecksum(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		Checksum *model.Checksum `json:"checksum"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if err := s.svc.UpdateChecksum(r.Context(), id, body.Checksum); err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "updated",
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.GetConfig())
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var partial struct {
		DownloadDir      *string `json:"download_dir"`
		MaxConcurrent    *int    `json:"max_concurrent"`
		DefaultSegments  *int    `json:"default_segments"`
		GlobalSpeedLimit *int64  `json:"global_speed_limit"`
		MaxRetries       *int    `json:"max_retries"`
		Notifications    *bool   `json:"notifications"`
		MinSegmentSize   *int64  `json:"min_segment_size"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&partial); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	err := s.svc.UpdateConfig(r.Context(), func(cfg *config.Config) {
		if partial.DownloadDir != nil {
			cfg.DownloadDir = *partial.DownloadDir
		}
		if partial.MaxConcurrent != nil {
			cfg.MaxConcurrent = *partial.MaxConcurrent
		}
		if partial.DefaultSegments != nil {
			cfg.DefaultSegments = *partial.DefaultSegments
		}
		if partial.GlobalSpeedLimit != nil {
			cfg.GlobalSpeedLimit = *partial.GlobalSpeedLimit
		}
		if partial.MaxRetries != nil {
			cfg.MaxRetries = *partial.MaxRetries
		}
		if partial.Notifications != nil {
			cfg.Notifications = *partial.Notifications
		}
		if partial.MinSegmentSize != nil {
			cfg.MinSegmentSize = *partial.MinSegmentSize
		}
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
		return
	}

	var attrs []any
	if partial.MaxConcurrent != nil {
		attrs = append(attrs, "max_concurrent", *partial.MaxConcurrent)
	}
	if partial.DefaultSegments != nil {
		attrs = append(attrs, "default_segments", *partial.DefaultSegments)
	}
	if partial.GlobalSpeedLimit != nil {
		attrs = append(attrs, "global_speed_limit", *partial.GlobalSpeedLimit)
	}
	if partial.Notifications != nil {
		attrs = append(attrs, "notifications", *partial.Notifications)
	}
	if partial.DownloadDir != nil {
		attrs = append(attrs, "download_dir", *partial.DownloadDir)
	}
	if partial.MaxRetries != nil {
		attrs = append(attrs, "max_retries", *partial.MaxRetries)
	}
	if partial.MinSegmentSize != nil {
		attrs = append(attrs, "min_segment_size", *partial.MinSegmentSize)
	}
	if len(attrs) > 0 {
		slog.Info("config updated", attrs...)
	}

	if partial.MaxConcurrent != nil {
		s.svc.SetMaxConcurrent(*partial.MaxConcurrent)
	}
	if partial.GlobalSpeedLimit != nil {
		s.svc.SetSpeedLimit(*partial.GlobalSpeedLimit)
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "updated",
	})
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.GetStats(r.Context()))
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

	result, err := s.svc.ProbeURL(r.Context(), body.URL, body.Headers)
	if err != nil {
		mapEngineError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleReorderDownloads(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderedIDs []string `json:"ordered_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if len(body.OrderedIDs) == 0 {
		writeError(w, http.StatusBadRequest, "ordered_ids is required", "VALIDATION_ERROR")
		return
	}

	if err := s.svc.ReorderDownloads(r.Context(), body.OrderedIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "reordered",
	})
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
