package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbe_Normal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD request, got %s", r.Method)
		}
		w.Header().Set("Content-Length", "1048576")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Disposition", `attachment; filename="testfile.zip"`)
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2024 00:00:00 GMT")
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := ts.Client()
	result, err := Probe(context.Background(), client, ts.URL+"/testfile.zip", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalSize != 1048576 {
		t.Errorf("TotalSize = %d, want 1048576", result.TotalSize)
	}
	if !result.AcceptsRanges {
		t.Error("AcceptsRanges = false, want true")
	}
	if result.Filename != "testfile.zip" {
		t.Errorf("Filename = %q, want %q", result.Filename, "testfile.zip")
	}
	if result.ETag != `"abc123"` {
		t.Errorf("ETag = %q, want %q", result.ETag, `"abc123"`)
	}
	if result.LastModified != "Mon, 01 Jan 2024 00:00:00 GMT" {
		t.Errorf("LastModified = %q, want %q", result.LastModified, "Mon, 01 Jan 2024 00:00:00 GMT")
	}
	if result.ContentType != "application/zip" {
		t.Errorf("ContentType = %q, want %q", result.ContentType, "application/zip")
	}
}

func TestProbe_HeadNotAllowed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodGet {
			// Verify the Range header is present.
			if r.Header.Get("Range") != "bytes=0-0" {
				t.Errorf("expected Range: bytes=0-0, got %q", r.Header.Get("Range"))
			}
			w.Header().Set("Content-Range", "bytes 0-0/2097152")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte{0})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := ts.Client()
	result, err := Probe(context.Background(), client, ts.URL+"/bigfile.bin", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalSize != 2097152 {
		t.Errorf("TotalSize = %d, want 2097152", result.TotalSize)
	}
	if !result.AcceptsRanges {
		t.Error("AcceptsRanges = false, want true")
	}
}

func TestProbe_Redirect(t *testing.T) {
	// Create the final destination server first.
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "512")
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	// Create a redirect server that points to the final server.
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/final-destination", http.StatusFound)
	}))
	defer redirect.Close()

	client := redirect.Client()
	result, err := Probe(context.Background(), client, redirect.URL+"/start", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := final.URL + "/final-destination"
	if result.FinalURL != want {
		t.Errorf("FinalURL = %q, want %q", result.FinalURL, want)
	}
	if result.TotalSize != 512 {
		t.Errorf("TotalSize = %d, want 512", result.TotalSize)
	}
}

func TestProbe_NoContentLength(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately omit Content-Length.
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := ts.Client()
	result, err := Probe(context.Background(), client, ts.URL+"/stream", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalSize != -1 {
		t.Errorf("TotalSize = %d, want -1", result.TotalSize)
	}
	if result.AcceptsRanges {
		t.Error("AcceptsRanges = true, want false")
	}
}

func TestProbe_CustomHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token123" {
			t.Errorf("Authorization header = %q, want %q", r.Header.Get("Authorization"), "Bearer token123")
		}
		if r.Header.Get("Referer") != "https://example.com" {
			t.Errorf("Referer header = %q, want %q", r.Header.Get("Referer"), "https://example.com")
		}
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := ts.Client()
	headers := map[string]string{
		"Authorization": "Bearer token123",
		"Referer":       "https://example.com",
	}
	result, err := Probe(context.Background(), client, ts.URL+"/auth", headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalSize != 100 {
		t.Errorf("TotalSize = %d, want 100", result.TotalSize)
	}
}

func TestProbe_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	client := ts.Client()
	_, err := Probe(context.Background(), client, ts.URL+"/forbidden", nil)
	if err == nil {
		t.Fatal("expected error for 403 response, got nil")
	}
	// HEAD 403 falls back to GET, which also returns 403.
	want := fmt.Sprintf("server rejected probe request: GET %s/forbidden returned 403", ts.URL)
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestProbe_HeadForbiddenFallback(t *testing.T) {
	// Simulates pre-signed S3/R2 URLs that reject HEAD with 403
	// but accept GET normally.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Range", "bytes 0-0/5242880")
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Disposition", `attachment; filename="archive.rar"`)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte{0})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := ts.Client()
	result, err := Probe(context.Background(), client, ts.URL+"/presigned", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalSize != 5242880 {
		t.Errorf("TotalSize = %d, want 5242880", result.TotalSize)
	}
	if !result.AcceptsRanges {
		t.Error("AcceptsRanges = false, want true")
	}
	if result.Filename != "archive.rar" {
		t.Errorf("Filename = %q, want %q", result.Filename, "archive.rar")
	}
}
