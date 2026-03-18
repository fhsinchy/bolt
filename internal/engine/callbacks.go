package engine

import "github.com/fhsinchy/bolt/internal/model"

// Callbacks contains optional callback functions that the engine invokes
// on download lifecycle events. Any nil callback is silently skipped.
type Callbacks struct {
	OnProgress  func(id string, update model.ProgressUpdate)
	OnCompleted func(id string, dl model.Download)
	OnFailed    func(id string, dl model.Download, err error)
	OnPaused    func(id string)
	OnResumed   func(id string)
	OnAdded     func(dl model.Download)
	OnRemoved   func(id string)
}
