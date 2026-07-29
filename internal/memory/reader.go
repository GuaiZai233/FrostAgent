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

// Recall searches the unified brain using hybrid search (keyword + semantic).
// Deterministic content/tag matches are ranked first; semantic results that meet
// the vector threshold fill the remaining slots. The Gateway is responsible for
// owner/visibility filtering.
func (r *Reader) Recall(ctx context.Context, currentMessage string) ([]MemoryEntry, error) {
	seen := make(map[string]bool)
	var results []MemoryEntry

	// Phase 1: ranked keyword matches always get priority.
	keywordResults, keywordErr := r.store.Search(currentMessage, r.limit)
	if keywordErr == nil {
		for _, entry := range keywordResults {
			results = append(results, entry)
			seen[entry.ID] = true
		}
	}
	if r.limit > 0 && len(results) >= r.limit {
		return results[:r.limit], nil
	}

	// Phase 2: semantic matches above the similarity threshold fill remaining slots.
	if r.vs == nil {
		return results, keywordErr
	}

	remaining := r.limit
	if remaining > 0 {
		remaining -= len(results)
	}
	ids, vectorErr := r.vs.Search(ctx, currentMessage, remaining)
	if vectorErr == nil && len(ids) > 0 {
		all, err := r.store.ListAll()
		if err != nil {
			if len(results) > 0 {
				return results, nil
			}
			return nil, err
		}
		entryMap := make(map[string]MemoryEntry, len(all))
		for _, entry := range all {
			entryMap[entry.ID] = entry
		}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			entry, ok := entryMap[id]
			if !ok {
				continue
			}
			results = append(results, entry)
			seen[id] = true
			if r.limit > 0 && len(results) >= r.limit {
				break
			}
		}
	}

	if len(results) > 0 {
		return results, nil
	}
	if keywordErr != nil {
		return nil, keywordErr
	}
	if vectorErr != nil {
		// Vector search is optional; a failure with a healthy keyword store is
		// equivalent to no semantic matches.
		return nil, nil
	}
	return results, nil
}
