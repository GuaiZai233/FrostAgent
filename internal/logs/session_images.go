package logs

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

const (
	maxSessionImages     = 256
	maxSessionImageBytes = 64 << 20
	maxSessionImageEach  = 8 << 20
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

// HasSessionImage reports whether a conversation image is already cached.
func (s *Store) HasSessionImage(key string) bool {
	_, ok := s.sessionImages.get(key)
	return ok
}

// PutSessionImage retains a conversation image for hover previews. Data that is
// empty, oversized, or not an image is ignored.
func (s *Store) PutSessionImage(key string, data []byte) {
	if key == "" || len(data) == 0 || len(data) > maxSessionImageEach {
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return
	}
	s.sessionImages.put(key, contentType, append([]byte(nil), data...))
}
