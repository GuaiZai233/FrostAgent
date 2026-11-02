package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const MaxContentBytes = 8 << 20

var ErrContentExpired = errors.New("computer-use content expired; do not re-execute the operation")

// Content is transient and deliberately separate from Operation and audit.
// It is never written to the durable operation journal.
type Content struct {
	Identity
	RequestHash string    `json:"request_hash"`
	Text        string    `json:"text,omitempty"`
	PNG         []byte    `json:"png,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type ContentReader interface{ Content(string) (Content, bool) }
type contentEntry struct {
	value Content
	size  int
}

// ContentCache is bounded in both lifetime and memory; restart deliberately
// loses raw output, never operation completion or deduplication metadata.
type ContentCache struct {
	mu      sync.Mutex
	entries map[string]contentEntry
	bytes   int
	now     func() time.Time
	timer   *time.Timer
	closed  bool
}

func NewContentCache() *ContentCache {
	return &ContentCache{entries: make(map[string]contentEntry), now: time.Now}
}

func (c *ContentCache) Put(req Request, text string, png []byte) error {
	hash, err := req.fingerprint()
	if err != nil {
		return err
	}
	value := Content{Identity: req.Identity, RequestHash: hash, Text: text, PNG: append([]byte(nil), png...), ExpiresAt: c.now().Add(15 * time.Minute)}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxContentBytes {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.prune()
	if previous, ok := c.entries[req.OperationID]; ok {
		c.bytes -= previous.size
		delete(c.entries, req.OperationID)
	}
	for len(c.entries) >= 128 || c.bytes+len(encoded) > 64<<20 {
		var oldest string
		var when time.Time
		for id, entry := range c.entries {
			if oldest == "" || entry.value.ExpiresAt.Before(when) {
				oldest, when = id, entry.value.ExpiresAt
			}
		}
		if oldest == "" {
			return ErrInvalid
		}
		c.bytes -= c.entries[oldest].size
		delete(c.entries, oldest)
	}
	c.entries[req.OperationID] = contentEntry{value: value, size: len(encoded)}
	c.bytes += len(encoded)
	c.schedule()
	return nil
}

// One timer per cache, not per operation, including when entries are evicted.
func (c *ContentCache) schedule() {
	if c.timer != nil || c.closed || len(c.entries) == 0 {
		return
	}
	var earliest time.Time
	for _, e := range c.entries {
		if earliest.IsZero() || e.value.ExpiresAt.Before(earliest) {
			earliest = e.value.ExpiresAt
		}
	}
	c.timer = time.AfterFunc(max(time.Millisecond, earliest.Sub(c.now())), func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.timer = nil
		c.prune()
		c.schedule()
	})
}
func (c *ContentCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	clear(c.entries)
	c.bytes = 0
	return nil
}

func (c *ContentCache) prune() {
	for id, entry := range c.entries {
		if !c.now().Before(entry.value.ExpiresAt) {
			c.bytes -= entry.size
			delete(c.entries, id)
		}
	}
}

func (c *ContentCache) Content(id string) (Content, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	entry, ok := c.entries[id]
	value := entry.value
	value.PNG = append([]byte(nil), value.PNG...)
	return value, ok
}

func (s *Service) content(id string) (Content, error) {
	op, err := s.Lookup(id)
	if err != nil {
		return Content{}, err
	}
	if !op.State.Terminal() {
		return Content{}, ErrCapacity
	}
	reader, ok := s.executor.(ContentReader)
	if !ok {
		return Content{}, ErrContentExpired
	}
	value, ok := reader.Content(id)
	if !ok || value.Identity != op.Identity || value.RequestHash != op.RequestHash {
		return Content{}, ErrContentExpired
	}
	return value, nil
}

func (c *Client) Content(ctx context.Context, request Request) (Content, error) {
	hash, err := request.fingerprint()
	if err != nil {
		return Content{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.BaseURL+"/v1/operations/"+request.OperationID+"/content", nil)
	if err != nil {
		return Content{}, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.config.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return Content{}, ErrOutcomeUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusGone {
		return Content{}, ErrContentExpired
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return Content{}, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return Content{}, ErrOutcomeUnknown
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxContentBytes+1))
	if err != nil || len(data) > MaxContentBytes {
		return Content{}, ErrProtocol
	}
	var result Content
	if json.Unmarshal(data, &result) != nil || result.Identity != request.Identity || result.RequestHash != hash {
		return Content{}, ErrProtocol
	}
	return result, nil
}
