package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds all user-configurable settings for the Bolt download manager.
type Config struct {
	DownloadDir      string              `json:"download_dir"`
	Categorize       bool                `json:"categorize"`
	MaxConcurrent    int                 `json:"max_concurrent"`
	DefaultSegments  int                 `json:"default_segments"`
	GlobalSpeedLimit int64               `json:"global_speed_limit"`
	ServerPort       int                 `json:"server_port"`
	AuthToken        string              `json:"auth_token"`
	MinimizeToTray   bool                `json:"minimize_to_tray"`
	Theme            string              `json:"theme"`
	Proxy            string              `json:"proxy"`
	MaxRetries       int                 `json:"max_retries"`
	MinSegmentSize   int64               `json:"min_segment_size"`
	Categories       map[string][]string `json:"categories"`
}

// Dir returns the Bolt configuration directory, creating it if it does not
// exist. The directory is located under the OS user config directory.
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = filepath.Join(defaultDownloadDir(), ".config")
	}
	dir := filepath.Join(base, "bolt")
	_ = os.MkdirAll(dir, 0o755)
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
		Categorize:       false,
		MaxConcurrent:    3,
		DefaultSegments:  16,
		GlobalSpeedLimit: 0,
		ServerPort:       9683,
		AuthToken:        generateToken(),
		MinimizeToTray:   true,
		Theme:            "system",
		Proxy:            "",
		MaxRetries:       10,
		MinSegmentSize:   1048576, // 1 MB
		Categories: map[string][]string{
			"Documents":   {".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt", ".odt", ".ods", ".odp"},
			"Images":      {".jpg", ".jpeg", ".png", ".gif", ".bmp", ".svg", ".webp", ".ico", ".tiff"},
			"Audio":       {".mp3", ".flac", ".wav", ".aac", ".ogg", ".wma", ".m4a"},
			"Video":       {".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v"},
			"Archives":    {".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar", ".zst"},
			"Programs":    {".exe", ".msi", ".dmg", ".deb", ".rpm", ".appimage", ".snap", ".flatpak"},
			"Torrents":    {".torrent"},
			"ISO":         {".iso", ".img"},
		},
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
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
	if c.ServerPort < 1024 || c.ServerPort > 65535 {
		return fmt.Errorf("server_port must be between 1024 and 65535, got %d", c.ServerPort)
	}
	if c.AuthToken == "" || len(c.AuthToken) < 16 {
		return fmt.Errorf("auth_token must be at least 16 characters, got %d", len(c.AuthToken))
	}
	if c.MinSegmentSize < 65536 {
		return fmt.Errorf("min_segment_size must be at least 65536 (64KB), got %d", c.MinSegmentSize)
	}
	if c.MaxRetries < 0 || c.MaxRetries > 100 {
		return fmt.Errorf("max_retries must be between 0 and 100, got %d", c.MaxRetries)
	}
	return nil
}

// generateToken returns a cryptographically random token encoded as a
// 64-character hex string (32 random bytes).
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// defaultDownloadDir returns the user's default download directory.
func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Downloads")
}
