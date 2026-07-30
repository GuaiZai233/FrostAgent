package memory

import "context"

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
// Semantic results are ranked by cosine similarity; keyword results fill remaining
// slots by content/tag matching. The Gateway is responsible for owner/visibility filtering.
func (r *Reader) Recall(ctx context.Context, currentMessage string) ([]MemoryEntry, error) {
	// Phase 1: semantic search (if vector store is configured)
	seen := make(map[string]bool)
	var results []MemoryEntry

	if r.vs != nil {
		ids, err := r.vs.Search(ctx, currentMessage, r.limit)
		if err == nil && len(ids) > 0 {
			all, err := r.store.ListAll()
			if err != nil {
				return nil, err
			}
			// Build a lookup map for O(1) access
			entryMap := make(map[string]MemoryEntry, len(all))
			for _, e := range all {
				entryMap[e.ID] = e
			}
			for _, id := range ids {
				if e, ok := entryMap[id]; ok {
					results = append(results, e)
					seen[id] = true
				}
			}
		}
		// If we have enough results (or unlimited and have some), return early
		if r.limit > 0 && len(results) >= r.limit {
			return results[:r.limit], nil
		}
		// Unlimited mode: always fall through to keyword to catch non-indexed entries
	}

	// Phase 2: keyword fallback — fill remaining slots
	need := r.limit
	if need == 0 {
		need = 0 // unlimited, collect all
	} else {
		need -= len(results)
		if need <= 0 {
			return results, nil
		}
	}

	kwResults, err := r.store.Search(currentMessage, need)
	if err != nil {
		// If semantic search already returned something, return it best-effort
		if len(results) > 0 {
			return results, nil
		}
		return nil, err
	}

	for _, e := range kwResults {
		if seen[e.ID] {
			continue
		}
		results = append(results, e)
		seen[e.ID] = true
	}

	return results, nil
}

// SearchByTags searches memories by multiple tags using the store's tag-based search.
// Results are ranked by relevance (tag exact match > tag substring > content substring).
func (r *Reader) SearchByTags(ctx context.Context, tags []string) ([]MemoryEntry, error) {
	return r.store.SearchByTags(tags, r.limit)
}
