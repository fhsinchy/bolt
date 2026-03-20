package engine

import (
	"fmt"
	"mime"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"crypto/rand"

	"github.com/oklog/ulid/v2"
)

// DetectFilename determines the best filename for a download using the
// following priority order:
//
//  1. userProvided -- explicitly given by the user.
//  2. probeFilename -- already-extracted filename from the probe response
//     (Content-Disposition or URL path, parsed by the probe layer).
//  3. finalURL -- the last path segment of the URL after redirects.
//  4. fallback -- "download_" + first 10 characters of a ULID.
func DetectFilename(userProvided, probeFilename, finalURL string) string {
	// 1. User-provided name takes the highest priority.
	if name := strings.TrimSpace(userProvided); name != "" {
		return sanitizeFilename(name)
	}

	// 2. Probe-extracted filename (already parsed from Content-Disposition
	// or URL by the probe layer — use directly, don't re-parse).
	if name := strings.TrimSpace(probeFilename); name != "" {
		return sanitizeFilename(name)
	}

	// 3. URL path.
	if finalURL != "" {
		if name := filenameFromURL(finalURL); name != "" {
			return sanitizeFilename(name)
		}
	}

	// 4. Fallback.
	id := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)
	return "download_" + id.String()[:10]
}

// filenameFromContentDisposition parses a Content-Disposition header value
// and extracts the filename. It handles both the standard filename parameter
// and the filename* extended parameter defined in RFC 5987.
func filenameFromContentDisposition(cd string) string {
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	// mime.ParseMediaType automatically decodes filename* (RFC 5987) and
	// places the result in params["filename"].
	if fn := params["filename"]; fn != "" {
		return fn
	}
	return ""
}

// filenameFromURL extracts the best filename from a URL. It first checks the
// last path segment; if that segment lacks a file extension (no dot), it looks
// for well-known query parameters (filename, file, name, fname) that many file
// hosting services use to carry the real filename.
func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	// Try the URL path segment.
	pathName := ""
	if seg := path.Base(u.Path); seg != "" && seg != "." && seg != "/" {
		decoded, err := url.PathUnescape(seg)
		if err == nil {
			pathName = decoded
		} else {
			pathName = seg
		}
	}

	// If the path segment has a file extension, use it directly.
	if pathName != "" && strings.ContainsRune(pathName, '.') {
		return pathName
	}

	// Check query parameters for filename hints (common in Yandex Disk,
	// Google Drive, and other file hosting services).
	for _, param := range []string{"filename", "file", "name", "fname"} {
		if val := u.Query().Get(param); val != "" {
			return val
		}
	}

	// If the path segment is very long and has no extension, it's almost
	// certainly a hash/token (CDN proxies, pre-signed URLs), not a real
	// filename. Return empty so the caller falls through to a clean
	// fallback like "download_XXXX" instead of a truncated hash.
	if len(pathName) > 80 {
		return ""
	}

	// Fall back to the path segment even without an extension.
	return pathName
}

// DeduplicateFilename ensures that the returned filename does not collide
// with an existing file in dir. If dir/filename already exists it appends a
// numeric suffix: name(1).ext, name(2).ext, etc. up to 999.
//
// For double extensions such as archive.tar.gz the suffix is inserted before
// the compound extension: archive(1).tar.gz.
func DeduplicateFilename(dir, filename string) string {
	target := path.Join(dir, filename)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return filename
	}

	base, ext := splitFilename(filename)

	for i := 1; i <= 999; i++ {
		candidate := fmt.Sprintf("%s(%d)%s", base, i, ext)
		target = path.Join(dir, candidate)
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return candidate
		}
	}

	// Extremely unlikely -- just return the original.
	return filename
}

// splitFilename splits a filename into its base name and extension(s). It
// recognises common double extensions such as .tar.gz, .tar.bz2, .tar.xz,
// and .tar.zst.
func splitFilename(name string) (base, ext string) {
	doubleExts := []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tar.lz", ".tar.lz4", ".tar.br"}
	lower := strings.ToLower(name)
	for _, de := range doubleExts {
		if strings.HasSuffix(lower, de) {
			cut := len(name) - len(de)
			return name[:cut], name[cut:]
		}
	}

	// Single extension: find the last dot.
	dotIdx := strings.LastIndex(name, ".")
	if dotIdx <= 0 {
		return name, ""
	}
	return name[:dotIdx], name[dotIdx:]
}

// sanitizeFilename removes or replaces characters that are unsafe or
// undesirable in filenames across operating systems.
func sanitizeFilename(name string) string {
	// Strip null bytes.
	name = strings.ReplaceAll(name, "\x00", "")

	// Replace path separators with underscores.
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	// Strip leading dots to prevent hidden files on Unix-like systems.
	name = strings.TrimLeft(name, ".")

	// Strip leading and trailing whitespace.
	name = strings.TrimSpace(name)

	// Cap length at 255 characters (common filesystem limit).
	if len(name) > 255 {
		name = name[:255]
	}

	if name == "" {
		return "download"
	}

	return name
}
