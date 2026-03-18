package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// command is the incoming request from the Chrome extension.
type command struct {
	ID      string           `json:"id,omitempty"` // Request ID for correlation (echo back in response)
	Command string           `json:"command"`
	Data    *json.RawMessage `json:"data,omitempty"`
}

// response is the outgoing response to the Chrome extension.
type response struct {
	ID      string           `json:"id,omitempty"` // Echoed from request
	Command string           `json:"command"`
	Success bool             `json:"success"`
	Error   string           `json:"error,omitempty"`
	Data    *json.RawMessage `json:"data,omitempty"`
}

// relay handles HTTP communication with the Bolt daemon over a Unix socket.
type relay struct {
	client *http.Client
}

func newRelay(socketPath string) *relay {
	return &relay{
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// execute dispatches a command to the daemon and returns a response.
// The request ID is echoed in every response for client-side correlation.
func (r *relay) execute(cmd command) (response, error) {
	switch cmd.Command {
	case "ping":
		return r.doRequest(cmd, http.MethodGet, "/api/stats", nil)
	case "add_download":
		return r.doRequest(cmd, http.MethodPost, "/api/downloads", cmd.Data)
	case "probe":
		return r.doRequest(cmd, http.MethodPost, "/api/probe", cmd.Data)
	default:
		return response{
			ID:      cmd.ID,
			Command: cmd.Command,
			Success: false,
			Error:   "unknown_command",
		}, nil
	}
}

func (r *relay) doRequest(cmd command, method, path string, body *json.RawMessage) (response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(*body)
	}

	req, err := http.NewRequest(method, "http://localhost"+path, bodyReader)
	if err != nil {
		return response{ID: cmd.ID, Command: cmd.Command, Success: false, Error: "internal_error"}, nil
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		errCode := "daemon_unavailable"
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			errCode = "timeout"
		}
		return response{ID: cmd.ID, Command: cmd.Command, Success: false, Error: errCode}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageSize+1))
	if err != nil {
		return response{ID: cmd.ID, Command: cmd.Command, Success: false, Error: "read_error"}, nil
	}
	if len(respBody) > maxMessageSize {
		return response{ID: cmd.ID, Command: cmd.Command, Success: false, Error: "response_too_large"}, nil
	}

	data := json.RawMessage(respBody)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return response{
			ID:      cmd.ID,
			Command: cmd.Command,
			Success: true,
			Data:    &data,
		}, nil
	}

	// Extract error code from daemon response
	var errResp struct {
		Code string `json:"code"`
	}
	json.Unmarshal(respBody, &errResp)

	errCode := "request_failed"
	if errResp.Code != "" {
		errCode = strings.ToLower(errResp.Code)
	}

	return response{
		ID:      cmd.ID,
		Command: cmd.Command,
		Success: false,
		Error:   errCode,
		Data:    &data,
	}, nil
}
