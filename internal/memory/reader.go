package memory

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

// Recall searches the unified brain for memories related to the current message.
// Returns all matching memories (unfiltered). The Gateway is responsible for filtering.
func (r *Reader) Recall(currentMessage string) ([]MemoryEntry, error) {
	return r.store.Search(currentMessage, r.limit)
}
