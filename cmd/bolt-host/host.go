package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"sync"
)

// host manages the native messaging command loop.
type host struct {
	relay  *relay
	stdout io.Writer
	mu     *sync.Mutex
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
			log.Printf("fatal read error: %v", err)
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

		resp, err := h.relay.execute(cmd)
		if err != nil {
			log.Printf("relay error: %v", err)
			resp = response{
				Command: cmd.Command,
				Success: false,
				Error:   "internal_error",
			}
		}

		h.writeResponse(resp)
	}
}

func (h *host) writeResponse(resp response) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := writeMessage(h.stdout, data); err != nil {
		log.Printf("write error: %v", err)
	}
}
