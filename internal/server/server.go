package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/queue"
)

// Server provides the HTTP API for controlling the download engine.
type Server struct {
	engine *engine.Engine
	store  *db.Store
	cfg    *config.Config
	bus    *event.Bus
	queue  *queue.Manager
	srv    *http.Server
}

// New creates a new Server.
func New(eng *engine.Engine, store *db.Store, cfg *config.Config, bus *event.Bus, queueMgr *queue.Manager) *Server {
	return &Server{
		engine: eng,
		store:  store,
		cfg:    cfg,
		bus:    bus,
		queue:  queueMgr,
	}
}

// Start registers routes, applies middleware, and begins listening.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// REST routes (Go 1.22+ patterns)
	mux.HandleFunc("POST /api/downloads", s.handleAddDownload)
	mux.HandleFunc("GET /api/downloads", s.handleListDownloads)
	mux.HandleFunc("PUT /api/downloads/reorder", s.handleReorderDownloads)
	mux.HandleFunc("GET /api/downloads/{id}", s.handleGetDownload)
	mux.HandleFunc("DELETE /api/downloads/{id}", s.handleDeleteDownload)
	mux.HandleFunc("POST /api/downloads/{id}/pause", s.handlePauseDownload)
	mux.HandleFunc("POST /api/downloads/{id}/resume", s.handleResumeDownload)
	mux.HandleFunc("POST /api/downloads/{id}/retry", s.handleRetryDownload)
	mux.HandleFunc("POST /api/downloads/{id}/refresh", s.handleRefreshURL)
	mux.HandleFunc("POST /api/downloads/{id}/set-refresh", s.handleSetRefresh)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handleUpdateConfig)
	mux.HandleFunc("GET /api/stats", s.handleGetStats)
	mux.HandleFunc("POST /api/probe", s.handleProbe)
	mux.HandleFunc("POST /api/window/show", s.handleShowWindow)
	mux.HandleFunc("GET /ws", s.handleWebSocket)

	// Apply middleware chain: recovery -> logging -> auth
	handler := s.recovery(s.logging(s.auth(mux)))

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.ServerPort)

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	fmt.Printf("Server listening on %s\n", addr)

	return s.srv.Serve(ln)
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
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
