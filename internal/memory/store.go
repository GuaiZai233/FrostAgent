package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Store implements a file-based unified memory store.
// All memories are stored in a single brain.json file.
type Store struct {
	path string
	mu   sync.RWMutex
}

// NewStore creates a new file-based memory store.
// path should point to the brain.json file.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// load reads the brain data from disk.
func (s *Store) load() (*BrainData, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &BrainData{Entries: []MemoryEntry{}}, nil
		}
		return nil, fmt.Errorf("failed to read brain: %w", err)
	}
	var brain BrainData
	if err := json.Unmarshal(data, &brain); err != nil {
		return nil, fmt.Errorf("failed to parse brain: %w", err)
	}
	return &brain, nil
}

// save writes the brain data to disk.
func (s *Store) save(brain *BrainData) error {
	data, err := json.MarshalIndent(brain, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal brain: %w", err)
	}
	return os.WriteFile(s.path, data, 0644)
}

// Save writes a single memory entry to the store.
func (s *Store) Save(entry MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return err
	}

	entry.UpdatedAt = time.Now()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = entry.UpdatedAt
	}
	if entry.Visibility == "" {
		entry.Visibility = VisibilityPrivate
	}

	brain.Entries = append(brain.Entries, entry)
	return s.save(brain)
}

// Search performs a global keyword search across all memories.
// Returns entries whose content or tags contain the query string (case-insensitive).
func (s *Store) Search(query string, limit int) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.load()
	if err != nil {
		return nil, err
	}

	queryLower := strings.ToLower(query)
	var results []MemoryEntry
	for _, entry := range brain.Entries {
		if matchEntry(entry, queryLower) {
			results = append(results, entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

// matchEntry checks if a memory entry matches the query.
func matchEntry(entry MemoryEntry, queryLower string) bool {
	if strings.Contains(strings.ToLower(entry.Content), queryLower) {
		return true
	}
	for _, tag := range entry.Tags {
		if strings.Contains(strings.ToLower(tag), queryLower) {
			return true
		}
	}
	return false
}

// ListByOwner returns all memories owned by the specified user.
func (s *Store) ListByOwner(owner string) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.load()
	if err != nil {
		return nil, err
	}

	var results []MemoryEntry
	for _, entry := range brain.Entries {
		if entry.Owner == owner {
			results = append(results, entry)
		}
	}
	return results, nil
}

// ListAll returns all memories in the store (used by Reflector).
func (s *Store) ListAll() ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.load()
	if err != nil {
		return nil, err
	}
	return brain.Entries, nil
}

// GetSummary returns the memory summary for the specified owner from the unified brain.
func (s *Store) GetSummary(owner string) (*MemorySummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.load()
	if err != nil {
		return nil, err
	}

	for _, summary := range brain.Summaries {
		if summary.Owner == owner {
			s2 := summary
			return &s2, nil
		}
	}
	return nil, nil
}

// SaveSummary writes or updates a memory summary in the unified brain.
func (s *Store) SaveSummary(summary MemorySummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return err
	}

	for i, s2 := range brain.Summaries {
		if s2.Owner == summary.Owner {
			brain.Summaries[i] = summary
			return s.save(brain)
		}
	}
	brain.Summaries = append(brain.Summaries, summary)
	return s.save(brain)
}

// Delete removes a memory entry by ID.
func (s *Store) Delete(memoryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return err
	}

	for i, entry := range brain.Entries {
		if entry.ID == memoryID {
			brain.Entries = append(brain.Entries[:i], brain.Entries[i+1:]...)
			return s.save(brain)
		}
	}
	return fmt.Errorf("memory %s not found", memoryID)
}

// UpdateImportance updates a single memory's importance score.
func (s *Store) UpdateImportance(memoryID string, newImportance float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return err
	}

	for i, entry := range brain.Entries {
		if entry.ID == memoryID {
			brain.Entries[i].Importance = newImportance
			brain.Entries[i].UpdatedAt = time.Now()
			return s.save(brain)
		}
	}
	return fmt.Errorf("memory %s not found", memoryID)
}
