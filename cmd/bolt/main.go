package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fhsinchy/bolt/internal/cli"
	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/db"
	"github.com/fhsinchy/bolt/internal/engine"
	"github.com/fhsinchy/bolt/internal/event"
	"github.com/fhsinchy/bolt/internal/pid"
	"github.com/fhsinchy/bolt/internal/queue"
	"github.com/fhsinchy/bolt/internal/server"
)

const version = "0.3.0-dev"

func main() {
	if len(os.Args) < 2 {
		// No args — launch GUI.
		launchGUI()
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "start":
		headless := false
		for _, arg := range args {
			if arg == "--headless" {
				headless = true
			}
		}
		if headless {
			launchHeadless()
		} else {
			launchGUI()
		}
	case "stop":
		runStop()
	case "add":
		runWithClient(func(ctx context.Context, c *cli.Client) error {
			return runAdd(ctx, c, args)
		})
	case "list", "ls":
		runWithClient(func(ctx context.Context, c *cli.Client) error {
			return runList(ctx, c, args)
		})
	case "status":
		runWithClient(func(ctx context.Context, c *cli.Client) error {
			return runStatus(ctx, c, args)
		})
	case "pause":
		runWithClient(func(ctx context.Context, c *cli.Client) error {
			return runPause(ctx, c, args)
		})
	case "resume":
		runWithClient(func(ctx context.Context, c *cli.Client) error {
			return runResume(ctx, c, args)
		})
	case "cancel", "rm":
		runWithClient(func(ctx context.Context, c *cli.Client) error {
			return runCancel(ctx, c, args)
		})
	case "refresh":
		runWithClient(func(ctx context.Context, c *cli.Client) error {
			return runRefresh(ctx, c, args)
		})
	case "version", "--version", "-v":
		fmt.Printf("bolt %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`bolt - fast segmented download manager

Usage:
  bolt                       Start the GUI
  bolt start                 Start the GUI
  bolt start --headless      Start the daemon (headless, no GUI)
  bolt stop                  Stop the running daemon
  bolt add <url> [flags]     Add and start a download
  bolt list [flags]          List downloads
  bolt status <id>           Show download details
  bolt pause <id>            Pause a download
  bolt resume <id>           Resume a paused download
  bolt cancel <id> [flags]   Cancel and remove a download
  bolt refresh <id> <url>    Update URL for a failed download
  bolt version               Show version

Add flags:
  --dir <path>        Download directory
  --filename <name>   Override filename
  --segments <n>      Number of segments (1-32)
  --header <k:v>      Custom header (repeatable)
  --referer <url>     Referer URL
  --json              Output as JSON

List flags:
  --status <status>   Filter by status (queued/active/paused/completed/error)
  --json              Output as JSON

Cancel flags:
  --delete-file       Also delete the downloaded file
`)
}

// daemon holds shared resources for both GUI and headless modes.
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

	// 2. Check PID file
	if pid.IsRunning() {
		fatal(fmt.Errorf("daemon is already running (pid file: %s)", pid.Path()))
	}

	// 3. Write PID file
	if err := pid.Write(); err != nil {
		fatal(fmt.Errorf("writing PID file: %w", err))
	}

	// 4. Open database
	dbPath := filepath.Join(config.Dir(), "bolt.db")
	store, err := db.Open(dbPath)
	if err != nil {
		fatal(fmt.Errorf("opening database: %w", err))
	}

	// 5. Create event bus, engine, queue manager
	bus := event.NewBus()
	eng := engine.New(store, cfg, bus)

	ctx, cancel := context.WithCancel(context.Background())

	var queueMgr *queue.Manager
	queueMgr = queue.New(store, bus, cfg.MaxConcurrent, func(ctx context.Context, id string) error {
		return eng.StartDownload(ctx, id)
	})

	// 6. Wire queue completion
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

	// 7. Create HTTP server
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
	pid.Remove()
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

// launchHeadless starts the daemon with HTTP server (no GUI).
func launchHeadless() {
	d := setupDaemon()
	defer d.cleanup()

	// Start queue manager goroutine
	go d.queueMgr.Run(d.ctx)

	// Start HTTP server goroutine
	go func() {
		if err := d.server.Start(d.ctx); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
		}
	}()

	// Resume interrupted downloads
	if err := d.engine.Start(d.ctx); err != nil {
		slog.Error("resume interrupted downloads", "error", err)
	}

	fmt.Printf("Bolt %s — headless mode\n", version)

	// Block on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	fmt.Printf("\nReceived %s, shutting down...\n", sig)

	d.shutdown()
}

// runStop stops the running daemon.
func runStop() {
	c, err := cli.NewClient()
	if err != nil {
		fatal(err)
	}
	if err := c.Stop(); err != nil {
		fatal(err)
	}
}

// runWithClient creates a CLI client, checks the daemon, and runs the command.
func runWithClient(fn func(ctx context.Context, c *cli.Client) error) {
	c, err := cli.NewClient()
	if err != nil {
		fatal(err)
	}

	if err := c.CheckDaemon(); err != nil {
		fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := fn(ctx, c); err != nil {
		if ctx.Err() != nil {
			os.Exit(0)
		}
		fatal(err)
	}
}

func runAdd(ctx context.Context, c *cli.Client, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: bolt add <url> [flags]")
		os.Exit(1)
	}

	opts := cli.AddOptions{
		URL:     args[0],
		Headers: make(map[string]string),
	}

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			i++
			if i < len(args) {
				opts.Dir = args[i]
			}
		case "--filename":
			i++
			if i < len(args) {
				opts.Filename = args[i]
			}
		case "--segments":
			i++
			if i < len(args) {
				var n int
				fmt.Sscanf(args[i], "%d", &n)
				opts.Segments = n
			}
		case "--header":
			i++
			if i < len(args) {
				parts := strings.SplitN(args[i], ":", 2)
				if len(parts) == 2 {
					opts.Headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
			}
		case "--referer":
			i++
			if i < len(args) {
				opts.Referer = args[i]
			}
		case "--json":
			opts.JSON = true
		}
	}

	return c.Add(ctx, opts)
}

func runList(ctx context.Context, c *cli.Client, args []string) error {
	opts := cli.ListOptions{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--status":
			i++
			if i < len(args) {
				opts.Status = args[i]
			}
		case "--json":
			opts.JSON = true
		}
	}

	return c.List(ctx, opts)
}

func runStatus(ctx context.Context, c *cli.Client, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: bolt status <id>")
		os.Exit(1)
	}
	return c.Status(ctx, args[0])
}

func runPause(ctx context.Context, c *cli.Client, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: bolt pause <id>")
		os.Exit(1)
	}
	return c.Pause(ctx, args[0])
}

func runResume(ctx context.Context, c *cli.Client, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: bolt resume <id>")
		os.Exit(1)
	}
	return c.Resume(ctx, args[0], true)
}

func runCancel(ctx context.Context, c *cli.Client, args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: bolt cancel <id> [--delete-file]")
		os.Exit(1)
	}

	id := args[0]
	deleteFile := false
	for _, arg := range args[1:] {
		if arg == "--delete-file" {
			deleteFile = true
		}
	}

	return c.Cancel(ctx, id, deleteFile)
}

func runRefresh(ctx context.Context, c *cli.Client, args []string) error {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: bolt refresh <id> <new-url>")
		os.Exit(1)
	}
	return c.Refresh(ctx, args[0], args[1])
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
