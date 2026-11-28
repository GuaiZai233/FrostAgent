package logs

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAHEC7cQAAAABJRU5ErkJggg==")

func TestSessionImageServedThroughLogImageHandler(t *testing.T) {
	store := New("", "test", 10)
	key := SessionImageKey("https://example.invalid/synthetic.png")
	if store.HasSessionImage(key) {
		t.Fatal("image cached before it was put")
	}
	store.PutSessionImage(key, testPNG)
	if !store.HasSessionImage(key) {
		t.Fatal("image not cached")
	}
	rec := httptest.NewRecorder()
	store.ImageHandler(rec, httptest.NewRequest(http.MethodGet, LogImagePathPrefix+key, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() != len(testPNG) {
		t.Fatalf("unexpected response: %d %q %d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	missing := httptest.NewRecorder()
	store.ImageHandler(missing, httptest.NewRequest(http.MethodGet, LogImagePathPrefix+strings.Repeat("0", 64), nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown key served: %d", missing.Code)
	}
}

func TestSessionImageRejectsNonImagesAndEvictsOldest(t *testing.T) {
	store := New("", "test", 10)
	store.PutSessionImage("text", []byte("not an image"))
	if store.HasSessionImage("text") {
		t.Fatal("non-image data was retained")
	}
	store.PutSessionImage("oversized", append(append([]byte(nil), testPNG...), make([]byte, MaxSessionImageBytes)...))
	if store.HasSessionImage("oversized") {
		t.Fatal("oversized image was retained")
	}
	for i := 0; i <= maxSessionImages; i++ {
		store.PutSessionImage(SessionImageKey(string(rune('a'+i%26))+strings.Repeat("x", i)), testPNG)
	}
	if store.HasSessionImage(SessionImageKey("a")) {
		t.Fatal("oldest image was not evicted past the entry limit")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFetchSessionImageMergesInflightRequestsForOneKey(t *testing.T) {
	store := New("", "test", 10)
	release := make(chan struct{})
	var started atomic.Int32
	fetch := func(context.Context) ([]byte, error) {
		started.Add(1)
		<-release
		return testPNG, nil
	}
	key := SessionImageKey("https://example.invalid/same.png")
	for i := 0; i < 300; i++ {
		if !store.FetchSessionImage(key, fetch) {
			t.Fatal("a duplicate of an in-flight download must be merged, not dropped")
		}
	}
	waitFor(t, "first download", func() bool { return started.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	if got := started.Load(); got != 1 {
		t.Fatalf("%d downloads started for one key, want 1", got)
	}
	close(release)
	waitFor(t, "image cached", func() bool { return store.HasSessionImage(key) })
	if !store.FetchSessionImage(key, fetch) || started.Load() != 1 {
		t.Fatal("a cached image must not be downloaded again")
	}
}

func TestFetchSessionImageBoundsConcurrencyAndQueueAndReleasesSlots(t *testing.T) {
	store := New("", "test", 10)
	release := make(chan struct{})
	var running, peak, started atomic.Int32
	fetch := func(context.Context) ([]byte, error) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		started.Add(1)
		<-release
		running.Add(-1)
		return nil, errors.New("synthetic download failure")
	}
	accepted, dropped := 0, 0
	for i := 0; i < maxSessionImagePending+20; i++ {
		if store.FetchSessionImage(SessionImageKey(fmt.Sprintf("https://example.invalid/%d.png", i)), fetch) {
			accepted++
		} else {
			dropped++
		}
	}
	if accepted != maxSessionImagePending || dropped != 20 {
		t.Fatalf("accepted %d dropped %d, want %d and 20", accepted, dropped, maxSessionImagePending)
	}
	waitFor(t, "workers", func() bool { return started.Load() == maxSessionImageFetches })
	time.Sleep(20 * time.Millisecond)
	if got := peak.Load(); got != maxSessionImageFetches {
		t.Fatalf("peak concurrent downloads = %d, want %d", got, maxSessionImageFetches)
	}
	close(release)
	// Failed downloads must give their slots back and may be retried.
	waitFor(t, "slots released", func() bool {
		return store.FetchSessionImage(SessionImageKey("https://example.invalid/retry.png"), func(context.Context) ([]byte, error) { return testPNG, nil })
	})
	waitFor(t, "retry cached", func() bool { return store.HasSessionImage(SessionImageKey("https://example.invalid/retry.png")) })
}

type memorySessionBackend struct {
	mu      sync.Mutex
	images  map[string]sessionImage
	touches int
	putErr  error
	block   chan struct{} // when set, every call waits for it to close
}

func (b *memorySessionBackend) wait() {
	if b.block != nil {
		<-b.block
	}
}

func (b *memorySessionBackend) PutSessionImage(key, contentType string, data []byte) error {
	b.wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.putErr != nil {
		return b.putErr
	}
	b.images[key] = sessionImage{key: key, contentType: contentType, data: data}
	return nil
}

func (b *memorySessionBackend) GetSessionImage(key string) (string, []byte, bool) {
	b.wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	image, ok := b.images[key]
	return image.contentType, image.data, ok
}

func (b *memorySessionBackend) HasSessionImage(key string) bool {
	b.wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.images[key]
	return ok
}

func (b *memorySessionBackend) TouchSessionImage(key string) {
	b.wait()
	b.mu.Lock()
	b.touches++
	b.mu.Unlock()
}

func (b *memorySessionBackend) stored() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.images)
}

func (c *sessionImageCache) unpersistedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, element := range c.items {
		if element.Value.(*sessionImage).unpersisted {
			count++
		}
	}
	return count
}

func TestSessionImageSurvivesCacheLossThroughBackend(t *testing.T) {
	backend := &memorySessionBackend{images: map[string]sessionImage{}}
	store := New("", "test", 10)
	store.SetSessionImageBackend(backend)
	key := SessionImageKey("https://example.invalid/synthetic.png")
	store.PutSessionImage(key, testPNG)
	waitFor(t, "durable write", func() bool { return backend.stored() == 1 })

	// A fresh store models a restart: memory is empty, the backend is not.
	restarted := New("", "test", 10)
	restarted.SetSessionImageBackend(backend)
	if !restarted.HasSessionImage(key) {
		t.Fatal("durable image not reported after restart")
	}
	rec := httptest.NewRecorder()
	restarted.ImageHandler(rec, httptest.NewRequest(http.MethodGet, LogImagePathPrefix+key, nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != len(testPNG) {
		t.Fatalf("durable image not served after restart: %d", rec.Code)
	}
	if _, warmed := restarted.sessionImages.get(key); !warmed {
		t.Fatal("serving a durable image should warm the memory cache")
	}
}

func TestSessionImageMessagePathNeverWaitsForBackend(t *testing.T) {
	backend := &memorySessionBackend{images: map[string]sessionImage{}, block: make(chan struct{})}
	store := New("", "test", 10)
	store.SetSessionImageBackend(backend)
	defer close(backend.block)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			key := SessionImageKey(fmt.Sprint("blocked", i))
			store.PutSessionImage(key, testPNG)
			store.FetchSessionImage(key, func(context.Context) ([]byte, error) { return testPNG, nil })
			store.FetchSessionImage(SessionImageKey(fmt.Sprint("unknown", i)), func(context.Context) ([]byte, error) { return nil, errors.New("offline") })
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("storing or fetching preview images waited for a blocked database")
	}
}

func TestSessionImagePersistFailureIsRetriedOnNextFetch(t *testing.T) {
	backend := &memorySessionBackend{images: map[string]sessionImage{}, putErr: errors.New("database busy")}
	store := New("", "test", 10)
	store.SetSessionImageBackend(backend)
	key := SessionImageKey("https://example.invalid/retry.png")
	store.PutSessionImage(key, testPNG)
	waitFor(t, "failed write to settle", func() bool {
		store.sessionPersist.mu.Lock()
		defer store.sessionPersist.mu.Unlock()
		return !store.sessionPersist.running && len(store.sessionPersist.queue) == 0
	})
	if backend.stored() != 0 || !store.HasSessionImage(key) {
		t.Fatal("expected a memory-only image after the failed write")
	}

	backend.mu.Lock()
	backend.putErr = nil
	backend.mu.Unlock()
	// The memory hit must not hide that the image was never persisted.
	store.FetchSessionImage(key, func(context.Context) ([]byte, error) {
		t.Error("must not download again")
		return nil, errors.New("unexpected")
	})
	waitFor(t, "retried durable write", func() bool { return backend.stored() == 1 })
}

func TestSessionImageViewTouchesDurableLRUOnMemoryHit(t *testing.T) {
	backend := &memorySessionBackend{images: map[string]sessionImage{}}
	store := New("", "test", 10)
	store.SetSessionImageBackend(backend)
	key := SessionImageKey("https://example.invalid/viewed.png")
	store.PutSessionImage(key, testPNG)
	waitFor(t, "durable write", func() bool { return backend.stored() == 1 })

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		store.ImageHandler(rec, httptest.NewRequest(http.MethodGet, LogImagePathPrefix+key, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("memory hit not served: %d", rec.Code)
		}
	}
	waitFor(t, "LRU touch", func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.touches >= 1
	})
	time.Sleep(20 * time.Millisecond)
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.touches != 1 {
		t.Fatalf("touches must be throttled per key, got %d", backend.touches)
	}
}

func TestSessionImageRetryStateStaysBoundedUnderOutage(t *testing.T) {
	backend := &memorySessionBackend{images: map[string]sessionImage{}, block: make(chan struct{})}
	store := New("", "test", 10)
	store.SetSessionImageBackend(backend)

	// Far more distinct images than either the queue or the LRU can hold.
	total := maxSessionImages*4 + 50
	for i := 0; i < total; i++ {
		store.PutSessionImage(SessionImageKey(fmt.Sprint("outage", i)), testPNG)
	}
	store.sessionPersist.mu.Lock()
	queued := len(store.sessionPersist.queue)
	store.sessionPersist.mu.Unlock()
	if queued > maxSessionPersistQueue {
		t.Fatalf("queue grew to %d", queued)
	}
	if pending := store.sessionImages.unpersistedCount(); pending > maxSessionImages {
		t.Fatalf("retry state tracks %d images, more than the %d the cache can hold", pending, maxSessionImages)
	}

	// Recovery drains what is queued; nothing evicted is left behind.
	close(backend.block)
	waitFor(t, "queue to drain", func() bool {
		store.sessionPersist.mu.Lock()
		defer store.sessionPersist.mu.Unlock()
		return !store.sessionPersist.running && len(store.sessionPersist.queue) == 0
	})
	if pending := store.sessionImages.unpersistedCount(); pending > maxSessionImages {
		t.Fatalf("retry state %d exceeds cache capacity after recovery", pending)
	}
}
