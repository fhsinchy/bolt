package cli

import (
	"fmt"
	"strings"

	"github.com/fhsinchy/bolt/internal/model"
)

// progressEvent holds progress data extracted from WebSocket messages.
type progressEvent struct {
	Downloaded int64
	TotalSize  int64
	Speed      int64
	ETA        int
	Status     string
}

func formatProgressBar(p progressEvent, filename string) string {
	var pct float64
	var pctStr string
	if p.TotalSize > 0 {
		pct = float64(p.Downloaded) / float64(p.TotalSize) * 100
		pctStr = fmt.Sprintf("%.0f%%", pct)
	} else {
		pctStr = "??%"
	}

	// Progress bar: 20 chars wide
	barWidth := 20
	filled := int(pct / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	sizeStr := fmt.Sprintf("%s/%s",
		model.FormatBytes(p.Downloaded),
		model.FormatBytes(p.TotalSize),
	)

	speedStr := model.FormatSpeed(p.Speed)
	etaStr := model.FormatETA(p.ETA)

	// Truncate filename to 25 chars
	if len(filename) > 25 {
		filename = filename[:22] + "..."
	}

	return fmt.Sprintf("\r%-25s [%s] %4s | %s | %s | ETA %s",
		filename, bar, pctStr, sizeStr, speedStr, etaStr)
}

func formatCompleted(filename string) string {
	return fmt.Sprintf("\n%-25s  Completed\n", filename)
}

func formatFailed(downloadID, errMsg string) string {
	return fmt.Sprintf("\nDownload %s failed: %s\n", downloadID, errMsg)
}
