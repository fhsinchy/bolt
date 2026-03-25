package engine

import (
	"testing"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/model"
)

func TestCheckExclusion_EmptyLists(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := checkExclusion(cfg, "file.zip", 1024); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCheckExclusion_WhitelistAllows(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ExtensionWhitelist = []string{".zip", ".tar.gz"}

	if err := checkExclusion(cfg, "archive.zip", 1024); err != nil {
		t.Errorf("expected .zip to pass whitelist, got %v", err)
	}
}

func TestCheckExclusion_WhitelistBlocks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ExtensionWhitelist = []string{".zip"}

	if err := checkExclusion(cfg, "document.pdf", 1024); err != model.ErrFileExcluded {
		t.Errorf("expected ErrFileExcluded for .pdf with .zip whitelist, got %v", err)
	}
}

func TestCheckExclusion_BlacklistBlocks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ExtensionBlacklist = []string{".exe"}

	if err := checkExclusion(cfg, "setup.exe", 1024); err != model.ErrFileExcluded {
		t.Errorf("expected ErrFileExcluded for .exe, got %v", err)
	}
}

func TestCheckExclusion_BlacklistAllows(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ExtensionBlacklist = []string{".exe"}

	if err := checkExclusion(cfg, "archive.zip", 1024); err != nil {
		t.Errorf("expected .zip to pass blacklist, got %v", err)
	}
}

func TestCheckExclusion_SizeUnderThreshold(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MinFileSize = 1000

	if err := checkExclusion(cfg, "small.txt", 999); err != model.ErrFileExcluded {
		t.Errorf("expected ErrFileExcluded for size 999 < 1000, got %v", err)
	}
}

func TestCheckExclusion_SizeAtThreshold(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MinFileSize = 1000

	if err := checkExclusion(cfg, "exact.txt", 1000); err != nil {
		t.Errorf("expected size 1000 == threshold to pass, got %v", err)
	}
}

func TestCheckExclusion_SizeOverThreshold(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MinFileSize = 1000

	if err := checkExclusion(cfg, "big.bin", 2000); err != nil {
		t.Errorf("expected size 2000 > threshold to pass, got %v", err)
	}
}

func TestCheckExclusion_UnknownSizeSkipsCheck(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MinFileSize = 1000

	// Size 0 (unknown) should skip the size check.
	if err := checkExclusion(cfg, "unknown.bin", 0); err != nil {
		t.Errorf("expected unknown size (0) to skip check, got %v", err)
	}

	// Size -1 (unknown) should also skip.
	if err := checkExclusion(cfg, "unknown.bin", -1); err != nil {
		t.Errorf("expected unknown size (-1) to skip check, got %v", err)
	}
}

func TestCheckExclusion_CompoundExtension(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ExtensionWhitelist = []string{".tar.gz"}

	if err := checkExclusion(cfg, "archive.tar.gz", 1024); err != nil {
		t.Errorf("expected .tar.gz to pass whitelist, got %v", err)
	}

	// .gz alone should not match .tar.gz whitelist
	if err := checkExclusion(cfg, "file.gz", 1024); err != model.ErrFileExcluded {
		t.Errorf("expected .gz to be blocked by .tar.gz whitelist, got %v", err)
	}
}

func TestCheckExclusion_CaseInsensitive(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ExtensionBlacklist = []string{".pdf"}

	if err := checkExclusion(cfg, "DOCUMENT.PDF", 1024); err != model.ErrFileExcluded {
		t.Errorf("expected .PDF to match .pdf blacklist, got %v", err)
	}
}

func TestExtractExtension(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"archive.tar.gz", ".tar.gz"},
		{"archive.tar.bz2", ".tar.bz2"},
		{"archive.tar.xz", ".tar.xz"},
		{"archive.tar.zst", ".tar.zst"},
		{"book.kepub.epub", ".kepub.epub"},
		{"file.zip", ".zip"},
		{"FILE.PDF", ".pdf"},
		{"noext", ""},
		{"dots.in.name.txt", ".txt"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := extractExtension(tt.filename)
			if got != tt.want {
				t.Errorf("extractExtension(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}
