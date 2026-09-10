package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AccessState is the global state of a trusted platform principal.
type AccessState string

const (
	AccessActive AccessState = "ACTIVE"
	AccessLocked AccessState = "LOCKED"
)

// Principal is constructed only from trusted adapter metadata or an
// authenticated evaluation credential. Model content must never populate it.
type Principal struct {
	Platform string `json:"platform"`
	UserID   string `json:"user_id"`
}

// CanonicalPlatform maps transport names or adapter aliases to canonical user platform identities.
// For example, onebot, aiocqhttp, cqhttp, and qq all map to "qq".
func CanonicalPlatform(platform string) string {
	p := strings.ToLower(strings.TrimSpace(platform))
	switch p {
	case "onebot", "aiocqhttp", "cqhttp", "qq":
		return "qq"
	case "":
		return "unknown"
	default:
		return p
	}
}

func NewPrincipal(platform, userID string) (Principal, error) {
	platform = CanonicalPlatform(platform)
	userID = strings.TrimSpace(userID)
	if platform == "" || userID == "" || len(platform) > 64 || len(userID) > 256 {
		return Principal{}, errors.New("invalid security principal")
	}
	return Principal{Platform: platform, UserID: userID}, nil
}

func (p Principal) Key() string { return p.Platform + ":" + p.UserID }

type AccessRecord struct {
	Principal       Principal   `json:"principal"`
	State           AccessState `json:"state"`
	Reason          string      `json:"reason,omitempty"`
	LockedAt        time.Time   `json:"locked_at,omitempty"`
	UpdatedAt       time.Time   `json:"updated_at"`
	StrikeTimes     []time.Time `json:"strike_times,omitempty"`
	LastBlockedHash string      `json:"last_blocked_hash,omitempty"`
	LastBlockedAt   time.Time   `json:"last_blocked_at,omitempty"`
}

type accessFile struct {
	Version int                     `json:"version"`
	Records map[string]AccessRecord `json:"records"`
}

// AccessStore is a restart-persistent, process-wide access-control store.
// Reads reload the atomic file so separate FrostAgent processes sharing the
// same data directory observe lock and unlock operations immediately.
type AccessStore struct {
	path string
	mu   sync.RWMutex
}

func NewAccessStore(path string) *AccessStore { return &AccessStore{path: path} }
func (s *AccessStore) Path() string           { return s.path }

func (s *AccessStore) IsLocked(p Principal) (bool, AccessRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, err := s.load()
	if err != nil {
		return false, AccessRecord{}, err
	}
	record, ok := file.Records[p.Key()]
	return ok && record.State == AccessLocked, record, nil
}

func (s *AccessStore) LastBlockedHash(p Principal, cutoff time.Time) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, err := s.load()
	if err != nil {
		return ""
	}
	record, ok := file.Records[p.Key()]
	if !ok || record.LastBlockedAt.IsZero() || record.LastBlockedAt.Before(cutoff) {
		return ""
	}
	return record.LastBlockedHash
}

func (s *AccessStore) ListLocked() ([]AccessRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	file, err := s.load()
	if err != nil {
		return nil, err
	}
	result := make([]AccessRecord, 0)
	for _, record := range file.Records {
		if record.State == AccessLocked {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LockedAt.After(result[j].LockedAt) })
	return result, nil
}

func (s *AccessStore) Lock(p Principal, reason string) error {
	return s.update(func(file *accessFile) error {
		now := time.Now().UTC()
		record := file.Records[p.Key()]
		record.Principal = p
		record.State = AccessLocked
		record.Reason = truncate(reason, 512)
		record.UpdatedAt = now
		if record.LockedAt.IsZero() {
			record.LockedAt = now
		}
		file.Records[p.Key()] = record
		return nil
	})
}

func (s *AccessStore) Unlock(p Principal) error {
	return s.update(func(file *accessFile) error {
		if record, ok := file.Records[p.Key()]; ok {
			record.State = AccessActive
			record.UpdatedAt = time.Now().UTC()
			record.Reason = ""
			record.LockedAt = time.Time{}
			record.StrikeTimes = nil
			record.LastBlockedHash = ""
			record.LastBlockedAt = time.Time{}
			file.Records[p.Key()] = record
		}
		return nil
	})
}

// RecordBlockedSubmission atomically updates strike state shared by all
// instances. A first ambiguous block is not a strike. Repeating the same
// normalized payload or submitting an encoded variant within the window is.
func (s *AccessStore) RecordBlockedSubmission(
	p Principal,
	hash string,
	encoded bool,
	now time.Time,
	window time.Duration,
	lockThreshold int,
) (strikes int, locked bool, err error) {
	err = s.update(func(file *accessFile) error {
		record := file.Records[p.Key()]
		record.Principal = p
		if record.State == "" {
			record.State = AccessActive
		}
		cutoff := now.Add(-window)
		kept := record.StrikeTimes[:0]
		for _, at := range record.StrikeTimes {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		record.StrikeTimes = kept
		hasPriorBlock := !record.LastBlockedAt.IsZero() && record.LastBlockedAt.After(cutoff)
		repeated := hash != "" && hash == record.LastBlockedHash && hasPriorBlock
		// A first offense within the window (even if encoded or ambiguous) only blocks content
		// without accumulating strikes. Only active repeated attempts or encoding evasions after
		// a prior block within the window accumulate strikes toward locking.
		if hasPriorBlock && (repeated || encoded) {
			record.StrikeTimes = append(record.StrikeTimes, now.UTC())
		}
		record.LastBlockedHash = hash
		record.LastBlockedAt = now.UTC()
		record.UpdatedAt = now.UTC()
		strikes = len(record.StrikeTimes)
		if lockThreshold > 0 && strikes >= lockThreshold {
			record.State = AccessLocked
			record.LockedAt = now.UTC()
			record.Reason = "repeated active attempts to evade watchdog blocks"
			locked = true
		}
		file.Records[p.Key()] = record
		return nil
	})
	return strikes, locked, err
}

func (s *AccessStore) update(fn func(*accessFile) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer lock.release()

	file, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(&file); err != nil {
		return err
	}
	return s.save(file)
}

func (s *AccessStore) load() (accessFile, error) {
	file := accessFile{Version: 1, Records: make(map[string]AccessRecord)}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return file, nil
	}
	if err != nil {
		return file, fmt.Errorf("read security access store: %w", err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("parse security access store: %w", err)
	}
	if file.Version == 0 {
		file.Version = 1
	}
	if file.Version != 1 {
		return file, fmt.Errorf("unsupported security access store version %d", file.Version)
	}
	if file.Records == nil {
		file.Records = make(map[string]AccessRecord)
	}
	return file, nil
}

func (s *AccessStore) save(file accessFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode security access store: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create security data directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".security-access-*.tmp")
	if err != nil {
		return fmt.Errorf("create security access temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect security access temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write security access temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync security access temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close security access temp file: %w", err)
	}
	if err := atomicReplaceFile(tmpName, s.path); err != nil {
		return fmt.Errorf("replace security access store: %w", err)
	}
	return nil
}

func ContentHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

type fileLock struct {
	file *os.File
	path string
}

func (l *fileLock) release() {
	_ = l.file.Close()
	_ = os.Remove(l.path)
}

func acquireFileLock(path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create security lock directory: %w", err)
	}
	for i := 0; i < 300; i++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return &fileLock{file: file, path: path}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create security lock file: %w", err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(path)
			continue
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, errors.New("security store lock timeout")
}
