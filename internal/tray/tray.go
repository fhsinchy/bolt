package tray

import (
	"github.com/energye/systray"
)

// Callbacks configures tray menu actions.
type Callbacks struct {
	OnShow      func()
	OnHide      func()
	OnPauseAll  func()
	OnResumeAll func()
	OnSettings  func()
	OnQuit      func()
}

var (
	visible   = true
	showItem  *systray.MenuItem
	callbacks Callbacks
)

// Start initializes the system tray. It should be called after the main
// window is created. It uses RunWithExternalLoop so it does not block
// or interfere with Wails' main thread.
func Start(cb Callbacks) {
	callbacks = cb

	start, _ := systray.RunWithExternalLoop(func() {
		systray.SetIcon(iconData)
		systray.SetTitle("Bolt")
		systray.SetTooltip("Bolt Download Manager")

		showItem = systray.AddMenuItem("Hide", "Toggle window visibility")
		systray.AddSeparator()
		mPause := systray.AddMenuItem("Pause All", "Pause all downloads")
		mResume := systray.AddMenuItem("Resume All", "Resume all downloads")
		systray.AddSeparator()
		mSettings := systray.AddMenuItem("Settings", "Open settings")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit Bolt")

		showItem.Click(func() {
			if visible {
				visible = false
				showItem.SetTitle("Show")
				if cb.OnHide != nil {
					cb.OnHide()
				}
			} else {
				visible = true
				showItem.SetTitle("Hide")
				if cb.OnShow != nil {
					cb.OnShow()
				}
			}
		})

		// Click on the tray icon itself toggles window visibility.
		systray.SetOnClick(func(_ systray.IMenu) {
			if visible {
				visible = false
				showItem.SetTitle("Show")
				if cb.OnHide != nil {
					cb.OnHide()
				}
			} else {
				visible = true
				showItem.SetTitle("Hide")
				if cb.OnShow != nil {
					cb.OnShow()
				}
			}
		})

		mPause.Click(func() {
			if cb.OnPauseAll != nil {
				cb.OnPauseAll()
			}
		})

		mResume.Click(func() {
			if cb.OnResumeAll != nil {
				cb.OnResumeAll()
			}
		})

		mSettings.Click(func() {
			if cb.OnSettings != nil {
				cb.OnSettings()
			}
		})

		mQuit.Click(func() {
			if cb.OnQuit != nil {
				cb.OnQuit()
			}
		})
	}, nil)
	start()
}

// SetVisible updates the tray's tracked visibility state and the menu item
// label WITHOUT firing callbacks. Use this to sync tray state when the
// window is shown/hidden from outside the tray (e.g. window close button,
// or the WindowShow API event).
func SetVisible(v bool) {
	if v == visible {
		return
	}
	visible = v
	if showItem == nil {
		return
	}
	if visible {
		showItem.SetTitle("Hide")
	} else {
		showItem.SetTitle("Show")
	}
}

// Quit cleans up the system tray.
func Quit() {
	systray.Quit()
}
