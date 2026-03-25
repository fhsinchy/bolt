package model

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound           = errors.New("download not found")
	ErrAlreadyActive      = errors.New("download is already active")
	ErrAlreadyPaused      = errors.New("download is already paused")
	ErrAlreadyCompleted   = errors.New("download is already completed")
	ErrInvalidURL         = errors.New("invalid URL")
	ErrInvalidSegments    = errors.New("invalid segment count")
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
	ErrSizeMismatch       = errors.New("content length does not match original download size")
	ErrProbeRejected      = errors.New("server rejected probe request")
	ErrDuplicateURL       = errors.New("download with this URL already exists")
	ErrFileExcluded       = errors.New("file excluded by filter rules")
)

// DuplicateDownloadError is returned when a new download matches an existing
// one by filename. It carries the existing download and new request data so
// the caller can offer refresh or force-add options.
type DuplicateDownloadError struct {
	Existing   *Download
	NewURL     string
	NewHeaders map[string]string
}

func (e *DuplicateDownloadError) Error() string {
	return fmt.Sprintf("duplicate filename: %s already exists (status: %s)", e.Existing.Filename, e.Existing.Status)
}
