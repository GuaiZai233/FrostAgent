package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
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

// SearchByTags searches memories by multiple tags using OR logic.
// Each search tag is matched against entry tags and content, with results
// ranked by relevance: tag exact match > tag substring > content substring.
// Tags are never split or reprocessed — the caller is responsible for providing
// meaningful search terms.
func (s *Store) SearchByTags(tags []string, limit int) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.load()
	if err != nil {
		return nil, err
	}

	type scored struct {
		entry MemoryEntry
		score float64
	}

	var scoredEntries []scored
	for _, entry := range brain.Entries {
		sc := tagMatchScore(entry, tags)
		if sc > 0 {
			scoredEntries = append(scoredEntries, scored{entry: entry, score: sc})
		}
	}

	// Sort by score descending
	sort.SliceStable(scoredEntries, func(i, j int) bool {
		return scoredEntries[i].score > scoredEntries[j].score
	})

	results := make([]MemoryEntry, 0, len(scoredEntries))
	for _, se := range scoredEntries {
		results = append(results, se.entry)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results, nil
}

// tagMatchScore computes a relevance score for an entry against search tags.
// Scoring: tag exact match +3, tag substring match +2, content substring match +1.
// Lowercases both the entry and search tags for case-insensitive matching.
func tagMatchScore(entry MemoryEntry, searchTags []string) float64 {
	var score float64
	contentLower := strings.ToLower(entry.Content)
	for _, st := range searchTags {
		stLower := strings.ToLower(strings.TrimSpace(st))
		if stLower == "" {
			continue
		}

		var termScore float64
		for _, et := range entry.Tags {
			tagLower := strings.ToLower(strings.TrimSpace(et))
			if tagLower == stLower {
				termScore = 3.0
				break
			}
			if termScore < 2.0 && strings.Contains(tagLower, stLower) {
				termScore = 2.0
			}
		}

		if termScore == 0 && strings.Contains(contentLower, stLower) {
			termScore = 1.0
		}
		score += termScore
	}
	return score
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

// ApplyReflection atomically applies a validated reflection result for one
// owner and returns the remaining entries plus IDs that were deleted.
func (s *Store) ApplyReflection(
	owner string,
	outdatedIDs []string,
	importanceUpdates map[string]float64,
) ([]MemoryEntry, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return nil, nil, err
	}

	outdated := make(map[string]bool, len(outdatedIDs))
	for _, id := range outdatedIDs {
		outdated[id] = true
	}

	remaining := make([]MemoryEntry, 0)
	deleted := make([]string, 0)
	for i := range brain.Entries {
		entry := &brain.Entries[i]
		if entry.Owner != owner {
			continue
		}
		if outdated[entry.ID] {
			deleted = append(deleted, entry.ID)
			continue
		}
		if importance, ok := importanceUpdates[entry.ID]; ok {
			if importance < 0 {
				importance = 0
			} else if importance > 1 {
				importance = 1
			}
			entry.Importance = importance
			entry.UpdatedAt = time.Now()
		}
		remaining = append(remaining, *entry)
	}

	if len(deleted) > 0 {
		filtered := brain.Entries[:0]
		for _, entry := range brain.Entries {
			if entry.Owner == owner && outdated[entry.ID] {
				continue
			}
			filtered = append(filtered, entry)
		}
		brain.Entries = filtered
	}

	// Always rewrite after reflection so legacy inline "summaries" fields are
	// removed from brain.json even when no importance or deletion changed.
	if err := s.save(brain); err != nil {
		return nil, nil, err
	}
	return remaining, deleted, nil
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

// UpdateEntry replaces an existing memory entry in-place, preserving its ID and CreatedAt.
func (s *Store) UpdateEntry(updated MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return err
	}

	for i, entry := range brain.Entries {
		if entry.ID == updated.ID {
			updated.CreatedAt = entry.CreatedAt
			updated.UpdatedAt = time.Now()
			brain.Entries[i] = updated
			return s.save(brain)
		}
	}
	return fmt.Errorf("memory %s not found", updated.ID)
}
