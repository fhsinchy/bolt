package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

const maxMessageSize = 1024 * 1024 // 1 MB Chrome native messaging limit

// readMessage reads a Chrome native messaging length-prefixed JSON message.
func readMessage(r io.Reader) ([]byte, error) {
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return nil, fmt.Errorf("reading message length: %w", err)
	}
	if length > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", length, maxMessageSize)
	}
	msg := make([]byte, length)
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, fmt.Errorf("reading message body: %w", err)
	}
	return msg, nil
}

// writeMessage writes a Chrome native messaging length-prefixed JSON message.
func writeMessage(w io.Writer, msg []byte) error {
	if len(msg) > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max %d)", len(msg), maxMessageSize)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(msg))); err != nil {
		return fmt.Errorf("writing message length: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("writing message body: %w", err)
	}
	return nil
}
