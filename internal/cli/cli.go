package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"nhooyr.io/websocket"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/model"
)

// Client is an HTTP client that talks to the bolt daemon.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient creates a new Client by loading config for port and token.
func NewClient() (*Client, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	return &Client{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.ServerPort),
		token:   cfg.AuthToken,
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// CheckDaemon verifies the daemon is running by hitting /api/stats.
func (c *Client) CheckDaemon() error {
	_, err := c.get(context.Background(), "/api/stats")
	if err != nil {
		return fmt.Errorf("daemon not running (is 'bolt start' running?): %w", err)
	}
	return nil
}

// Add adds a download via the daemon API and optionally shows progress.
func (c *Client) Add(ctx context.Context, opts AddOptions) error {
	req := model.AddRequest{
		URL:      opts.URL,
		Filename: opts.Filename,
		Dir:      opts.Dir,
		Segments: opts.Segments,
		Headers:  opts.Headers,
	}

	if opts.Checksum != "" {
		parts := strings.SplitN(opts.Checksum, ":", 2)
		if len(parts) == 2 {
			req.Checksum = &model.Checksum{
				Algorithm: parts[0],
				Value:     parts[1],
			}
		}
	}

	if opts.Referer != "" {
		if req.Headers == nil {
			req.Headers = make(map[string]string)
		}
		req.Headers["Referer"] = opts.Referer
	}

	resp, err := c.post(ctx, "/api/downloads", req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return readError(resp)
	}

	var result struct {
		Download model.Download `json:"download"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	dl := result.Download

	if opts.JSON {
		return json.NewEncoder(os.Stdout).Encode(dl)
	}

	fmt.Printf("Added: %s\n", dl.Filename)
	fmt.Printf("  ID:       %s\n", dl.ID)
	fmt.Printf("  Size:     %s\n", model.FormatBytes(dl.TotalSize))
	fmt.Printf("  Segments: %d\n", dl.SegmentCount)
	fmt.Printf("  Dir:      %s\n", dl.Dir)
	fmt.Println()

	// Connect to WebSocket and show progress for this download.
	return c.watchProgress(ctx, dl.ID, dl.Filename)
}

// List shows downloads matching the filter.
func (c *Client) List(ctx context.Context, opts ListOptions) error {
	path := "/api/downloads"
	if opts.Status != "" {
		path += "?status=" + url.QueryEscape(opts.Status)
	}

	resp, err := c.get(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	var result struct {
		Downloads []model.Download `json:"downloads"`
		Total     int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if opts.JSON {
		return json.NewEncoder(os.Stdout).Encode(result.Downloads)
	}

	if len(result.Downloads) == 0 {
		fmt.Println("No downloads found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tFILENAME\tSIZE\tPROGRESS\tSTATUS")
	for _, dl := range result.Downloads {
		filename := dl.Filename
		if len(filename) > 30 {
			filename = filename[:27] + "..."
		}

		var progress string
		if dl.TotalSize > 0 {
			pct := float64(dl.Downloaded) / float64(dl.TotalSize) * 100
			progress = fmt.Sprintf("%.0f%%", pct)
		} else {
			progress = model.FormatBytes(dl.Downloaded)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			dl.ID, filename, model.FormatBytes(dl.TotalSize), progress, dl.Status)
	}
	w.Flush()
	return nil
}

// Status shows detailed info for a single download.
func (c *Client) Status(ctx context.Context, id string) error {
	resp, err := c.get(ctx, "/api/downloads/"+id)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	var result struct {
		Download model.Download  `json:"download"`
		Segments []model.Segment `json:"segments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	dl := result.Download
	fmt.Printf("ID:           %s\n", dl.ID)
	fmt.Printf("URL:          %s\n", dl.URL)
	fmt.Printf("Filename:     %s\n", dl.Filename)
	fmt.Printf("Directory:    %s\n", dl.Dir)
	fmt.Printf("Size:         %s\n", model.FormatBytes(dl.TotalSize))
	fmt.Printf("Downloaded:   %s\n", model.FormatBytes(dl.Downloaded))
	fmt.Printf("Status:       %s\n", dl.Status)
	fmt.Printf("Segments:     %d\n", dl.SegmentCount)
	fmt.Printf("Created:      %s\n", dl.CreatedAt.Format("2006-01-02 15:04:05"))
	if dl.CompletedAt != nil {
		fmt.Printf("Completed:    %s\n", dl.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	if dl.Error != "" {
		fmt.Printf("Error:        %s\n", dl.Error)
	}

	if len(result.Segments) > 0 {
		fmt.Println("\nSegments:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  #\tRANGE\tDOWNLOADED\tDONE")
		for _, seg := range result.Segments {
			size := seg.EndByte - seg.StartByte + 1
			var pct string
			if size > 0 {
				pct = fmt.Sprintf("%.0f%%", float64(seg.Downloaded)/float64(size)*100)
			}
			doneStr := "no"
			if seg.Done {
				doneStr = "yes"
			}
			fmt.Fprintf(w, "  %d\t%s-%s\t%s (%s)\t%s\n",
				seg.Index,
				model.FormatBytes(seg.StartByte),
				model.FormatBytes(seg.EndByte),
				model.FormatBytes(seg.Downloaded),
				pct,
				doneStr)
		}
		w.Flush()
	}

	return nil
}

// Pause pauses a download.
func (c *Client) Pause(ctx context.Context, id string) error {
	resp, err := c.post(ctx, "/api/downloads/"+id+"/pause", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	fmt.Printf("Paused: %s\n", id)
	return nil
}

// Resume resumes a download and optionally shows progress.
func (c *Client) Resume(ctx context.Context, id string, showProgress bool) error {
	resp, err := c.post(ctx, "/api/downloads/"+id+"/resume", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	if !showProgress {
		fmt.Printf("Resumed: %s\n", id)
		return nil
	}

	// Get download info for filename.
	dlResp, err := c.get(ctx, "/api/downloads/"+id)
	if err != nil {
		fmt.Printf("Resumed: %s\n", id)
		return nil
	}
	defer dlResp.Body.Close()

	var result struct {
		Download model.Download `json:"download"`
	}
	json.NewDecoder(dlResp.Body).Decode(&result)

	fmt.Printf("Resumed: %s\n", id)
	return c.watchProgress(ctx, id, result.Download.Filename)
}

// Cancel cancels a download.
func (c *Client) Cancel(ctx context.Context, id string, deleteFile bool) error {
	path := "/api/downloads/" + id
	if deleteFile {
		path += "?delete_file=true"
	}

	resp, err := c.del(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	fmt.Printf("Cancelled: %s\n", id)
	if deleteFile {
		fmt.Println("  File deleted.")
	}
	return nil
}

// Refresh refreshes the URL for a failed download.
func (c *Client) Refresh(ctx context.Context, id, newURL string) error {
	body := map[string]string{"url": newURL}
	resp, err := c.post(ctx, "/api/downloads/"+id+"/refresh", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}

	fmt.Printf("URL refreshed for %s. Use 'bolt resume %s' to continue.\n", id, id)
	return nil
}

// ShowWindow asks the running daemon to raise its GUI window.
// Returns true if the daemon is running in headless mode (no GUI to raise).
func (c *Client) ShowWindow(ctx context.Context) (headless bool, err error) {
	resp, err := c.post(ctx, "/api/window/show", nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, readError(resp)
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, nil
	}
	return result.Status == "headless", nil
}

// watchProgress connects to the WebSocket and displays progress for a download.
func (c *Client) watchProgress(ctx context.Context, downloadID, filename string) error {
	wsURL := fmt.Sprintf("ws%s/ws?token=%s",
		c.baseURL[len("http"):], // "://127.0.0.1:9683"
		url.QueryEscape(c.token),
	)

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil // WebSocket is optional; don't fail the command.
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil
		}

		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		msgID, _ := msg["download_id"].(string)
		if msgID != downloadID {
			continue
		}

		switch msg["type"] {
		case "progress":
			downloaded, _ := msg["downloaded"].(float64)
			totalSize, _ := msg["total_size"].(float64)
			speed, _ := msg["speed"].(float64)
			eta, _ := msg["eta"].(float64)
			status, _ := msg["status"].(string)

			p := progressEvent{
				Downloaded: int64(downloaded),
				TotalSize:  int64(totalSize),
				Speed:      int64(speed),
				ETA:        int(eta),
				Status:     status,
			}
			fmt.Print(formatProgressBar(p, filename))

		case "download_completed":
			fmt.Print(formatCompleted(filename))
			return nil

		case "download_failed":
			errMsg, _ := msg["error"].(string)
			fmt.Print(formatFailed(downloadID, errMsg))
			return fmt.Errorf("download failed: %s", errMsg)

		case "download_paused":
			fmt.Printf("\nDownload %s paused\n", downloadID[:12])
			return nil
		}
	}
}

// HTTP helpers

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.http.Do(req)
}

func (c *Client) post(ctx context.Context, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return c.http.Do(req)
}

func (c *Client) put(ctx context.Context, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return c.http.Do(req)
}

func (c *Client) del(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.http.Do(req)
}

// readError extracts an error message from a non-OK HTTP response.
func readError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("%s: %s", body.Code, body.Error)
}

// AddOptions holds flags for the add command.
type AddOptions struct {
	URL      string
	Dir      string
	Filename string
	Segments int
	Headers  map[string]string
	Referer  string
	Checksum string // "algorithm:value" (e.g. "sha256:abc123...")
	JSON     bool
}

// ListOptions holds flags for the list command.
type ListOptions struct {
	Status string
	JSON   bool
}
