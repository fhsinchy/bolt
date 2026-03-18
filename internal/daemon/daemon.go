package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/server"
	"github.com/fhsinchy/bolt/internal/service"
)

// Daemon owns the full lifecycle of the Bolt download manager.
type Daemon struct {
	cfg     *config.Config
	cfgPath string
	store   *db.Store
	engine  *engine.Engine
	queue   *queue.Manager
	service *service.Service
	server  *server.Server

	unixLn     net.Listener
	loopbackLn net.Listener
	sockPath   string
}

// New creates a new Daemon, initialising config, DB, engine, queue, service, and server.
func New(cfgPath string) (*Daemon, error) {
	// 1. Load config
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	// 2. Open DB
	dataDir, err := dataDir()
	if err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	dbPath := filepath.Join(dataDir, "bolt.db")
	store, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// 3. Create service (to get callbacks before engine)
	svc := service.New(nil, nil, store, cfg, cfgPath)
	callbacks := svc.EngineCallbacks()

	// 4. Create engine with callbacks
	eng := engine.New(store, svc.GetConfig, callbacks)

	// 5. Create queue
	queueMgr := queue.New(store, cfg.MaxConcurrent,
		func(ctx context.Context, id string) error {
			return eng.StartDownload(ctx, id)
		},
		func(ctx context.Context, id string) error {
			return eng.PauseDownload(ctx, id)
		},
		svc.OnResumedCallback(),
	)

	// 6. Wire service with engine + queue
	svc.SetEngine(eng)
	svc.SetQueue(queueMgr)

	// 7. Create HTTP server
	srv := server.New(svc)

	return &Daemon{
		cfg:     cfg,
		cfgPath: cfgPath,
		store:   store,
		engine:  eng,
		queue:   queueMgr,
		service: svc,
		server:  srv,
	}, nil
}

// Run starts the daemon and blocks until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	// 1. Instance detection
	d.sockPath = socketPath()
	if isAlreadyRunning(d.sockPath) {
		return fmt.Errorf("another bolt daemon is already running (socket %s is active)", d.sockPath)
	}

	// 2. Crash recovery — set stale active downloads to queued
	if err := d.engine.Start(ctx); err != nil {
		d.store.Close()
		return fmt.Errorf("crash recovery: %w", err)
	}

	// 3. Start queue goroutine
	queueCtx, queueCancel := context.WithCancel(ctx)
	defer queueCancel()
	go d.queue.Run(queueCtx)

	// Signal queue to pick up re-queued downloads
	d.queue.Enqueue("")

	// 4. Create Unix socket listener
	var err error
	d.unixLn, err = createSocketListener(d.sockPath)
	if err != nil {
		d.store.Close()
		return fmt.Errorf("unix socket: %w", err)
	}

	// 5. Create loopback TCP listener
	loopbackAddr := fmt.Sprintf("127.0.0.1:%d", d.cfg.LoopbackPort)
	d.loopbackLn, err = net.Listen("tcp", loopbackAddr)
	if err != nil {
		d.unixLn.Close()
		removeSocket(d.sockPath)
		d.store.Close()
		return fmt.Errorf("loopback listen: %w", err)
	}

	// 6. Serve HTTP on both listeners
	handler := d.server.Handler()

	newHTTPServer := func() *http.Server {
		return &http.Server{
			Handler:      handler,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
	}
	unixSrv := newHTTPServer()
	loopbackSrv := newHTTPServer()

	serveFailed := make(chan error, 2)
	go func() { serveFailed <- unixSrv.Serve(d.unixLn) }()
	go func() { serveFailed <- loopbackSrv.Serve(d.loopbackLn) }()

	// 7. Notify systemd
	sdNotify("READY=1")

	slog.Info("daemon ready",
		"socket", d.sockPath,
		"loopback", loopbackAddr,
	)

	// 8. Block until signal or listener failure
	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-serveFailed:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("listener failed", "error", err)
			serveErr = fmt.Errorf("listener failed: %w", err)
		}
	}

	// 9. Graceful shutdown
	d.shutdown(unixSrv, loopbackSrv)

	return serveErr
}

func (d *Daemon) shutdown(unixSrv, loopbackSrv *http.Server) {
	sdNotify("STOPPING=1")

	slog.Info("shutting down daemon")

	// Close listeners (stops accepting new connections)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = unixSrv.Shutdown(shutdownCtx)
	_ = loopbackSrv.Shutdown(shutdownCtx)

	// Shutdown engine (pauses active downloads, persists progress)
	if err := d.engine.Shutdown(shutdownCtx); err != nil {
		slog.Error("engine shutdown", "error", err)
	}

	// Close DB
	d.store.Close()

	// Remove socket file
	removeSocket(d.sockPath)

	slog.Info("daemon stopped")
}

// dataDir returns the Bolt data directory, creating it if needed.
// Respects $XDG_DATA_HOME, falling back to ~/.local/share.
func dataDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "bolt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
