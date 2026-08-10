package groupsummary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const fileVersion = 1

// Record is the latest durable running summary for one group session.
type Record struct {
	SessionID string    `json:"session_id"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type fileData struct {
	Version   int      `json:"version"`
	Summaries []Record `json:"summaries"`
}

// Store keeps group summaries outside brain.json. It is safe for concurrent
// use within one process; multiple processes must not share the same file.
type Store struct {
	path        string
	mu          sync.RWMutex
	records     map[string]Record
	generations map[string]uint64
	blockedErr  error
}

// NewStore loads the summary file. A malformed file blocks writes for the
// lifetime of this Store so recoverable data is never overwritten.
func NewStore(path string) (*Store, error) {
	store := &Store{
		path:        path,
		records:     make(map[string]Record),
		generations: make(map[string]uint64),
	}
	data, err := load(path)
	if err != nil {
		store.blockedErr = err
		return store, err
	}
	for _, record := range data.Summaries {
		record.SessionID = strings.TrimSpace(record.SessionID)
		record.Summary = strings.TrimSpace(record.Summary)
		if record.SessionID == "" || record.Summary == "" {
			continue
		}
		store.records[record.SessionID] = record
	}
	return store, nil
}

func load(path string) (fileData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileData{Version: fileVersion}, nil
		}
		return fileData{}, fmt.Errorf("read group summaries: %w", err)
	}

	var data fileData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fileData{}, fmt.Errorf("parse group summaries: %w", err)
	}
	if data.Version != 0 && data.Version != fileVersion {
		return fileData{}, fmt.Errorf("unsupported group summary version %d", data.Version)
	}
	return data, nil
}

// Get returns one persisted summary.
func (s *Store) Get(sessionID string) (Record, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockedErr != nil {
		return Record{}, false, s.blockedErr
	}
	record, ok := s.records[sessionID]
	return record, ok, nil
}

// List returns a stable snapshot ordered by most recently updated first.
func (s *Store) List() ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockedErr != nil {
		return nil, s.blockedErr
	}
	records := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].UpdatedAt.Equal(records[j].UpdatedAt) {
			return records[i].SessionID < records[j].SessionID
		}
		return records[i].UpdatedAt.After(records[j].UpdatedAt)
	})
	return records, nil
}

// Generation returns the deletion epoch used to reject stale compact writes.
func (s *Store) Generation(sessionID string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generations[sessionID]
}

// Upsert replaces one group's latest summary when its deletion epoch still
// matches. A false result means an administrator deleted it while compacting.
func (s *Store) Upsert(
	sessionID string,
	summary string,
	expectedGeneration uint64,
) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	summary = strings.TrimSpace(summary)
	if sessionID == "" || summary == "" {
		return false, fmt.Errorf("session ID and summary are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.generations[sessionID] != expectedGeneration {
		return false, nil
	}
	if s.blockedErr != nil {
		return false, s.blockedErr
	}

	now := time.Now()
	record := Record{
		SessionID: sessionID,
		Summary:   summary,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if existing, ok := s.records[sessionID]; ok {
		record.CreatedAt = existing.CreatedAt
	}

	next := cloneRecords(s.records)
	next[sessionID] = record
	if err := s.save(next); err != nil {
		return false, err
	}
	s.records = next
	return true, nil
}

// Delete removes one summary and advances its deletion epoch even if the disk
// operation fails, preventing an older in-flight compact from recreating it.
func (s *Store) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.generations[sessionID]++
	if s.blockedErr != nil {
		return s.blockedErr
	}
	if _, ok := s.records[sessionID]; !ok {
		return nil
	}

	next := cloneRecords(s.records)
	delete(next, sessionID)
	if err := s.save(next); err != nil {
		return err
	}
	s.records = next
	return nil
}

func cloneRecords(records map[string]Record) map[string]Record {
	cloned := make(map[string]Record, len(records))
	for id, record := range records {
		cloned[id] = record
	}
	return cloned
}

// save writes a complete replacement before atomically moving it over the
// live file. Rename failures are returned instead of falling back to truncation.
func (s *Store) save(records map[string]Record) error {
	summaries := make([]Record, 0, len(records))
	for _, record := range records {
		summaries = append(summaries, record)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].SessionID < summaries[j].SessionID
	})
	raw, err := json.MarshalIndent(fileData{
		Version:   fileVersion,
		Summaries: summaries,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal group summaries: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create group summary directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create group summary temp file: %w", err)
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(0o644); err != nil {
		return fmt.Errorf("set group summary permissions: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		return fmt.Errorf("write group summary temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync group summary temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close group summary temp file: %w", err)
	}
	closed = true
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace group summary file: %w", err)
	}
	return nil
}
