package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bolt "github.com/fhsinchy/bolt"
	"github.com/fhsinchy/bolt/internal/app"
	"github.com/fhsinchy/bolt/internal/tray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed appicon.png
var appIcon []byte

func launchGUI(minimized bool) {
	d := setupDaemon()
	defer d.cleanup()

	application := app.New(d.engine, d.store, d.cfg, d.bus, d.queueMgr)
	application.SetWindowShowHook(func() {
		tray.SetVisible(true)
	})

	// Start queue manager goroutine
	go d.queueMgr.Run(d.ctx)

	// Start HTTP server goroutine (for browser extension compatibility)
	serverErr := make(chan error, 1)
	go func() {
		if err := d.server.Start(d.ctx); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Check for immediate startup failure (e.g., port already in use)
	select {
	case err := <-serverErr:
		slog.Error("HTTP server failed to start — CLI and browser extension will not work",
			"port", d.cfg.ServerPort, "error", err)
	case <-time.After(200 * time.Millisecond):
		// Server bound successfully
	}

	// Re-queue interrupted downloads (crash recovery)
	if err := d.engine.Start(d.ctx); err != nil {
		slog.Error("re-queue interrupted downloads", "error", err)
	}
	d.queueMgr.Enqueue("") // signal queue to evaluate re-queued downloads

	fmt.Printf("Bolt %s — GUI mode\n", version)

	quitting := false

	// Wrap OnStartup to also start the system tray.
	onStartup := func(ctx context.Context) {
		application.OnStartup(ctx)

		tray.Start(tray.Callbacks{
			OnShow: func() {
				wailsRuntime.WindowShow(ctx)
			},
			OnHide: func() {
				wailsRuntime.WindowHide(ctx)
			},
			OnPauseAll: func() {
				_ = application.PauseAll()
			},
			OnResumeAll: func() {
				_ = application.ResumeAll()
			},
			OnSettings: func() {
				wailsRuntime.WindowShow(ctx)
				tray.SetVisible(true)
				wailsRuntime.EventsEmit(ctx, "open_settings")
			},
			OnQuit: func() {
				quitting = true
				tray.Quit()
				wailsRuntime.Quit(ctx)
			},
		})

		if minimized {
			tray.SetVisible(false)
		}

		// Handle SIGTERM/SIGINT so systemctl stop works.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			quitting = true
			tray.Quit()
			wailsRuntime.Quit(ctx)
		}()
	}

	onShutdown := func(ctx context.Context) {
		tray.Quit()
		application.OnShutdown(ctx)
	}

	err := wails.Run(&options.App{
		Title:         "Bolt",
		Width:         960,
		Height:        640,
		MinWidth:      640,
		MinHeight:     480,
		StartHidden:   minimized,
		AssetServer: &assetserver.Options{
			Assets: bolt.FrontendAssets,
		},
		Linux: &linux.Options{
			Icon:        appIcon,
			ProgramName: "bolt",
		},
		OnStartup:  onStartup,
		OnShutdown: onShutdown,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			if quitting {
				return false
			}
			if d.cfg.MinimizeToTray {
				wailsRuntime.WindowHide(ctx)
				tray.SetVisible(false)
				return true
			}
			return false
		},
		Bind: []any{
			application,
		},
	})
	if err != nil {
		fatal(fmt.Errorf("wails: %w", err))
	}

	// After Wails exits, shut down gracefully
	d.shutdown()
}
