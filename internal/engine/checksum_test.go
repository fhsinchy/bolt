package engine

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fhsinchy/bolt/internal/model"
)

func hexDigest(h interface{ Sum([]byte) []byte; Write([]byte) (int, error) }, data []byte) string {
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "testfile.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestVerifyChecksum_NilChecksum(t *testing.T) {
	if err := verifyChecksum("/nonexistent/path", nil); err != nil {
		t.Errorf("expected nil for nil checksum, got %v", err)
	}
}

func TestVerifyChecksum_MD5Correct(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	h := md5.New()
	expected := hexDigest(h, data)
	cs := &model.Checksum{Algorithm: "md5", Value: expected}
	if err := verifyChecksum(path, cs); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestVerifyChecksum_SHA1Correct(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	h := sha1.New()
	expected := hexDigest(h, data)
	cs := &model.Checksum{Algorithm: "sha1", Value: expected}
	if err := verifyChecksum(path, cs); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestVerifyChecksum_SHA256Correct(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	h := sha256.New()
	expected := hexDigest(h, data)
	cs := &model.Checksum{Algorithm: "sha256", Value: expected}
	if err := verifyChecksum(path, cs); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestVerifyChecksum_SHA512Correct(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	h := sha512.New()
	expected := hexDigest(h, data)
	cs := &model.Checksum{Algorithm: "sha512", Value: expected}
	if err := verifyChecksum(path, cs); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestVerifyChecksum_SHA256Mismatch(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	cs := &model.Checksum{Algorithm: "sha256", Value: "0000000000000000000000000000000000000000000000000000000000000000"}
	err := verifyChecksum(path, cs)
	if err == nil {
		t.Error("expected mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected error containing 'mismatch', got %v", err)
	}
}

func TestVerifyChecksum_HyphenatedAlgo(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	h := sha256.New()
	expected := hexDigest(h, data)
	cs := &model.Checksum{Algorithm: "SHA-256", Value: expected}
	if err := verifyChecksum(path, cs); err != nil {
		t.Errorf("expected nil for SHA-256 alias, got %v", err)
	}
}

func TestVerifyChecksum_NoHyphenAlgo(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	h := sha256.New()
	expected := hexDigest(h, data)
	cs := &model.Checksum{Algorithm: "SHA256", Value: expected}
	if err := verifyChecksum(path, cs); err != nil {
		t.Errorf("expected nil for SHA256 alias, got %v", err)
	}
}

func TestVerifyChecksum_UppercaseHexValue(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	h := sha256.New()
	expected := strings.ToUpper(hexDigest(h, data))
	cs := &model.Checksum{Algorithm: "sha256", Value: expected}
	if err := verifyChecksum(path, cs); err != nil {
		t.Errorf("expected nil for uppercase hex, got %v", err)
	}
}

func TestVerifyChecksum_UnsupportedAlgo(t *testing.T) {
	data := []byte("hello world")
	path := writeTempFile(t, data)
	cs := &model.Checksum{Algorithm: "sha3", Value: "abc123"}
	err := verifyChecksum(path, cs)
	if err == nil {
		t.Error("expected error for unsupported algorithm, got nil")
	}
}

func TestVerifyChecksum_NonexistentFile(t *testing.T) {
	cs := &model.Checksum{Algorithm: "sha256", Value: "abc123"}
	err := verifyChecksum("/nonexistent/path/to/file.bin", cs)
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}
