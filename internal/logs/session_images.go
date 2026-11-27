package logs

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxSessionImages     = 256
	maxSessionImageBytes = 64 << 20
	// MaxSessionImageBytes is the largest single image kept for previews. Fetchers
	// must enforce it while reading, not only when the image is stored.
	MaxSessionImageBytes = 8 << 20

	maxSessionImageFetches   = 4  // concurrent preview downloads per store
	maxSessionImagePending   = 32 // running + queued preview downloads per store
	sessionImageFetchTimeout = 20 * time.Second
)

// SessionImageKey derives the opaque, hash-shaped key under which a
// conversation image is served from the log image endpoint. It is computed
// from the image source so the key is known before the bytes are fetched.
func SessionImageKey(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

type sessionImage struct {
	key         string
	contentType string
	data        []byte
}

// sessionImageCache is a small in-memory LRU for images referenced by
// conversation messages. Unlike log images it is not tied to log entries and is
// lost on restart; previews simply degrade to the plain placeholder.
type sessionImageCache struct {
	mu    sync.Mutex
	items map[string]*list.Element
	order *list.List
	bytes int
}

func (c *sessionImageCache) put(key, contentType string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]*list.Element)
		c.order = list.New()
	}
	if element, ok := c.items[key]; ok {
		c.order.MoveToFront(element)
		return
	}
	c.items[key] = c.order.PushFront(&sessionImage{key: key, contentType: contentType, data: data})
	c.bytes += len(data)
	for c.order.Len() > maxSessionImages || c.bytes > maxSessionImageBytes {
		oldest := c.order.Back()
		evicted := oldest.Value.(*sessionImage)
		c.order.Remove(oldest)
		delete(c.items, evicted.key)
		c.bytes -= len(evicted.data)
	}
}

func (c *sessionImageCache) get(key string) (sessionImage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return sessionImage{}, false
	}
	c.order.MoveToFront(element)
	return *element.Value.(*sessionImage), true
}

// sessionImageFetcher bounds background preview downloads: same-key requests are
// merged, at most maxSessionImageFetches run at once, and when
// maxSessionImagePending are already running or queued new ones are skipped so
// message intake never blocks or accumulates work.
type sessionImageFetcher struct {
	mu       sync.Mutex
	inflight map[string]struct{}
	slots    chan struct{}
}

// FetchSessionImage downloads a conversation image in the background and keeps
// it for previews. It returns immediately; false means the request was dropped
// because too many downloads are pending. Failed downloads just leave no image.
func (s *Store) FetchSessionImage(key string, fetch func(context.Context) ([]byte, error)) bool {
	if key == "" || s.HasSessionImage(key) {
		return true
	}
	f := &s.sessionFetches
	f.mu.Lock()
	if f.inflight == nil {
		f.inflight = make(map[string]struct{})
		f.slots = make(chan struct{}, maxSessionImageFetches)
	}
	if _, ok := f.inflight[key]; ok {
		f.mu.Unlock()
		return true
	}
	if len(f.inflight) >= maxSessionImagePending {
		f.mu.Unlock()
		return false
	}
	f.inflight[key] = struct{}{}
	f.mu.Unlock()

	go func() {
		defer func() {
			f.mu.Lock()
			delete(f.inflight, key)
			f.mu.Unlock()
		}()
		f.slots <- struct{}{}
		defer func() { <-f.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), sessionImageFetchTimeout)
		defer cancel()
		if data, err := fetch(ctx); err == nil {
			s.PutSessionImage(key, data)
		}
	}()
	return true
}

// HasSessionImage reports whether a conversation image is already cached.
func (s *Store) HasSessionImage(key string) bool {
	_, ok := s.sessionImages.get(key)
	return ok
}

// PutSessionImage retains a conversation image for hover previews. Data that is
// empty, oversized, or not an image is ignored.
func (s *Store) PutSessionImage(key string, data []byte) {
	if key == "" || len(data) == 0 || len(data) > MaxSessionImageBytes {
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return
	}
	s.sessionImages.put(key, contentType, append([]byte(nil), data...))
}
