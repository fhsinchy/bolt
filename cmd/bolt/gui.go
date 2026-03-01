package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	bolt "github.com/fhsinchy/bolt"
	"github.com/fhsinchy/bolt/internal/app"
	"github.com/fhsinchy/bolt/internal/tray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func launchGUI() {
	d := setupDaemon()
	defer d.cleanup()

	application := app.New(d.engine, d.store, d.cfg, d.bus, d.queueMgr)
	application.SetWindowShowHook(func() {
		tray.SetVisible(true)
	})

	// Start queue manager goroutine
	go d.queueMgr.Run(d.ctx)

	// Start HTTP server goroutine (for CLI and browser extension compatibility)
	go func() {
		if err := d.server.Start(d.ctx); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
		}
	}()

	// Resume interrupted downloads
	if err := d.engine.Start(d.ctx); err != nil {
		slog.Error("resume interrupted downloads", "error", err)
	}

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
			OnQuit: func() {
				quitting = true
				tray.Quit()
				wailsRuntime.Quit(ctx)
			},
		})
	}

	onShutdown := func(ctx context.Context) {
		tray.Quit()
		application.OnShutdown(ctx)
	}

	minimizeToTray := d.cfg.MinimizeToTray

	err := wails.Run(&options.App{
		Title:     "Bolt",
		Width:     960,
		Height:    640,
		MinWidth:  640,
		MinHeight: 480,
		AssetServer: &assetserver.Options{
			Assets: bolt.FrontendAssets,
		},
		OnStartup:  onStartup,
		OnShutdown: onShutdown,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			if quitting {
				return false
			}
			if minimizeToTray {
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
