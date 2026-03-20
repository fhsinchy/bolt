package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFilename_UserProvided(t *testing.T) {
	got := DetectFilename("custom.zip", "server.zip", "https://example.com/url.zip")
	if got != "custom.zip" {
		t.Errorf("got %q, want %q", got, "custom.zip")
	}
}

func TestDetectFilename_ProbeFilename(t *testing.T) {
	got := DetectFilename("", "report.pdf", "https://example.com/dl?id=42")
	if got != "report.pdf" {
		t.Errorf("got %q, want %q", got, "report.pdf")
	}
}

func TestDetectFilename_URL(t *testing.T) {
	got := DetectFilename("", "", "https://example.com/files/photo%20album.jpg")
	if got != "photo album.jpg" {
		t.Errorf("got %q, want %q", got, "photo album.jpg")
	}
}

func TestDetectFilename_Fallback(t *testing.T) {
	got := DetectFilename("", "", "")
	if !strings.HasPrefix(got, "download_") {
		t.Errorf("got %q, want prefix %q", got, "download_")
	}
	// "download_" (9 chars) + 10 ULID chars = 19
	if len(got) != 19 {
		t.Errorf("len = %d, want 19", len(got))
	}
}

func TestDetectFilename_Priority(t *testing.T) {
	tests := []struct {
		name    string
		user    string
		cd      string
		url     string
		wantPfx string
	}{
		{
			name: "user wins over all",
			user: "my.file",
			cd:   "cd.file",
			url:  "https://example.com/url.file",
			wantPfx: "my.file",
		},
		{
			name: "probe wins over url",
			user: "",
			cd:   "cd.file",
			url:  "https://example.com/url.file",
			wantPfx: "cd.file",
		},
		{
			name: "url wins over fallback",
			user: "",
			cd:   "",
			url:  "https://example.com/url.file",
			wantPfx: "url.file",
		},
		{
			name: "fallback",
			user: "",
			cd:   "",
			url:  "",
			wantPfx: "download_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectFilename(tt.user, tt.cd, tt.url)
			if !strings.HasPrefix(got, tt.wantPfx) {
				t.Errorf("got %q, want prefix %q", got, tt.wantPfx)
			}
		})
	}
}

func TestDetectFilename_ProbeFilenameUnicode(t *testing.T) {
	// Probe layer already decodes RFC 5987 filename* — DetectFilename
	// receives the decoded result directly.
	got := DetectFilename("", "\u4e2d\u6587\u6587\u4ef6.txt", "")
	want := "\u4e2d\u6587\u6587\u4ef6.txt"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDetectFilename_URLWithQuery(t *testing.T) {
	got := DetectFilename("", "", "https://example.com/files/data.csv?token=abc&v=2")
	if got != "data.csv" {
		t.Errorf("got %q, want %q", got, "data.csv")
	}
}

func TestDetectFilename_URLQueryParamFilename(t *testing.T) {
	// Yandex Disk-style URL: path is a hash, filename is in query param.
	got := DetectFilename("", "",
		"https://storage.yandex.net/rdisk/abc123/EbpU6f89Zmm==?filename=The%20Game%20%5BBASE%5D.nsz&disposition=attachment")
	if got != "The Game [BASE].nsz" {
		t.Errorf("got %q, want %q", got, "The Game [BASE].nsz")
	}
}

func TestDetectFilename_URLQueryParamIgnoredWhenPathHasExtension(t *testing.T) {
	// When the path has a real extension, query params should not override it.
	got := DetectFilename("", "",
		"https://example.com/files/photo.jpg?filename=wrong.png")
	if got != "photo.jpg" {
		t.Errorf("got %q, want %q", got, "photo.jpg")
	}
}

func TestDetectFilename_URLPathNoExtensionNoQueryParam(t *testing.T) {
	// Path segment with no extension and no query param — returns path segment.
	got := DetectFilename("", "", "https://example.com/downloads/abc123")
	if got != "abc123" {
		t.Errorf("got %q, want %q", got, "abc123")
	}
}

func TestDetectFilename_URLLongHashFallback(t *testing.T) {
	// CDN proxy URL with a long hash path and no filename info — should
	// fall through to ULID fallback, not use the truncated hash.
	longHash := strings.Repeat("aB3xQ", 30) // 150 chars, no extension
	got := DetectFilename("", "", "https://proxy.example.com/download/"+longHash+"?sig=abc")
	if !strings.HasPrefix(got, "download_") {
		t.Errorf("got %q, want prefix %q (long hash should not be used as filename)", got, "download_")
	}
}

func TestDeduplicateFilename(t *testing.T) {
	dir := t.TempDir()

	// No collision -- returns original name.
	got := DeduplicateFilename(dir, "unique.txt")
	if got != "unique.txt" {
		t.Errorf("got %q, want %q", got, "unique.txt")
	}

	// Create the file so next call collides.
	if err := os.WriteFile(filepath.Join(dir, "unique.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = DeduplicateFilename(dir, "unique.txt")
	if got != "unique(1).txt" {
		t.Errorf("got %q, want %q", got, "unique(1).txt")
	}

	// Create unique(1).txt too.
	if err := os.WriteFile(filepath.Join(dir, "unique(1).txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = DeduplicateFilename(dir, "unique.txt")
	if got != "unique(2).txt" {
		t.Errorf("got %q, want %q", got, "unique(2).txt")
	}
}

func TestDeduplicateFilename_DoubleExtension(t *testing.T) {
	dir := t.TempDir()

	// Create archive.tar.gz so next call must deduplicate.
	if err := os.WriteFile(filepath.Join(dir, "archive.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := DeduplicateFilename(dir, "archive.tar.gz")
	if got != "archive(1).tar.gz" {
		t.Errorf("got %q, want %q", got, "archive(1).tar.gz")
	}

	// Create archive(1).tar.gz too.
	if err := os.WriteFile(filepath.Join(dir, "archive(1).tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = DeduplicateFilename(dir, "archive.tar.gz")
	if got != "archive(2).tar.gz" {
		t.Errorf("got %q, want %q", got, "archive(2).tar.gz")
	}
}

func TestDeduplicateFilename_NoExtension(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := DeduplicateFilename(dir, "README")
	if got != "README(1)" {
		t.Errorf("got %q, want %q", got, "README(1)")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal filename",
			input: "file.txt",
			want:  "file.txt",
		},
		{
			name:  "path separators replaced",
			input: "path/to\\file.txt",
			want:  "path_to_file.txt",
		},
		{
			name:  "null bytes removed",
			input: "file\x00name.txt",
			want:  "filename.txt",
		},
		{
			name:  "leading dots stripped",
			input: "...hidden",
			want:  "hidden",
		},
		{
			name:  "whitespace trimmed",
			input: "  file.txt  ",
			want:  "file.txt",
		},
		{
			name:  "empty after sanitization",
			input: "...",
			want:  "download",
		},
		{
			name:  "long filename capped",
			input: strings.Repeat("a", 300),
			want:  strings.Repeat("a", 255),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
