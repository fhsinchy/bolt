package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxConcurrent != 3 {
		t.Errorf("MaxConcurrent = %d, want 3", cfg.MaxConcurrent)
	}
	if cfg.DefaultSegments != 16 {
		t.Errorf("DefaultSegments = %d, want 16", cfg.DefaultSegments)
	}
	if cfg.GlobalSpeedLimit != 0 {
		t.Errorf("GlobalSpeedLimit = %d, want 0", cfg.GlobalSpeedLimit)
	}
	if cfg.Notifications != true {
		t.Error("Notifications = false, want true")
	}
	if cfg.MaxRetries != 10 {
		t.Errorf("MaxRetries = %d, want 10", cfg.MaxRetries)
	}
	if cfg.MinSegmentSize != 1048576 {
		t.Errorf("MinSegmentSize = %d, want 1048576", cfg.MinSegmentSize)
	}
	if cfg.DownloadDir == "" {
		t.Error("DownloadDir is empty")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("DefaultConfig failed validation: %v", err)
	}
}

func TestLoad_NonexistentCreatesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// The file should now exist on disk.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected config file to be created, but it does not exist")
	}

	// Loaded config should pass validation and have sensible defaults.
	if cfg.MaxConcurrent != 3 {
		t.Errorf("MaxConcurrent = %d, want 3", cfg.MaxConcurrent)
	}
	if cfg.DefaultSegments != 16 {
		t.Errorf("DefaultSegments = %d, want 16", cfg.DefaultSegments)
	}
}

func TestLoad_PartialJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write a partial config: only max_concurrent is set.
	partial := map[string]any{
		"max_concurrent": 5,
	}
	data, err := json.Marshal(partial)
	if err != nil {
		t.Fatalf("Marshal partial: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Explicitly set value should be honoured.
	if cfg.MaxConcurrent != 5 {
		t.Errorf("MaxConcurrent = %d, want 5", cfg.MaxConcurrent)
	}

	// Missing fields should fall back to defaults.
	if cfg.DefaultSegments != 16 {
		t.Errorf("DefaultSegments = %d, want 16 (default)", cfg.DefaultSegments)
	}
	if cfg.MinSegmentSize != 1048576 {
		t.Errorf("MinSegmentSize = %d, want 1048576 (default)", cfg.MinSegmentSize)
	}
	if cfg.MaxRetries != 10 {
		t.Errorf("MaxRetries = %d, want 10 (default)", cfg.MaxRetries)
	}
	if cfg.Notifications != true {
		t.Error("Notifications = false, want true (default)")
	}
}

func TestValidate_RejectsOutOfRange(t *testing.T) {
	// Helper that creates a valid config and applies a mutator.
	validConfig := func(mutate func(*Config)) *Config {
		cfg := DefaultConfig()
		mutate(cfg)
		return cfg
	}

	tests := []struct {
		name string
		cfg  *Config
	}{
		{"MaxConcurrent too low", validConfig(func(c *Config) { c.MaxConcurrent = 0 })},
		{"MaxConcurrent too high", validConfig(func(c *Config) { c.MaxConcurrent = 11 })},
		{"DefaultSegments too low", validConfig(func(c *Config) { c.DefaultSegments = 0 })},
		{"DefaultSegments too high", validConfig(func(c *Config) { c.DefaultSegments = 33 })},
		{"MinSegmentSize too small", validConfig(func(c *Config) { c.MinSegmentSize = 100 })},
		{"MaxRetries negative", validConfig(func(c *Config) { c.MaxRetries = -1 })},
		{"MaxRetries too high", validConfig(func(c *Config) { c.MaxRetries = 101 })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

func TestValidate_NegativeMinFileSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinFileSize = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for negative MinFileSize, got nil")
	}
}

func TestValidate_WhitelistEntryWithoutDot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExtensionWhitelist = []string{"zip"} // missing leading dot
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for whitelist entry without '.', got nil")
	}
}

func TestValidate_BlacklistEntryWithoutDot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExtensionBlacklist = []string{"exe"} // missing leading dot
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for blacklist entry without '.', got nil")
	}
}

func TestValidate_ValidConfigWithFilterFields(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinFileSize = 1048576
	cfg.ExtensionWhitelist = []string{".zip", ".PDF"}
	cfg.ExtensionBlacklist = []string{".EXE"}

	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config to pass, got %v", err)
	}

	// Validate should lowercase the extensions.
	if cfg.ExtensionWhitelist[1] != ".pdf" {
		t.Errorf("expected whitelist to be lowercased, got %q", cfg.ExtensionWhitelist[1])
	}
	if cfg.ExtensionBlacklist[0] != ".exe" {
		t.Errorf("expected blacklist to be lowercased, got %q", cfg.ExtensionBlacklist[0])
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := DefaultConfig()
	original.MaxConcurrent = 7
	original.DefaultSegments = 24
	original.MaxRetries = 50
	original.MinSegmentSize = 131072 // 128KB
	original.Notifications = false
	original.GlobalSpeedLimit = 1048576

	if err := original.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.MaxConcurrent != original.MaxConcurrent {
		t.Errorf("MaxConcurrent = %d, want %d", loaded.MaxConcurrent, original.MaxConcurrent)
	}
	if loaded.DefaultSegments != original.DefaultSegments {
		t.Errorf("DefaultSegments = %d, want %d", loaded.DefaultSegments, original.DefaultSegments)
	}
	if loaded.MaxRetries != original.MaxRetries {
		t.Errorf("MaxRetries = %d, want %d", loaded.MaxRetries, original.MaxRetries)
	}
	if loaded.MinSegmentSize != original.MinSegmentSize {
		t.Errorf("MinSegmentSize = %d, want %d", loaded.MinSegmentSize, original.MinSegmentSize)
	}
	if loaded.Notifications != original.Notifications {
		t.Errorf("Notifications = %v, want %v", loaded.Notifications, original.Notifications)
	}
	if loaded.GlobalSpeedLimit != original.GlobalSpeedLimit {
		t.Errorf("GlobalSpeedLimit = %d, want %d", loaded.GlobalSpeedLimit, original.GlobalSpeedLimit)
	}
}
