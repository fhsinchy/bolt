package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/server"
)

var version = "dev"

func main() {
	minimized := false
	args := os.Args[1:]

	// Parse global flags
	filtered := args[:0]
	for _, arg := range args {
		if arg == "--minimized" {
			minimized = true
		} else {
			filtered = append(filtered, arg)
		}
	}
	args = filtered

	if len(args) == 0 {
		// No args — launch GUI, or raise existing window.
		if raiseExistingWindow() {
			return
		}
		launchGUI(minimized)
		return
	}

	switch args[0] {
	case "start":
		if raiseExistingWindow() {
			return
		}
		launchGUI(minimized)
	case "version", "--version", "-v":
		fmt.Printf("bolt %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`bolt - fast segmented download manager

Usage:
  bolt                  Start the GUI
  bolt start            Start the GUI
  bolt version          Show version
  bolt help             Show this help

Flags:
  --minimized           Start minimized to system tray
`)
}

// daemon holds shared resources for the GUI.
type daemon struct {
	cfg      *config.Config
	store    *db.Store
	bus      *event.Bus
	engine   *engine.Engine
	queueMgr *queue.Manager
	server   *server.Server
	ctx      context.Context
	cancel   context.CancelFunc
	subID    int
}

// setupDaemon initializes all shared resources (config, DB, engine, queue, server).
func setupDaemon() *daemon {
	// 1. Load config
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fatal(fmt.Errorf("loading config: %w", err))
	}

	// 2. Open database
	dbPath := filepath.Join(config.Dir(), "bolt.db")
	store, err := db.Open(dbPath)
	if err != nil {
		fatal(fmt.Errorf("opening database: %w", err))
	}

	// 3. Create event bus, engine, queue manager
	bus := event.NewBus()
	eng := engine.New(store, cfg, bus)

	ctx, cancel := context.WithCancel(context.Background())

	var queueMgr *queue.Manager
	queueMgr = queue.New(store, bus, cfg.MaxConcurrent, func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	})

	// 4. Wire queue completion
	ch, subID := bus.Subscribe()
	go func() {
		for evt := range ch {
			switch evt.(type) {
			case event.DownloadCompleted, event.DownloadFailed, event.DownloadPaused:
				var dlID string
				switch e := evt.(type) {
				case event.DownloadCompleted:
					dlID = e.DownloadID
				case event.DownloadFailed:
					dlID = e.DownloadID
				case event.DownloadPaused:
					dlID = e.DownloadID
				}
				queueMgr.OnDownloadComplete(dlID)
			}
		}
	}()

	// 5. Create HTTP server
	srv := server.New(eng, store, cfg, bus, queueMgr)

	return &daemon{
		cfg:      cfg,
		store:    store,
		bus:      bus,
		engine:   eng,
		queueMgr: queueMgr,
		server:   srv,
		ctx:      ctx,
		cancel:   cancel,
		subID:    subID,
	}
}

// cleanup releases resources that should always be released.
func (d *daemon) cleanup() {
	d.bus.Unsubscribe(d.subID)
	d.store.Close()
}

// shutdown gracefully shuts down the server and engine.
func (d *daemon) shutdown() {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := d.server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown", "error", err)
	}
	if err := d.engine.Shutdown(shutdownCtx); err != nil {
		slog.Error("engine shutdown", "error", err)
	}
	d.cancel()
}

// raiseExistingWindow checks if another Bolt instance is already running by
// probing the HTTP server. If reachable, it asks the server to show the window.
// Returns true if an existing instance was found and raised.
func raiseExistingWindow() bool {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return false
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.ServerPort)

	// Probe the server
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", base+"/api/stats", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Server is running — ask it to show the window
	req, err = http.NewRequest("POST", base+"/api/window/show", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)

	resp, err = client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	fmt.Println("Bolt is already running — window raised.")
	return true
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
