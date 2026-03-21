package engine

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/fhsinchy/bolt/internal/model"
)

// normalizeAlgorithm lowercases and strips hyphens so "SHA-256", "sha256",
// and "SHA256" all resolve to "sha256".
func normalizeAlgorithm(algo string) string {
	return strings.ReplaceAll(strings.ToLower(algo), "-", "")
}

// verifyChecksum opens filePath, streams it through the algorithm specified in
// cs, and compares the hex digest against cs.Value (case-insensitive).
// Returns nil immediately if cs is nil.
func verifyChecksum(filePath string, cs *model.Checksum) error {
	if cs == nil {
		return nil
	}

	slog.Debug("checksum verify start", "file", filePath, "algo", cs.Algorithm)

	algo := normalizeAlgorithm(cs.Algorithm)

	var h hash.Hash
	switch algo {
	case "md5":
		h = md5.New()
	case "sha1":
		h = sha1.New()
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return fmt.Errorf("unsupported checksum algorithm: %s", cs.Algorithm)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening file for checksum: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing file: %w", err)
	}

	actual := fmt.Sprintf("%x", h.Sum(nil))
	expected := strings.ToLower(cs.Value)

	if actual != expected {
		return fmt.Errorf("checksum mismatch (%s): got %s, want %s", algo, actual, expected)
	}

	slog.Debug("checksum verify pass", "file", filePath, "algo", cs.Algorithm)

	return nil
}
