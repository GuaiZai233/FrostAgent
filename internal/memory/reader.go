package memory

import "context"

// Reader handles memory retrieval from the unified brain.
// It performs global search without filtering — the Gateway handles access control.
type Reader struct {
	store *Store
	limit int
}

// NewReader creates a new memory reader.
// limit is the maximum number of memories to recall per query (0 = unlimited).
func NewReader(store *Store, limit int) *Reader {
	return &Reader{store: store, limit: limit}
}

// Recall returns global candidates without applying the result limit.
// The caller must pass them through Gateway before calling Limit, otherwise
// inaccessible entries could crowd the current owner's memories out of the window.
func (r *Reader) Recall(_ context.Context, currentMessage string) ([]MemoryEntry, error) {
	return r.store.Search(currentMessage, 0)
}

// SearchByTags searches memories by multiple tags using the store's tag-based search.
// Results are ranked by relevance (tag exact match > tag substring > content substring).
func (r *Reader) SearchByTags(_ context.Context, tags []string) ([]MemoryEntry, error) {
	return r.store.SearchByTags(tags, 0)
}

// Limit caps an already access-filtered candidate list.
func (r *Reader) Limit(entries []MemoryEntry) []MemoryEntry {
	if r.limit <= 0 || len(entries) <= r.limit {
		return entries
	}
	return entries[:r.limit]
}
