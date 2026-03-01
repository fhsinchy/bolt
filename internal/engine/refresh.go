package engine

import (
	"context"
	"fmt"

	"github.com/fhsinchy/bolt/internal/model"
)

// RefreshURL performs a Tier 3 (manual) URL refresh. It probes the new URL,
// verifies that the Content-Length matches the original TotalSize, and
// updates the download's URL in the DB.
func (e *Engine) RefreshURL(ctx context.Context, id string, newURL string, headers map[string]string) error {
	dl, err := e.store.GetDownload(ctx, id)
	if err != nil {
		return err
	}

	if dl.Status == model.StatusActive {
		return fmt.Errorf("cannot refresh an active download; pause it first")
	}
	if dl.Status == model.StatusCompleted {
		return model.ErrAlreadyCompleted
	}

	// Use provided headers or fall back to existing download headers
	probeHeaders := dl.Headers
	if headers != nil {
		probeHeaders = headers
	}

	// Probe the new URL
	result, err := Probe(ctx, e.client, newURL, probeHeaders)
	if err != nil {
		return fmt.Errorf("probing new URL: %w", err)
	}

	// Verify size matches if both are known
	if dl.TotalSize > 0 && result.TotalSize > 0 && result.TotalSize != dl.TotalSize {
		return fmt.Errorf("%w: original=%d, new=%d", model.ErrSizeMismatch, dl.TotalSize, result.TotalSize)
	}

	// Update the URL in DB
	if err := e.store.UpdateDownloadURL(ctx, id, result.FinalURL, probeHeaders); err != nil {
		return fmt.Errorf("updating URL: %w", err)
	}

	// If it was in error/refresh state, set to paused so user can resume
	if dl.Status == model.StatusError || dl.Status == model.StatusRefresh {
		if err := e.store.UpdateDownloadStatus(ctx, id, model.StatusPaused, ""); err != nil {
			return fmt.Errorf("updating status: %w", err)
		}
	}

	return nil
}
