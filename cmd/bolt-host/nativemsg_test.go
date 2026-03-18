package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestReadMessage(t *testing.T) {
	payload := []byte(`{"command":"ping"}`)
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint32(len(payload)))
	buf.Write(payload)

	msg, err := readMessage(buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if !bytes.Equal(msg, payload) {
		t.Fatalf("got %q, want %q", msg, payload)
	}
}

func TestReadMessageEOF(t *testing.T) {
	buf := new(bytes.Buffer)
	_, err := readMessage(buf)
	if err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestWriteMessage(t *testing.T) {
	payload := []byte(`{"command":"ping","success":true}`)
	buf := new(bytes.Buffer)

	err := writeMessage(buf, payload)
	if err != nil {
		t.Fatalf("writeMessage: %v", err)
	}

	// Read back length prefix
	var length uint32
	binary.Read(buf, binary.LittleEndian, &length)
	if int(length) != len(payload) {
		t.Fatalf("length prefix: got %d, want %d", length, len(payload))
	}

	got := buf.Bytes()
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestWriteMessageTooLarge(t *testing.T) {
	// Chrome native messaging has a 1 MB limit
	large := make([]byte, 1024*1024+1)
	buf := new(bytes.Buffer)
	err := writeMessage(buf, large)
	if err == nil {
		t.Fatal("expected error for message exceeding 1 MB")
	}
}

func TestRoundTrip(t *testing.T) {
	original := map[string]any{"command": "ping"}
	data, _ := json.Marshal(original)

	buf := new(bytes.Buffer)
	writeMessage(buf, data)

	got, err := readMessage(buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}

	var decoded map[string]any
	json.Unmarshal(got, &decoded)
	if decoded["command"] != "ping" {
		t.Fatalf("round-trip failed: %v", decoded)
	}
}
