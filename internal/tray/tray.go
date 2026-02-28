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
	OnQuit      func()
}

var visible = true

// Start initializes the system tray. It should be called after the main
// window is created. It uses RunWithExternalLoop so it does not block
// or interfere with Wails' main thread.
func Start(cb Callbacks) {
	systray.RunWithExternalLoop(func() {
		systray.SetIcon(iconData)
		systray.SetTitle("Bolt")
		systray.SetTooltip("Bolt Download Manager")

		mShow := systray.AddMenuItem("Hide", "Toggle window visibility")
		systray.AddSeparator()
		mPause := systray.AddMenuItem("Pause All", "Pause all downloads")
		mResume := systray.AddMenuItem("Resume All", "Resume all downloads")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit Bolt")

		mShow.Click(func() {
			if visible {
				if cb.OnHide != nil {
					cb.OnHide()
				}
				mShow.SetTitle("Show")
				visible = false
			} else {
				if cb.OnShow != nil {
					cb.OnShow()
				}
				mShow.SetTitle("Hide")
				visible = true
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

		mQuit.Click(func() {
			if cb.OnQuit != nil {
				cb.OnQuit()
			}
		})
	}, nil)
}

// Quit cleans up the system tray.
func Quit() {
	systray.Quit()
}
