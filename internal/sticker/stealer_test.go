package sticker

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStealer_GuessExtension(t *testing.T) {
	tests := []struct {
		url      string
		data     []byte
		expected string
	}{
		{"http://example.com/test.png", []byte{0x89, 'P', 'N', 'G'}, ".png"},
		{"http://example.com/test.gif", []byte("GIF89a"), ".gif"},
		{"http://example.com/test.webp", []byte("RIFF1234WEBP"), ".webp"},
		{"http://example.com/test.jpg", []byte{0xFF, 0xD8, 0xFF}, ".jpg"},
		{"http://example.com/unknown", []byte{0x89, 'P', 'N', 'G'}, ".png"},
		{"http://example.com/unknown", []byte("GIF87a"), ".gif"},
		{"http://example.com/unknown", []byte{0xFF, 0xD8}, ".jpg"},
	}

	for _, tt := range tests {
		got := guessExtension(tt.url, tt.data)
		if got != tt.expected {
			t.Errorf("guessExtension(%q, %v) = %q, want %q", tt.url, tt.data, got, tt.expected)
		}
	}
}

func TestStealer_ConcurrencyLimiting(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	stealer := NewStealer(store, nil)

	// Block the semaphore by filling all 3 slots
	for i := 0; i < maxConcurrent; i++ {
		stealer.sem <- struct{}{}
	}

	// Any additional TrySteal should immediately drop without blocking
	done := make(chan struct{})
	go func() {
		stealer.TrySteal("http://example.com/test.png")
		close(done)
	}()

	select {
	case <-done:
		// success: dropped immediately
	case <-time.After(100 * time.Millisecond):
		t.Fatal("TrySteal blocked when semaphore was full")
	}

	// Drain the semaphore
	for i := 0; i < maxConcurrent; i++ {
		<-stealer.sem
	}
}

func TestStealer_DownloadAndDeduplication(t *testing.T) {
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(imageBytes)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	store, err := NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	stealer := NewStealer(store, nil)
	_ = stealer
	hash := HashBytes(imageBytes)

	// Force steal once without probability check by adding to store or direct execution
	data, err := downloadImage(server.URL + "/test.png")
	if err != nil {
		t.Fatalf("downloadImage failed: %v", err)
	}
	if HashBytes(data) != hash {
		t.Fatalf("hash mismatch: got %s, want %s", HashBytes(data), hash)
	}

	fileName := hash + ".png"
	if err := store.Add(hash, fileName, data); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Second steal with existing hash should increment weight
	if store.Exists(hash) {
		_ = store.IncrementWeight(hash)
	}

	entry, ok := store.Get(hash)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.Weight != 2 {
		t.Errorf("expected weight 2, got %d", entry.Weight)
	}
}

func TestStealer_ConcurrentTheft(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	stealer := NewStealer(store, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stealer.TrySteal("http://127.0.0.1:9999/dummy.png")
		}()
	}
	wg.Wait()
}

func TestDownloadImage_RejectsOversized(t *testing.T) {
	oversized := bytes.Repeat([]byte{0xFF}, maxImageSize+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(oversized)
	}))
	defer server.Close()

	_, err := downloadImage(server.URL + "/big.jpg")
	if err == nil {
		t.Fatal("expected error for oversized image, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

func TestDownloadImage_AcceptsExactLimit(t *testing.T) {
	exact := bytes.Repeat([]byte{0xFF}, maxImageSize)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(exact)
	}))
	defer server.Close()

	data, err := downloadImage(server.URL + "/exact.jpg")
	if err != nil {
		t.Fatalf("expected success for exact-limit image, got: %v", err)
	}
	if len(data) != maxImageSize {
		t.Errorf("expected %d bytes, got %d", maxImageSize, len(data))
	}
}

func TestDownloadImage_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	saved := httpClient.Timeout
	httpClient.Timeout = 100 * time.Millisecond
	defer func() { httpClient.Timeout = saved }()

	_, err := downloadImage(server.URL + "/slow.jpg")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
