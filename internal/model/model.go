package model

import "time"

type Status string

const (
	StatusQueued    Status = "queued"
	StatusActive    Status = "active"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusError     Status = "error"
	StatusRefresh   Status = "refresh"
	StatusVerifying Status = "verifying"
)

type Download struct {
	ID           string            `json:"id"`
	URL          string            `json:"url"`
	Filename     string            `json:"filename"`
	Dir          string            `json:"dir"`
	TotalSize    int64             `json:"total_size"`
	Downloaded   int64             `json:"downloaded"`
	Status       Status            `json:"status"`
	SegmentCount int               `json:"segments"`
	SpeedLimit   int64             `json:"speed_limit"`
	Headers      map[string]string `json:"headers"`
	RefererURL   string            `json:"referer_url"`
	Checksum     *Checksum         `json:"checksum"`
	Error        string            `json:"error"`
	ETag         string            `json:"etag"`
	LastModified string            `json:"last_modified"`
	CreatedAt    time.Time         `json:"created_at"`
	CompletedAt  *time.Time        `json:"completed_at"`
	QueueOrder   int               `json:"queue_order"`
}

type Checksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type Segment struct {
	DownloadID string `json:"download_id"`
	Index      int    `json:"index"`
	StartByte  int64  `json:"start_byte"`
	EndByte    int64  `json:"end_byte"`
	Downloaded int64  `json:"downloaded"`
	Done       bool   `json:"done"`
}

type ProbeResult struct {
	Filename      string `json:"filename"`
	TotalSize     int64  `json:"total_size"`
	AcceptsRanges bool   `json:"accepts_ranges"`
	ETag          string `json:"etag"`
	LastModified  string `json:"last_modified"`
	FinalURL      string `json:"final_url"`
	ContentType   string `json:"content_type"`
}

type ProgressUpdate struct {
	DownloadID string            `json:"id"`
	Downloaded int64             `json:"downloaded"`
	TotalSize  int64             `json:"total_size"`
	Speed      int64             `json:"speed"`
	ETA        int               `json:"eta"`
	Status     Status            `json:"status"`
	Segments   []SegmentProgress `json:"segments,omitempty"`
}

type SegmentProgress struct {
	Index      int   `json:"index"`
	Downloaded int64 `json:"downloaded"`
	Done       bool  `json:"done"`
}

type AddRequest struct {
	URL        string            `json:"url"`
	Filename   string            `json:"filename"`
	Dir        string            `json:"dir"`
	Segments   int               `json:"segments"`
	Headers    map[string]string `json:"headers"`
	RefererURL string            `json:"referer_url"`
	SpeedLimit int64             `json:"speed_limit"`
	Checksum   *Checksum         `json:"checksum"`
}

type ListFilter struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}
