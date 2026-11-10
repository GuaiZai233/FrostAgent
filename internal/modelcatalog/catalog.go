// Package modelcatalog keeps a process-wide snapshot of the models.dev model
// list and derives capability hints for upstream model names.
//
// The snapshot is fetched once at startup and then every RefreshInterval. A
// failed fetch keeps the previous snapshot; when no snapshot exists at all,
// Infer reports nothing rather than guessing.
package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// DefaultURL is the models.dev model list used for capability hints.
	DefaultURL = "https://models.dev/models.json?type=all"
	// RefreshInterval is how often the list is re-fetched after startup.
	RefreshInterval = 48 * time.Hour

	fetchTimeout = 30 * time.Second
	maxBodyBytes = 64 << 20
)

// Capability names match the labels offered by the model router editor.
const (
	CapabilityText   = "text"
	CapabilityTools  = "tools"
	CapabilityVision = "vision"
)

type record struct {
	ToolCall   bool `json:"tool_call"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
}

// capabilitySet is the capability tuple of one models.dev entry.
type capabilitySet struct {
	text, tools, vision bool
}

// index maps a lowercased models.dev id ("provider/model") to its capabilities.
type index struct {
	full map[string]capabilitySet
	bare map[string][]capabilitySet
}

// Catalog is a models.dev snapshot with an on-disk cache.
type Catalog struct {
	url       string
	cachePath string
	interval  time.Duration
	client    *http.Client
	current   atomic.Pointer[index]
}

// New creates a catalog that caches the raw list at cachePath.
func New(url, cachePath string, interval time.Duration) *Catalog {
	return &Catalog{
		url:       url,
		cachePath: cachePath,
		interval:  interval,
		client:    &http.Client{Timeout: fetchTimeout},
	}
}

var active atomic.Pointer[Catalog]

// Start installs c as the process-wide catalog, loads the cache synchronously,
// and then refreshes in the background: once immediately and then every interval
// until ctx is done.
func Start(ctx context.Context, c *Catalog) {
	active.Store(c)
	if err := c.loadCache(); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "models.dev cache ignored: %v\n", err)
	}
	go func() {
		c.refresh(ctx)
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refresh(ctx)
			}
		}
	}()
}

// Infer returns the capabilities that every models.dev entry matching name
// agrees on. found is false when there is no snapshot or no matching entry.
// Capabilities on which matching entries disagree are omitted.
func Infer(name string) (capabilities []string, found bool) {
	c := active.Load()
	if c == nil {
		return nil, false
	}
	idx := c.current.Load()
	if idx == nil {
		return nil, false
	}
	return idx.lookup(name)
}

func (idx *index) lookup(name string) ([]string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil, false
	}
	var matches []capabilitySet
	if set, ok := idx.full[key]; ok {
		matches = append(matches, set)
	} else if !strings.Contains(key, "/") {
		// A bare name may match the same model listed under several providers.
		matches = idx.bare[key]
	}
	if len(matches) == 0 {
		return nil, false
	}
	all := capabilitySet{text: true, tools: true, vision: true}
	for _, m := range matches {
		all.text = all.text && m.text
		all.tools = all.tools && m.tools
		all.vision = all.vision && m.vision
	}
	var caps []string
	if all.text {
		caps = append(caps, CapabilityText)
	}
	if all.tools {
		caps = append(caps, CapabilityTools)
	}
	if all.vision {
		caps = append(caps, CapabilityVision)
	}
	return caps, true
}

func (c *Catalog) refresh(ctx context.Context) {
	raw, err := c.fetch(ctx)
	if err == nil {
		var idx *index
		idx, err = parse(raw)
		if err == nil {
			if writeErr := writeFileAtomic(c.cachePath, raw); writeErr != nil {
				fmt.Fprintf(os.Stderr, "models.dev cache write failed: %v\n", writeErr)
			}
			c.current.Store(idx)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "models.dev refresh failed, keeping previous data: %v\n", err)
}

func (c *Catalog) loadCache() error {
	raw, err := os.ReadFile(c.cachePath)
	if err != nil {
		return err
	}
	idx, err := parse(raw)
	if err != nil {
		return err
	}
	c.current.Store(idx)
	return nil
}

func (c *Catalog) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

func parse(raw []byte) (*index, error) {
	var records map[string]record
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode models list: %w", err)
	}
	idx := &index{full: map[string]capabilitySet{}, bare: map[string][]capabilitySet{}}
	for id, rec := range records {
		key := strings.ToLower(strings.TrimSpace(id))
		if key == "" {
			continue
		}
		set := capabilitySet{tools: rec.ToolCall}
		for _, mod := range rec.Modalities.Input {
			switch strings.ToLower(mod) {
			case "text":
				set.text = true
			case "image":
				set.vision = true
			}
		}
		idx.full[key] = set
		bare := key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			bare = key[i+1:]
		}
		idx.bare[bare] = append(idx.bare[bare], set)
	}
	if len(idx.full) == 0 {
		return nil, errors.New("models list is empty")
	}
	return idx, nil
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
