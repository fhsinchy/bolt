package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

// host manages the native messaging command loop.
type host struct {
	relay  *relay
	stdout io.Writer
	mu     *sync.Mutex
	logger *logger
}

// run reads commands from r until EOF, relays each to the daemon, and writes
// responses to stdout. This is the main command handler goroutine.
func (h *host) run(r io.Reader) {
	for {
		msg, err := readMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Extension disconnected — exit cleanly.
				return
			}
			// Truncated input, corrupt length prefix, oversized message, etc.
			h.logger.Log("", "fatal read error: %v", err)
			os.Exit(1)
		}

		var cmd command
		if err := json.Unmarshal(msg, &cmd); err != nil {
			h.writeResponse(response{
				Command: "",
				Success: false,
				Error:   "invalid_json",
			})
			continue
		}

		traceID := extractTraceID(cmd.Data)

		// Log command received
		if cmd.Data != nil {
			var urlData struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(*cmd.Data, &urlData)
			h.logger.Log(traceID, "command=%s url=%s", cmd.Command, urlData.URL)
		} else {
			h.logger.Log(traceID, "command=%s", cmd.Command)
		}

		resp, statusCode, err := h.relay.execute(cmd)
		if err != nil {
			h.logger.Log(traceID, "relay error: %v", err)
			resp = response{
				Command: cmd.Command,
				Success: false,
				Error:   "internal_error",
			}
		} else {
			method, path := commandRoute(cmd.Command)
			if resp.Success {
				h.logger.Log(traceID, "%s %s -> %d", method, path, statusCode)
			} else {
				h.logger.Log(traceID, "%s %s -> %d error: %s", method, path, statusCode, resp.Error)
			}
		}

		h.writeResponse(resp)
	}
}

func (h *host) writeResponse(resp response) {
	data, err := json.Marshal(resp)
	if err != nil {
		h.logger.Log("", "marshal error: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := writeMessage(h.stdout, data); err != nil {
		h.logger.Log("", "write error: %v", err)
	}
}
