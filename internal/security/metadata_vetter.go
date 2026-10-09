package security

import (
	"sync"
)

// MetadataVetter provides thread-safe value-based caching and in-flight deduplication
// for platform metadata (nicknames, cards, group names) evaluated against security.SourcePlatformMeta.
type MetadataVetter struct {
	mu       sync.RWMutex
	cache    map[string]bool
	inFlight map[string]struct{}
}

// NewMetadataVetter initializes a new MetadataVetter.
func NewMetadataVetter() *MetadataVetter {
	return &MetadataVetter{
		cache:    make(map[string]bool),
		inFlight: make(map[string]struct{}),
	}
}

// Check returns (isSafe, isCached). If text is empty, it returns (true, true).
func (v *MetadataVetter) Check(text string) (bool, bool) {
	if text == "" {
		return true, true
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	safe, ok := v.cache[text]
	return safe, ok
}

// Record records the evaluation result for text and clears in-flight state.
func (v *MetadataVetter) Record(text string, safe bool) {
	if text == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cache[text] = safe
	delete(v.inFlight, text)
}

// MarkInFlight checks if text is currently being evaluated.
// If already cached or in-flight, it returns false.
// Otherwise, it marks text as in-flight and returns true.
func (v *MetadataVetter) MarkInFlight(text string) bool {
	if text == "" {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.cache[text]; ok {
		return false
	}
	if _, ok := v.inFlight[text]; ok {
		return false
	}
	v.inFlight[text] = struct{}{}
	return true
}

// ClearInFlight removes text from in-flight tracking (e.g. on context cancellation or error).
func (v *MetadataVetter) ClearInFlight(text string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.inFlight, text)
}

// Size returns the number of cached entries.
func (v *MetadataVetter) Size() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.cache)
}

// InFlightCount returns the number of currently in-flight evaluations.
func (v *MetadataVetter) InFlightCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.inFlight)
}
