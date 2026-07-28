package memory

import (
	"context"
)

// Reader handles memory retrieval from the unified brain.
// It performs global search without filtering — the Gateway handles access control.
type Reader struct {
	store *Store
	vs    *VectorStore
	limit int
}

// NewReader creates a new memory reader.
// limit is the maximum number of memories to recall per query (0 = unlimited).
// vs is an optional VectorStore for semantic search; if nil, keyword search is used.
func NewReader(store *Store, vs *VectorStore, limit int) *Reader {
	return &Reader{store: store, vs: vs, limit: limit}
}

// Recall searches the unified brain using hybrid search (semantic + keyword).
// If a VectorStore is configured and the semantic search returns results, those
// are ranked by cosine similarity. Otherwise falls back to keyword matching on
// Content and Tags. The Gateway is responsible for filtering by owner/visibility.
func (r *Reader) Recall(ctx context.Context, currentMessage string) ([]MemoryEntry, error) {
	// Try semantic search first
	if r.vs != nil {
		ids, err := r.vs.Search(ctx, currentMessage, r.limit)
		if err == nil && len(ids) > 0 {
			entries, err := r.store.ListAll()
			if err != nil {
				return nil, err
			}
			var results []MemoryEntry
			seen := make(map[string]bool)
			for _, id := range ids {
				seen[id] = true
			}
			for _, entry := range entries {
				if seen[entry.ID] {
					results = append(results, entry)
				}
			}
			// If semantic results are enough, return them
			if len(results) >= r.limit || r.limit == 0 {
				return results, nil
			}
			// Fall back: combine with keyword search, deduplicate
			kwResults, err := r.store.Search(currentMessage, r.limit*2)
			if err == nil {
				for _, e := range kwResults {
					if !seen[e.ID] {
						results = append(results, e)
						seen[e.ID] = true
					}
					if r.limit > 0 && len(results) >= r.limit {
						break
					}
				}
			}
			return results, nil
		}
	}
	// Fall back to keyword search
	return r.store.Search(currentMessage, r.limit)
}