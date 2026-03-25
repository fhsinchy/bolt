package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds all user-configurable settings for the Bolt download manager.
type Config struct {
	DownloadDir      string `json:"download_dir"`
	MaxConcurrent    int    `json:"max_concurrent"`
	DefaultSegments  int    `json:"default_segments"`
	GlobalSpeedLimit   int64    `json:"global_speed_limit"`
	Notifications      bool     `json:"notifications"`
	MaxRetries         int      `json:"max_retries"`
	MinSegmentSize     int64    `json:"min_segment_size"`
	MinFileSize        int64    `json:"min_file_size"`
	ExtensionWhitelist []string `json:"extension_whitelist"`
	ExtensionBlacklist []string `json:"extension_blacklist"`
}

// Dir returns the Bolt configuration directory, creating it if it does not
// exist. The directory is located under the OS user config directory.
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = filepath.Join(defaultDownloadDir(), ".config")
	}
	dir := filepath.Join(base, "bolt")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// DefaultPath returns the default path for the configuration file.
func DefaultPath() string {
	return filepath.Join(Dir(), "config.json")
}

// DefaultConfig returns a Config populated with sensible default values.
func DefaultConfig() *Config {
	return &Config{
		DownloadDir:      defaultDownloadDir(),
		MaxConcurrent:    3,
		DefaultSegments:  16,
		GlobalSpeedLimit: 0,
		Notifications:    true,
		MaxRetries:       10,
		MinSegmentSize:   1048576, // 1 MB
	}
}

// Load reads a configuration file from path. If the file does not exist, it
// creates a new file with default values. Fields absent from the JSON are
// filled from DefaultConfig. The loaded configuration is validated before
// being returned.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		// File does not exist: create with defaults.
		cfg := DefaultConfig()
		if saveErr := cfg.Save(path); saveErr != nil {
			return nil, fmt.Errorf("creating default config: %w", saveErr)
		}
		return cfg, nil
	}

	// Start from defaults so that missing keys keep their default value.
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// Save writes the configuration to path as pretty-printed JSON. Parent
// directories are created automatically if they do not exist.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Write to temp file + rename for atomic save.
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("setting config permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming config: %w", err)
	}

	return nil
}

// Validate checks that configuration values are within acceptable ranges.
func (c *Config) Validate() error {
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 10 {
		return fmt.Errorf("max_concurrent must be between 1 and 10, got %d", c.MaxConcurrent)
	}
	if c.DefaultSegments < 1 || c.DefaultSegments > 32 {
		return fmt.Errorf("default_segments must be between 1 and 32, got %d", c.DefaultSegments)
	}
	if c.MinSegmentSize < 65536 {
		return fmt.Errorf("min_segment_size must be at least 65536 (64KB), got %d", c.MinSegmentSize)
	}
	if c.MaxRetries < 0 || c.MaxRetries > 100 {
		return fmt.Errorf("max_retries must be between 0 and 100, got %d", c.MaxRetries)
	}
	if c.MinFileSize < 0 {
		return fmt.Errorf("min_file_size must be non-negative, got %d", c.MinFileSize)
	}
	for i, ext := range c.ExtensionWhitelist {
		if !strings.HasPrefix(ext, ".") {
			return fmt.Errorf("extension_whitelist[%d] must start with '.', got %q", i, ext)
		}
		c.ExtensionWhitelist[i] = strings.ToLower(ext)
	}
	for i, ext := range c.ExtensionBlacklist {
		if !strings.HasPrefix(ext, ".") {
			return fmt.Errorf("extension_blacklist[%d] must start with '.', got %q", i, ext)
		}
		c.ExtensionBlacklist[i] = strings.ToLower(ext)
	}
	return nil
}

// defaultDownloadDir returns the user's default download directory.
func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Downloads")
}
