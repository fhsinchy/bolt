package engine

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/fhsinchy/bolt/internal/model"
)

// Probe sends a HEAD request (falling back to a partial GET on 403/405) to
// discover metadata about the resource at rawURL. The returned ProbeResult
// contains the total size, range-request support, filename from
// Content-Disposition, ETag, Last-Modified, Content-Type, and the final URL
// after any redirects.
func Probe(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) (*model.ProbeResult, error) {
	slog.Debug("probe start", "url", rawURL)

	result, err := probeHEAD(ctx, client, rawURL, headers)
	if err != nil {
		return nil, err
	}
	if result != nil {
		slog.Debug("probe result",
			"url", rawURL, "method", "HEAD",
			"resumable", result.AcceptsRanges,
			"size", result.TotalSize,
			"filename", result.Filename,
		)
		return result, nil
	}

	// HEAD returned 405 -- fall back to a ranged GET.
	result, err = probeGET(ctx, client, rawURL, headers)
	if err != nil {
		return nil, err
	}
	slog.Debug("probe result",
		"url", rawURL, "method", "GET-fallback",
		"resumable", result.AcceptsRanges,
		"size", result.TotalSize,
		"filename", result.Filename,
	)
	return result, nil
}

// probeHEAD performs a HEAD request. It returns (nil, nil) when the server
// responds with 403, 401, or 405 so that the caller can fall back to a ranged
// GET. Pre-signed URLs (S3, R2) commonly reject HEAD with 403. A 410 returns
// ErrProbeRejected directly to avoid poisoning temporary URLs with a Range GET.
func probeHEAD(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) (*model.ProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HEAD request: %w", err)
	}
	applyHeaders(req, headers)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HEAD request: %w", err)
	}
	defer resp.Body.Close()

	// 410 Gone: server explicitly rejects this resource. Don't fall back
	// to a ranged GET — hostile servers (1fichier) may treat any Range
	// request as poisoning the temporary URL. Return ErrProbeRejected so
	// AddDownload can create a degraded download that uses a plain GET.
	if resp.StatusCode == http.StatusGone {
		return nil, fmt.Errorf("%w: HEAD %s returned %d", model.ErrProbeRejected, rawURL, resp.StatusCode)
	}

	if resp.StatusCode == http.StatusMethodNotAllowed ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusUnauthorized {
		return nil, nil // signal caller to fall back to ranged GET
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: HEAD %s returned %d", model.ErrProbeRejected, rawURL, resp.StatusCode)
	}

	return parseProbeResponse(resp, rawURL), nil
}

// probeGET performs a GET with Range: bytes=0-0 to discover Content-Range
// and other headers when HEAD is not supported.
func probeGET(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) (*model.ProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating GET request: %w", err)
	}
	applyHeaders(req, headers)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET fallback request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: GET %s returned %d", model.ErrProbeRejected, rawURL, resp.StatusCode)
	}

	result := parseProbeResponse(resp, rawURL)

	// When the server honours the range request it returns 206 with a
	// Content-Range header such as "bytes 0-0/12345".
	if resp.StatusCode == http.StatusPartialContent {
		result.AcceptsRanges = true
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if total := parseContentRangeTotal(cr); total > 0 {
				result.TotalSize = total
			}
		}
	}

	return result, nil
}

// parseProbeResponse extracts common metadata from a response.
// originalURL is the user-provided URL before redirects — its path
// segment often has a better filename than the CDN redirect target.
func parseProbeResponse(resp *http.Response, originalURL string) *model.ProbeResult {
	result := &model.ProbeResult{
		TotalSize:    -1,
		FinalURL:     resp.Request.URL.String(),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		ContentType:  resp.Header.Get("Content-Type"),
	}

	// Content-Length
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n > 0 {
			result.TotalSize = n
		}
	}

	// Accept-Ranges
	if ar := resp.Header.Get("Accept-Ranges"); strings.EqualFold(ar, "bytes") {
		result.AcceptsRanges = true
	}

	// Content-Disposition filename (highest priority)
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		result.Filename = parseContentDispositionFilename(cd)
	}

	// Fall back to URL path segment. Try the original URL first — CDN
	// redirect targets often have opaque token paths or stripped extensions.
	if result.Filename == "" {
		result.Filename = filenameFromURL(originalURL)
	}
	if result.Filename == "" {
		result.Filename = filenameFromURL(result.FinalURL)
	}

	return result
}

// parseContentDispositionFilename extracts a filename from a
// Content-Disposition header value. It handles both filename and
// filename* (RFC 5987) parameters.
func parseContentDispositionFilename(cd string) string {
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	// Prefer filename* (RFC 5987 extended parameter) over filename.
	if fn := params["filename"]; fn != "" {
		return fn
	}
	return ""
}

// parseContentRangeTotal extracts the total size from a Content-Range
// header of the form "bytes 0-0/12345". Returns -1 on failure.
func parseContentRangeTotal(cr string) int64 {
	// Expected format: "bytes 0-0/12345" or "bytes */12345"
	idx := strings.LastIndex(cr, "/")
	if idx < 0 || idx+1 >= len(cr) {
		return -1
	}
	total := cr[idx+1:]
	if total == "*" {
		return -1
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil || n <= 0 {
		return -1
	}
	return n
}

// applyHeaders copies user-provided headers onto the request.
func applyHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}
