package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
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
	if entry.OwnerType == "" {
		entry.OwnerType = OwnerUser
	}

	brain.Entries = append(brain.Entries, entry)
	return s.save(brain)
}

// UpsertCompact keeps exactly one running compact entry per group owner.
func (s *Store) UpsertCompact(entry MemoryEntry) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return "", err
	}
	now := time.Now()
	entry.OwnerType = OwnerGroup
	entry.Source = SourceCompact
	entry.Visibility = VisibilityPrivate
	entry.UpdatedAt = now
	for i, existing := range brain.Entries {
		if existing.Owner == entry.Owner && existing.Source == SourceCompact {
			entry.ID = existing.ID
			entry.CreatedAt = existing.CreatedAt
			entry.AccessCount = existing.AccessCount
			entry.MergedFrom = slices.Clone(existing.MergedFrom)
			brain.Entries[i] = entry
			return entry.ID, s.save(brain)
		}
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	brain.Entries = append(brain.Entries, entry)
	return entry.ID, s.save(brain)
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
	applied, err := s.applyReflectionWithMerges(owner, nil, outdatedIDs, importanceUpdates)
	if err != nil {
		return nil, nil, err
	}
	return applied.Remaining, applied.RemovedIDs, nil
}

type reflectionApplyResult struct {
	Remaining         []MemoryEntry
	RemovedIDs        []string
	OutdatedIDs       []string
	MergedEntries     []MemoryEntry
	MergedSourceCount int
}

// applyReflectionWithMerges applies validated merge proposals together with
// the existing reflection updates. Merge sources are archived in full before
// they leave the active entry set, so a lossy LLM synthesis remains auditable.
// The store rechecks every source snapshot while holding the write lock; stale
// or cross-owner proposals are skipped without deleting their sources.
func (s *Store) applyReflectionWithMerges(
	owner string,
	merges []validatedMerge,
	outdatedIDs []string,
	importanceUpdates map[string]float64,
) (reflectionApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.load()
	if err != nil {
		return reflectionApplyResult{}, err
	}

	outdated := make(map[string]bool, len(outdatedIDs))
	for _, id := range outdatedIDs {
		outdated[id] = true
	}

	// A source mentioned by a merge must never be deleted as outdated in the
	// same cycle, even if the merge later fails its stale-snapshot check.
	for _, merge := range merges {
		for _, source := range merge.Sources {
			delete(outdated, source.ID)
		}
	}

	currentByID := make(map[string]MemoryEntry, len(brain.Entries))
	existingIDs := make(map[string]bool, len(brain.Entries))
	for _, entry := range brain.Entries {
		currentByID[entry.ID] = entry
		existingIDs[entry.ID] = true
	}

	now := time.Now()
	consumed := make(map[string]bool)
	mergedEntries := make([]MemoryEntry, 0, len(merges))
	archives := make([]MemoryMergeArchive, 0, len(merges))
	for _, merge := range merges {
		if len(merge.Sources) < 2 || len(merge.Sources) > maxMergeSources ||
			strings.TrimSpace(merge.Content) == "" || len([]rune(merge.Content)) > maxMergedContent ||
			len(merge.Tags) == 0 || len(merge.Tags) > maxMergedTags {
			continue
		}

		currentSources := make([]MemoryEntry, 0, len(merge.Sources))
		seenSources := make(map[string]bool, len(merge.Sources))
		valid := true
		for _, snapshot := range merge.Sources {
			current, ok := currentByID[snapshot.ID]
			if !ok || current.Owner != owner || seenSources[current.ID] || consumed[current.ID] ||
				!sameMergeSource(current, snapshot) {
				valid = false
				break
			}
			seenSources[current.ID] = true
			currentSources = append(currentSources, current)
		}
		if !valid {
			continue
		}

		mergedID := generateID()
		for existingIDs[mergedID] {
			mergedID = generateID()
		}
		existingIDs[mergedID] = true
		merged := buildMergedEntry(mergedID, owner, merge, currentSources, now)
		mergedEntries = append(mergedEntries, merged)
		archives = append(archives, MemoryMergeArchive{
			MergedID: mergedID,
			Owner:    owner,
			Sources:  currentSources,
			MergedAt: now,
		})
		for _, source := range currentSources {
			consumed[source.ID] = true
		}
	}

	remainingAll := make([]MemoryEntry, 0, len(brain.Entries)+len(mergedEntries))
	removedIDs := make([]string, 0, len(consumed)+len(outdated))
	appliedOutdated := make([]string, 0, len(outdated))
	for _, entry := range brain.Entries {
		if entry.Owner == owner {
			if consumed[entry.ID] {
				removedIDs = append(removedIDs, entry.ID)
				continue
			}
			if outdated[entry.ID] {
				removedIDs = append(removedIDs, entry.ID)
				appliedOutdated = append(appliedOutdated, entry.ID)
				continue
			}
			if importance, ok := importanceUpdates[entry.ID]; ok {
				entry.Importance = clampImportance(importance)
				entry.UpdatedAt = now
			}
		}
		remainingAll = append(remainingAll, entry)
	}
	remainingAll = append(remainingAll, mergedEntries...)
	brain.Entries = remainingAll
	brain.MergeArchives = append(brain.MergeArchives, archives...)

	// Always rewrite after reflection so legacy inline "summaries" fields are
	// removed from brain.json even when no importance or deletion changed.
	if err := s.save(brain); err != nil {
		return reflectionApplyResult{}, err
	}

	remaining := make([]MemoryEntry, 0)
	for _, entry := range brain.Entries {
		if entry.Owner == owner {
			remaining = append(remaining, entry)
		}
	}
	return reflectionApplyResult{
		Remaining:         remaining,
		RemovedIDs:        removedIDs,
		OutdatedIDs:       appliedOutdated,
		MergedEntries:     mergedEntries,
		MergedSourceCount: len(consumed),
	}, nil
}

func sameMergeSource(current MemoryEntry, snapshot MemoryEntry) bool {
	return current.ID == snapshot.ID &&
		current.Owner == snapshot.Owner &&
		current.Content == snapshot.Content &&
		slices.Equal(current.Tags, snapshot.Tags) &&
		current.Source == snapshot.Source &&
		current.Visibility == snapshot.Visibility &&
		current.Importance == snapshot.Importance &&
		current.CreatedAt.Equal(snapshot.CreatedAt) &&
		current.UpdatedAt.Equal(snapshot.UpdatedAt) &&
		current.AccessCount == snapshot.AccessCount &&
		slices.Equal(current.MergedFrom, snapshot.MergedFrom)
}

func buildMergedEntry(
	id string,
	owner string,
	merge validatedMerge,
	sources []MemoryEntry,
	now time.Time,
) MemoryEntry {
	visibility := VisibilityPublic
	importance := 0.0
	accessCount := 0
	mergedFrom := make([]string, 0, len(sources))
	for _, source := range sources {
		mergedFrom = append(mergedFrom, source.ID)
		if source.Visibility != VisibilityPublic {
			visibility = VisibilityPrivate
		}
		if source.Importance > importance {
			importance = source.Importance
		}
		if source.AccessCount > accessCount {
			accessCount = source.AccessCount
		}
	}
	sort.Strings(mergedFrom)

	return MemoryEntry{
		ID:          id,
		Owner:       owner,
		Content:     merge.Content,
		Tags:        slices.Clone(merge.Tags),
		Source:      SourceReflect,
		Visibility:  visibility,
		Importance:  clampImportance(importance),
		CreatedAt:   now,
		UpdatedAt:   now,
		AccessCount: accessCount,
		MergedFrom:  mergedFrom,
	}
}

func clampImportance(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
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
			updated.Owner = entry.Owner
			updated.Source = entry.Source
			updated.CreatedAt = entry.CreatedAt
			updated.AccessCount = entry.AccessCount
			updated.MergedFrom = slices.Clone(entry.MergedFrom)
			updated.UpdatedAt = time.Now()
			brain.Entries[i] = updated
			return s.save(brain)
		}
	}
	return fmt.Errorf("memory %s not found", updated.ID)
}
