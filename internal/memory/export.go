package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// ExportData represents the full export format for memories
type ExportData struct {
	Version    int           `json:"version"`
	Entries    []MemoryEntry `json:"entries"`
	ExportedAt time.Time     `json:"exported_at"`
}

// Export exports all memory entries to a JSON file. Derived reflection
// catalogs are intentionally excluded because they can be rebuilt.
func (s *Store) Export(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.load()
	if err != nil {
		return fmt.Errorf("load brain: %w", err)
	}

	data := ExportData{
		Version:    1,
		Entries:    brain.Entries,
		ExportedAt: time.Now(),
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal export data: %w", err)
	}

	return os.WriteFile(path, jsonData, 0644)
}

// Import imports memories from a JSON file, merging with existing data.
// If overwrite is true, existing entries with the same ID are replaced.
func (s *Store) Import(path string, overwrite bool) (imported int, skipped int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read import file: %w", err)
	}

	var data ExportData
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, 0, fmt.Errorf("unmarshal import data: %w", err)
	}

	return s.importDataLocked(&data, overwrite)
}

// ImportData imports memories from an ExportData struct, merging with existing data.
// If overwrite is true, existing entries with the same ID are replaced.
func (s *Store) ImportData(data ExportData, overwrite bool) (imported int, skipped int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.importDataLocked(&data, overwrite)
}

// importDataLocked is the shared import logic, assumes s.mu is held.
func (s *Store) importDataLocked(data *ExportData, overwrite bool) (imported int, skipped int, err error) {
	brain, err := s.load()
	if err != nil {
		return 0, 0, fmt.Errorf("load brain: %w", err)
	}

	existingIDs := make(map[string]bool)
	for _, e := range brain.Entries {
		existingIDs[e.ID] = true
	}

	for _, entry := range data.Entries {
		if existingIDs[entry.ID] {
			if overwrite {
				for i, e := range brain.Entries {
					if e.ID == entry.ID {
						brain.Entries[i] = entry
						imported++
						break
					}
				}
			} else {
				skipped++
			}
			continue
		}
		brain.Entries = append(brain.Entries, entry)
		imported++
	}

	if err := s.save(brain); err != nil {
		return 0, 0, fmt.Errorf("save after import: %w", err)
	}

	return imported, skipped, nil
}
