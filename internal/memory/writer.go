package memory

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Writer handles memory writing.
type Writer struct {
	store *Store
}

// NewWriter creates a new memory writer.
func NewWriter(store *Store) *Writer {
	return &Writer{store: store}
}

// Write directly saves a memory entry (user explicitly said "remember this").
func (w *Writer) Write(owner string, content string, tags []string) error {
	entry := MemoryEntry{
		ID:         generateID(),
		Owner:      owner,
		Content:    content,
		Tags:       tags,
		Source:     SourceManual,
		Visibility: VisibilityPrivate,
		Importance: 0.8, // manual entries are considered important
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	return w.store.Save(entry)
}

// generateID creates a random hex ID prefixed with "mem_".
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "mem_" + hex.EncodeToString(b)
}
