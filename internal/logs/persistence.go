package logs

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const DefaultPersistentBytes = 128 << 20
const LogBudgetKey = "LOG_STORAGE_MIB"

// Disk records contain their own attachments. An atomic rename commits the
// complete record; interrupted temporary files are never trusted on restart.
type diskImage struct {
	Hash        string `json:"hash"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
}
type diskRecord struct {
	Version int         `json:"version"`
	Entry   LogEntry    `json:"entry"`
	Images  []diskImage `json:"images,omitempty"`
}
type diskIndex struct {
	id     uint64
	bytes  int64
	images []string
}
type diskStore struct {
	dir       string
	budget    int64
	used      int64
	next      uint64
	entries   []diskIndex
	images    map[string]uint64
	failed    map[uint64]bool
	err       string
	loadError string
	closed    bool
}
type PersistenceStatus struct {
	Enabled     bool   `json:"enabled"`
	BudgetBytes int64  `json:"budget_bytes"`
	UsedBytes   int64  `json:"used_bytes"`
	Entries     int    `json:"entries"`
	Error       string `json:"error,omitempty"`
}

func PersistentBudget(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > 65536 {
		return DefaultPersistentBytes
	}
	return n << 20
}

// EnablePersistence is called once for an instance's long-lived logger. It is
// independent of the runtime and the business database.
func (s *Store) EnablePersistence(dir string, budget int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disk != nil {
		s.disk.closed = false
		s.disk.budget = int64(budget)
		return s.disk.evict()
	}
	p := &diskStore{dir: dir, budget: int64(budget), images: make(map[string]uint64), failed: make(map[uint64]bool)}
	s.disk = p
	if err := os.MkdirAll(dir, 0700); err != nil {
		p.err = "日志目录不可写，当前日志仅保留在内存"
		return err
	}
	var loadErr error
	if data, err := os.ReadFile(filepath.Join(dir, "sequence")); err == nil {
		p.used += int64(len(data))
		p.next, err = strconv.ParseUint(string(data), 10, 64)
		if err != nil {
			loadErr = errors.Join(loadErr, err)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".log-atomic-") {
			_ = os.Remove(filepath.Join(dir, f.Name()))
			continue
		}
		if filepath.Ext(f.Name()) != ".json" {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSuffix(f.Name(), ".json"), 10, 64)
		info, statErr := f.Info()
		if err != nil || statErr != nil || !info.Mode().IsRegular() {
			loadErr = errors.Join(loadErr, errors.New("invalid log file"))
			continue
		}
		p.next = max(p.next, id)
		p.used += info.Size()
		record, readErr := p.read(id)
		index := diskIndex{id: id, bytes: info.Size()}
		if readErr == nil && record.Entry.ID == id {
			for _, image := range record.Images {
				index.images = append(index.images, image.Hash)
			}
		} else {
			loadErr = errors.Join(loadErr, errors.New("unreadable log record"))
		}
		// Corrupt records still count against the budget and age out normally.
		p.entries = append(p.entries, index)
	}
	sort.Slice(p.entries, func(i, j int) bool { return p.entries[i].id < p.entries[j].id })
	for _, entry := range p.entries {
		for _, hash := range entry.images {
			p.images[hash] = entry.id
		}
	}
	s.nextEntryID = max(s.nextEntryID, p.next)
	loadErr = errors.Join(loadErr, p.evict())
	if loadErr != nil {
		p.loadError = "部分日志文件无法读取或清理；可读取日志仍然可用"
	}
	return loadErr
}

func (s *Store) StopPersistence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disk != nil {
		s.disk.closed = true
	}
}

func (p *diskStore) path(id uint64) string {
	return filepath.Join(p.dir, fmt.Sprintf("%020d.json", id))
}

func (p *diskStore) read(id uint64) (diskRecord, error) {
	var record diskRecord
	info, err := os.Lstat(p.path(id))
	if err != nil {
		return record, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return record, errors.New("invalid log file")
	}
	f, err := os.Open(p.path(id))
	if err != nil {
		return record, err
	}
	defer f.Close()
	err = json.NewDecoder(io.LimitReader(f, (256<<20)+1)).Decode(&record)
	if err == nil && record.Version != 1 {
		err = errors.New("unsupported log format")
	}
	return record, err
}

func logAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".log-atomic-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return atomicReplaceFile(f.Name(), path)
}

func (p *diskStore) append(entry LogEntry, images []retainedImage) error {
	record := diskRecord{Version: 1, Entry: entry}
	for _, image := range images {
		record.Images = append(record.Images, diskImage{image.hash, image.contentType, image.data})
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(p.dir, 0700); err != nil {
		return err
	}
	seq := []byte(strconv.FormatUint(entry.ID, 10))
	oldSeq, _ := os.Stat(filepath.Join(p.dir, "sequence"))
	if err = logAtomic(filepath.Join(p.dir, "sequence"), seq); err != nil {
		return err
	}
	if oldSeq != nil {
		p.used -= oldSeq.Size()
	}
	p.used += int64(len(seq))
	p.next = entry.ID
	if int64(len(data)+len(seq)) > p.budget {
		return errors.New("log record exceeds disk budget")
	}
	// Reclaim space before committing a new record, bounding steady disk use.
	if err = p.evictTo(p.budget - int64(len(data))); err != nil {
		return err
	}
	if err = logAtomic(p.path(entry.ID), data); err != nil {
		return err
	}
	index := diskIndex{id: entry.ID, bytes: int64(len(data))}
	for _, image := range images {
		index.images = append(index.images, image.hash)
		p.images[image.hash] = entry.ID
	}
	p.entries = append(p.entries, index)
	p.used += index.bytes
	return nil
}

func (p *diskStore) evict() error { return p.evictTo(p.budget) }
func (p *diskStore) evictTo(limit int64) error {
	for len(p.entries) > 0 && p.used > limit {
		old := p.entries[0]
		if err := os.Remove(p.path(old.id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		p.entries = p.entries[1:]
		p.used -= old.bytes
		for _, hash := range old.images {
			if p.images[hash] == old.id {
				delete(p.images, hash)
			}
		}
	}
	return nil
}

func (s *Store) SetPersistentBudget(budget int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disk == nil {
		return
	}
	s.disk.budget = int64(budget)
	if err := s.disk.evict(); err != nil {
		s.disk.err = "日志预算清理失败，请检查本地磁盘"
	}
}
func (s *Store) PersistenceStatus() PersistenceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.disk == nil {
		return PersistenceStatus{}
	}
	p := s.disk
	return PersistenceStatus{true, p.budget, p.used, len(p.entries), strings.Trim(strings.Join([]string{p.err, p.loadError}, "; "), "; ")}
}

func (p *diskStore) snapshot() []LogEntry {
	result := make([]LogEntry, 0, len(p.entries))
	for _, index := range p.entries {
		record, err := p.read(index.id)
		if err == nil {
			result = append(result, record.Entry)
		}
	}
	return result
}

func (p *diskStore) image(hash string) (*cachedImage, bool) {
	id, ok := p.images[hash]
	if !ok {
		return nil, false
	}
	record, err := p.read(id)
	if err != nil {
		return nil, false
	}
	for _, image := range record.Images {
		if image.Hash == hash && fmt.Sprintf("%x", sha256.Sum256(image.Data)) == hash {
			return &cachedImage{contentType: image.ContentType, data: image.Data}, true
		}
	}
	return nil, false
}
