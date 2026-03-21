package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fhsinchy/bolt/internal/model"
	"golang.org/x/time/rate"
)

type segmentReport struct {
	Index     int
	BytesRead int64
	Done      bool
	Err       error
}

type segmentWorker struct {
	download *model.Download
	segment  *model.Segment
	client   *http.Client
	reportCh chan<- segmentReport
	file     *os.File
	limiter  *rate.Limiter
}

const readBufSize = 32 * 1024

// Run downloads the byte range for this segment. It returns nil on success,
// a non-nil error on failure. Partial progress is reported through reportCh.
func (w *segmentWorker) Run(ctx context.Context) error {
	startByte := w.segment.StartByte + w.segment.Downloaded
	endByte := w.segment.EndByte

	if endByte >= 0 && startByte > endByte {
		w.reportCh <- segmentReport{Index: w.segment.Index, Done: true}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.download.URL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	applyHeaders(req, w.download.Headers)

	// For single-connection mode (no range support), don't set Range header
	if w.download.TotalSize > 0 && w.download.SegmentCount > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", startByte, endByte))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return &httpError{StatusCode: resp.StatusCode}
	}

	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(strings.ToLower(ct), "text/html") {
		fn := strings.ToLower(w.download.Filename)
		if !strings.HasSuffix(fn, ".html") && !strings.HasSuffix(fn, ".htm") {
			return &contentTypeError{ContentType: ct}
		}
	}

	buf := make([]byte, readBufSize)
	var reader io.Reader = resp.Body

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, readErr := reader.Read(buf)
		if n > 0 {
			writeOffset := w.segment.StartByte + w.segment.Downloaded
			if _, writeErr := w.file.WriteAt(buf[:n], writeOffset); writeErr != nil {
				return fmt.Errorf("writing to file: %w", writeErr)
			}
			w.segment.Downloaded += int64(n)
			w.reportCh <- segmentReport{
				Index:     w.segment.Index,
				BytesRead: int64(n),
			}

			if w.limiter != nil {
				if err := w.limiter.WaitN(ctx, n); err != nil {
					return err
				}
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				if w.segment.EndByte < 0 {
					// Unknown-size download: EOF = done
					w.segment.Done = true
					w.reportCh <- segmentReport{Index: w.segment.Index, Done: true}
					return nil
				}
				expectedSize := w.segment.EndByte - w.segment.StartByte + 1
				if w.segment.Downloaded >= expectedSize {
					w.segment.Done = true
					w.reportCh <- segmentReport{Index: w.segment.Index, Done: true}
					return nil
				}
				// EOF before expected — could be server cut short
				return io.ErrUnexpectedEOF
			}
			return readErr
		}

		if w.segment.EndByte >= 0 {
			expectedSize := w.segment.EndByte - w.segment.StartByte + 1
			if w.segment.Downloaded >= expectedSize {
				w.segment.Done = true
				w.reportCh <- segmentReport{Index: w.segment.Index, Done: true}
				return nil
			}
		}
	}
}

// RunWithRetry wraps Run with exponential backoff retry for transient errors.
func (w *segmentWorker) RunWithRetry(ctx context.Context, maxRetries int) {
	if w.segment.Done {
		w.reportCh <- segmentReport{Index: w.segment.Index, Done: true}
		return
	}

	slog.Debug("segment start",
		"id", w.download.ID, "trace", w.download.TraceID,
		"seg", w.segment.Index,
		"range", fmt.Sprintf("%d-%d", w.segment.StartByte, w.segment.EndByte),
	)

	backoff := 1 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := w.Run(ctx)
		if err == nil {
			slog.Debug("segment complete",
				"id", w.download.ID, "trace", w.download.TraceID,
				"seg", w.segment.Index,
				"bytes", w.segment.Downloaded,
			)
			return
		}

		if ctx.Err() != nil {
			return
		}

		if isPermanentError(err) {
			w.reportCh <- segmentReport{Index: w.segment.Index, Err: err}
			return
		}

		slog.Debug("segment retry",
			"id", w.download.ID, "trace", w.download.TraceID,
			"seg", w.segment.Index,
			"attempt", attempt+1,
			"error", err.Error(),
		)

		if attempt < maxRetries {
			select {
			case <-time.After(backoff):
				backoff = min(backoff*2, 60*time.Second)
			case <-ctx.Done():
				return
			}
		}
	}
	w.reportCh <- segmentReport{Index: w.segment.Index, Err: model.ErrMaxRetriesExceeded}
}

type httpError struct {
	StatusCode int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

type contentTypeError struct {
	ContentType string
}

func (e *contentTypeError) Error() string {
	return fmt.Sprintf("server returned HTML (%s) — URL may require authentication or the file does not exist", e.ContentType)
}

func isPermanentError(err error) bool {
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 404, 403, 410, 416:
			return true
		}
		return false
	}

	// Context cancelled / deadline exceeded are not permanent — they're
	// signals from the user (pause/cancel).
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors are transient
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return false
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}

	// io.UnexpectedEOF is transient
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}

	// Connection reset / broken pipe
	if isConnectionReset(err) {
		return false
	}

	var ctErr *contentTypeError
	if errors.As(err, &ctErr) {
		return true
	}

	// Default: treat unknown errors as transient to allow retry
	return false
}

func isConnectionReset(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection refused")
}
