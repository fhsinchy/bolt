package server

import (
	"encoding/json"
	"net/http"

	"github.com/fhsinchy/bolt/internal/service"
)

// Server provides the HTTP API for controlling the download engine.
type Server struct {
	svc *service.Service
}

// New creates a new Server.
func New(svc *service.Service) *Server {
	return &Server{
		svc: svc,
	}
}

// Handler returns the middleware-wrapped HTTP handler. The caller owns
// listener creation and http.Server lifecycle.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// REST routes (Go 1.22+ patterns)
	mux.HandleFunc("POST /api/downloads", s.handleAddDownload)
	mux.HandleFunc("GET /api/downloads", s.handleListDownloads)
	mux.HandleFunc("PUT /api/downloads/reorder", s.handleReorderDownloads)
	mux.HandleFunc("POST /api/downloads/pause-all", s.handlePauseAll)
	mux.HandleFunc("POST /api/downloads/resume-all", s.handleResumeAll)
	mux.HandleFunc("GET /api/downloads/{id}", s.handleGetDownload)
	mux.HandleFunc("DELETE /api/downloads/{id}", s.handleDeleteDownload)
	mux.HandleFunc("POST /api/downloads/{id}/pause", s.handlePauseDownload)
	mux.HandleFunc("POST /api/downloads/{id}/resume", s.handleResumeDownload)
	mux.HandleFunc("POST /api/downloads/{id}/retry", s.handleRetryDownload)
	mux.HandleFunc("POST /api/downloads/{id}/refresh", s.handleRefreshURL)
	mux.HandleFunc("POST /api/downloads/{id}/set-refresh", s.handleSetRefresh)
	mux.HandleFunc("POST /api/downloads/{id}/checksum", s.handleUpdateChecksum)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handleUpdateConfig)
	mux.HandleFunc("GET /api/stats", s.handleGetStats)
	mux.HandleFunc("POST /api/probe", s.handleProbe)
	mux.HandleFunc("GET /ws", s.handleWebSocket)

	// Apply middleware chain: recovery -> logging
	return s.recovery(s.logging(mux))
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
		"code":  code,
	})
}
