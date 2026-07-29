package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
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
// The query is split into individual terms (by whitespace and common delimiters)
// and matched with OR logic: any term matching content or tags qualifies the entry.
// Results are ranked by relevance score (more term matches + tag hits rank higher).
func (s *Store) Search(query string, limit int) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.load()
	if err != nil {
		return nil, err
	}

	terms := splitTerms(query)
	if len(terms) == 0 {
		// Empty query: return all entries (respect limit)
		if limit > 0 && len(brain.Entries) > limit {
			return brain.Entries[:limit], nil
		}
		return brain.Entries, nil
	}

	type scoredEntry struct {
		entry MemoryEntry
		score float64
	}

	var scored []scoredEntry
	for _, entry := range brain.Entries {
		sc := entryScore(entry, terms)
		if sc > 0 {
			scored = append(scored, scoredEntry{entry: entry, score: sc})
		}
	}

	// Sort by score descending (stable to preserve insertion order for ties)
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	results := make([]MemoryEntry, 0, len(scored))
	for _, se := range scored {
		results = append(results, se.entry)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results, nil
}

// splitTerms separates Han and non-Han words so mixed queries such as
// "maimai当前版本" can match both English and Chinese memory fields.
func splitTerms(query string) []string {
	const (
		termNone = iota
		termHan
		termWord
	)

	terms := make([]string, 0)
	seen := make(map[string]bool)
	var current []rune
	currentKind := termNone

	flush := func() {
		if len(current) == 0 {
			return
		}
		term := strings.ToLower(string(current))
		if !seen[term] {
			terms = append(terms, term)
			seen[term] = true
		}
		current = current[:0]
	}

	for _, r := range query {
		kind := termNone
		switch {
		case unicode.Is(unicode.Han, r):
			kind = termHan
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			kind = termWord
		}

		if kind == termNone {
			flush()
			currentKind = termNone
			continue
		}
		if currentKind != termNone && currentKind != kind {
			flush()
		}
		current = append(current, r)
		currentKind = kind
	}
	flush()

	return terms
}

// entryScore computes a relevance score for a memory entry against search terms.
// Scoring:
//   - Content substring match: +1.0 per matched term
//   - Tag substring match: +1.5 per matched term (tags are more specific)
//   - Tag exact match: +2.0 per matched term
func entryScore(entry MemoryEntry, terms []string) float64 {
	var score float64
	contentLower := strings.ToLower(entry.Content)
	for _, term := range terms {
		termLower := strings.ToLower(term)
		if strings.Contains(contentLower, termLower) {
			score += 1.0
		}
		var tagScore float64
		for _, tag := range entry.Tags {
			tagLower := strings.ToLower(tag)
			if tagLower == termLower {
				tagScore = 2.0
				break
			}
			if tagScore < 1.5 && strings.Contains(tagLower, termLower) {
				tagScore = 1.5
			}
		}
		score += tagScore
	}
	return score
}

// matchEntry checks if a memory entry matches any of the search terms (OR logic).
// Deprecated: use entryScore > 0 instead for ranked results.
func matchEntry(entry MemoryEntry, query string) bool {
	terms := splitTerms(query)
	if len(terms) == 0 {
		return true
	}
	return entryScore(entry, terms) > 0
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
