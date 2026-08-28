package sticker

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	mu      sync.RWMutex
	dir     string
	index   map[string]*Entry
	ordered []string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create sticker dir: %w", err)
	}
	s := &Store{
		dir:   dir,
		index: make(map[string]*Entry),
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load index: %w", err)
	}
	return s, nil
}

func (s *Store) indexPath() string { return filepath.Join(s.dir, "index.json") }

func (s *Store) load() error {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		return err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("unmarshal index: %w", err)
	}
	s.index = make(map[string]*Entry, len(entries))
	s.ordered = make([]string, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		s.index[e.ID] = e
		s.ordered = append(s.ordered, e.ID)
	}
	return nil
}

func (s *Store) save() error {
	entries := make([]Entry, 0, len(s.ordered))
	for _, id := range s.ordered {
		if e, ok := s.index[id]; ok {
			entries = append(entries, *e)
		}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, s.indexPath()); err != nil {
		if wErr := os.WriteFile(s.indexPath(), data, 0644); wErr != nil {
			os.Remove(tmp)
			return fmt.Errorf("persist index: %w", wErr)
		}
		os.Remove(tmp)
	}
	return nil
}

func HashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func (s *Store) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.index[id]
	return ok
}

func (s *Store) Add(id, fileName string, fileData []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.index[id]; ok {
		return fmt.Errorf("sticker %s already exists", id)
	}

	filePath := filepath.Join(s.dir, fileName)
	if err := os.WriteFile(filePath, fileData, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	e := newEntry(id, fileName)
	s.index[id] = &e
	s.ordered = append(s.ordered, id)
	return s.save()
}

func (s *Store) IncrementWeight(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.index[id]
	if !ok {
		return fmt.Errorf("sticker %s not found", id)
	}
	e.Weight++
	e.UpdatedAt = time.Now().Unix()
	return s.save()
}

func (s *Store) Get(id string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.index[id]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Entry, 0, len(s.ordered))
	for _, id := range s.ordered {
		if e, ok := s.index[id]; ok {
			result = append(result, *e)
		}
	}
	return result
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.index[id]
	if !ok {
		return fmt.Errorf("sticker %s not found", id)
	}

	filePath := filepath.Join(s.dir, e.FileName)
	os.Remove(filePath)

	delete(s.index, id)
	for i, oid := range s.ordered {
		if oid == id {
			s.ordered = append(s.ordered[:i], s.ordered[i+1:]...)
			break
		}
	}
	return s.save()
}

func (s *Store) Update(id string, description string, keywords []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.index[id]
	if !ok {
		return fmt.Errorf("sticker %s not found", id)
	}
	e.Description = description
	e.Keywords = keywords
	e.Status = StatusReady
	e.UpdatedAt = time.Now().Unix()
	return s.save()
}

func (s *Store) SetStatus(id string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.index[id]
	if !ok {
		return fmt.Errorf("sticker %s not found", id)
	}
	e.Status = status
	e.UpdatedAt = time.Now().Unix()
	return s.save()
}

func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var st Stats
	for _, e := range s.index {
		st.Total++
		switch e.Status {
		case StatusReady:
			st.Ready++
		case StatusUnsummarized:
			st.Unsummarized++
		}
	}
	return st
}

func (s *Store) FilePath(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.index[id]
	if !ok {
		return "", false
	}
	return filepath.Join(s.dir, e.FileName), true
}

func (s *Store) Unsummarized() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Entry
	for _, id := range s.ordered {
		if e, ok := s.index[id]; ok && e.Status == StatusUnsummarized {
			result = append(result, *e)
		}
	}
	return result
}

func (s *Store) Dir() string { return s.dir }
