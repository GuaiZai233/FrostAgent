package sticker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func useStickerHTTPResponse(t *testing.T, status int, data []byte) {
	t.Helper()
	saved := httpClient.Transport
	httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(data)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { httpClient.Transport = saved })
}

func TestStealerObserveProbabilityMissSkipsLoader(t *testing.T) {
	stealer := NewStealer(nil, nil)
	stealer.random = func() float64 { return 1 }
	var loads atomic.Int32

	stealer.Observe("group:test", "message:1", 0, func(context.Context) ([]byte, error) {
		loads.Add(1)
		return []byte("GIF89a"), nil
	}, true)

	if got := loads.Load(); got != 0 {
		t.Fatalf("loader calls = %d, want 0 when probability gate misses", got)
	}
}

func TestStealerObserveFullSemaphoreSkipsLoader(t *testing.T) {
	stealer := NewStealer(nil, nil)
	stealer.random = func() float64 { return 0 }
	for i := 0; i < cap(stealer.sem); i++ {
		stealer.sem <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(stealer.sem); i++ {
			<-stealer.sem
		}
	}()

	var loads atomic.Int32
	stealer.Observe("group:test", "message:1", 0, func(context.Context) ([]byte, error) {
		loads.Add(1)
		return []byte("GIF89a"), nil
	}, true)

	if got := loads.Load(); got != 0 {
		t.Fatalf("loader calls = %d, want 0 when semaphore is full", got)
	}
}

func TestStealerObserveBurstCreatesOnlyAdmittedLoaders(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	stealer := NewStealer(store, nil)
	stealer.random = func() float64 { return 0 }
	started := make(chan struct{}, maxConcurrent+1)
	release := make(chan struct{})
	loader := func(context.Context) ([]byte, error) {
		started <- struct{}{}
		<-release
		return []byte("GIF89a burst"), nil
	}

	for i := 0; i < 1000; i++ {
		stealer.Observe("group:test", fmt.Sprintf("message:%d", i), 0, loader, true)
	}
	for i := 0; i < maxConcurrent; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("admitted loader %d did not start", i)
		}
	}
	select {
	case <-started:
		t.Fatal("burst created a loader waiting behind the semaphore")
	default:
	}

	close(release)
	for i := 0; i < cap(stealer.sem); i++ {
		stealer.sem <- struct{}{}
	}
	for i := 0; i < cap(stealer.sem); i++ {
		<-stealer.sem
	}
	stealer.observedMu.Lock()
	observedCount := len(stealer.observed)
	stealer.observedMu.Unlock()
	if observedCount != maxObservedStickers {
		t.Fatalf("observed metadata count = %d, want cap %d", observedCount, maxObservedStickers)
	}
}

func TestStealer_GuessExtension(t *testing.T) {
	tests := []struct {
		data     []byte
		expected string
	}{
		{[]byte{0x89, 'P', 'N', 'G'}, ".png"},
		{[]byte("GIF89a"), ".gif"},
		{[]byte("RIFF1234WEBP"), ".webp"},
		{[]byte{0xFF, 0xD8, 0xFF}, ".jpg"},
	}

	for _, tt := range tests {
		got := guessExtension(tt.data)
		if got != tt.expected {
			t.Errorf("guessExtension(%v) = %q, want %q", tt.data, got, tt.expected)
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
		stealer.TrySteal([]byte("GIF89a"))
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
	useStickerHTTPResponse(t, http.StatusOK, imageBytes)

	tempDir := t.TempDir()
	store, err := NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	stealer := NewStealer(store, nil)
	_ = stealer
	hash := HashBytes(imageBytes)

	// Force steal once without probability check by adding to store or direct execution
	data, err := downloadImage("https://gchat.qpic.cn/test.png")
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

func TestStealer_ExplicitStealIsDeterministicAndDeduplicates(t *testing.T) {
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x01}
	store, err := NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	stealer := NewStealer(store, nil)

	first, err := stealer.Steal(context.Background(), imageBytes)
	if err != nil {
		t.Fatalf("explicit Steal returned error: %v", err)
	}
	wantID := HashBytes(imageBytes)
	if first.ID != wantID || first.Duplicate {
		t.Fatalf("first result = %+v, want ID %s and non-duplicate", first, wantID)
	}

	second, err := stealer.Steal(context.Background(), imageBytes)
	if err != nil {
		t.Fatalf("duplicate Steal returned error: %v", err)
	}
	if second.ID != wantID || !second.Duplicate {
		t.Fatalf("second result = %+v, want duplicate ID %s", second, wantID)
	}
	entry, ok := store.Get(wantID)
	if !ok || entry.Weight != 2 {
		t.Fatalf("stored entry = %+v, found=%v; want weight 2", entry, ok)
	}
}

func TestStealerPersistenceFailureIsNotReportedAsDuplicate(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "stickers")
	store, err := NewStore(storeDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	data := []byte("GIF89a persistence failure")
	id := HashBytes(data)
	unblock := blockIndexSave(t, store)
	defer unblock()

	result, err := NewStealer(store, nil).Steal(context.Background(), data)
	if err == nil || !strings.Contains(err.Error(), "add sticker") {
		t.Fatalf("Steal error = %v, want Add persistence failure", err)
	}
	if result.Duplicate {
		t.Fatal("persistence failure was reported as a duplicate")
	}
	if store.Exists(id) {
		t.Fatal("persistence failure left a duplicate-looking entry in memory")
	}
	if _, err := os.Stat(filepath.Join(storeDir, id+".gif")); !os.IsNotExist(err) {
		t.Fatalf("persistence failure left an image file behind: %v", err)
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
			stealer.TrySteal([]byte("GIF89a concurrent"))
		}()
	}
	wg.Wait()

	// TrySteal returns after scheduling accepted work. Occupying every semaphore
	// slot waits for those workers before t.TempDir starts removing the store.
	for i := 0; i < cap(stealer.sem); i++ {
		stealer.sem <- struct{}{}
	}
	for i := 0; i < cap(stealer.sem); i++ {
		<-stealer.sem
	}
}

func TestDownloadImage_RejectsOversized(t *testing.T) {
	oversized := bytes.Repeat([]byte{0xFF}, maxImageSize+1)
	useStickerHTTPResponse(t, http.StatusOK, oversized)

	_, err := downloadImage("https://gchat.qpic.cn/big.jpg")
	if err == nil {
		t.Fatal("expected error for oversized image, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

func TestDownloadImage_AcceptsExactLimit(t *testing.T) {
	exact := bytes.Repeat([]byte{0xFF}, maxImageSize)
	useStickerHTTPResponse(t, http.StatusOK, exact)

	data, err := downloadImage("https://gchat.qpic.cn/exact.jpg")
	if err != nil {
		t.Fatalf("expected success for exact-limit image, got: %v", err)
	}
	if len(data) != maxImageSize {
		t.Errorf("expected %d bytes, got %d", maxImageSize, len(data))
	}
}

func TestDownloadImage_Timeout(t *testing.T) {
	saved := httpClient.Timeout
	httpClient.Timeout = 100 * time.Millisecond
	defer func() { httpClient.Timeout = saved }()
	savedTransport := httpClient.Transport
	httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	defer func() { httpClient.Transport = savedTransport }()

	_, err := downloadImage("https://gchat.qpic.cn/slow.jpg")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestDownloadImage_RejectsUntrustedURL(t *testing.T) {
	_, err := downloadImage("http://127.0.0.1/private.jpg")
	if err == nil || !strings.Contains(err.Error(), "allowed QQ media URL") {
		t.Fatalf("downloadImage error = %v, want untrusted URL rejection", err)
	}
}
