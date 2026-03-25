package engine

import (
	"path/filepath"
	"strings"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/model"
)

// compoundExtensions lists multi-part extensions that should be matched as a
// unit rather than using only the final dot-delimited suffix.
var compoundExtensions = []string{
	".tar.gz",
	".tar.bz2",
	".tar.xz",
	".tar.zst",
	".kepub.epub",
}

// extractExtension returns the file extension from filename, handling compound
// extensions like .tar.gz. The returned extension is always lowercase.
func extractExtension(filename string) string {
	lower := strings.ToLower(filename)
	for _, ce := range compoundExtensions {
		if strings.HasSuffix(lower, ce) {
			return ce
		}
	}
	return strings.ToLower(filepath.Ext(filename))
}

// checkExclusion tests whether a file should be rejected based on the
// configured extension whitelist/blacklist and minimum file size. It returns
// model.ErrFileExcluded when the file does not pass the filter rules.
func checkExclusion(cfg *config.Config, filename string, totalSize int64) error {
	ext := extractExtension(filename)

	// Whitelist check: if a whitelist is configured, only listed extensions
	// are allowed through.
	if len(cfg.ExtensionWhitelist) > 0 {
		found := false
		for _, allowed := range cfg.ExtensionWhitelist {
			if ext == allowed {
				found = true
				break
			}
		}
		if !found {
			return model.ErrFileExcluded
		}
	}

	// Blacklist check.
	for _, blocked := range cfg.ExtensionBlacklist {
		if ext == blocked {
			return model.ErrFileExcluded
		}
	}

	// Size check: skip when size is unknown (0 or negative).
	if cfg.MinFileSize > 0 && totalSize > 0 && totalSize < cfg.MinFileSize {
		return model.ErrFileExcluded
	}

	return nil
}
