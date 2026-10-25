package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const DefaultAuditRetentionDays = 7

// Audit exposes metadata-only auditing to the FrostAgent control plane.
type Audit struct {
	mu     sync.Mutex
	writer *fileAudit
}

func NewAudit(dir string, retentionDays int) (*Audit, error) {
	if !filepath.IsAbs(dir) {
		return nil, ErrInvalid
	}
	if retentionDays == 0 {
		retentionDays = DefaultAuditRetentionDays
	}
	if retentionDays < 1 || retentionDays > 3650 {
		return nil, ErrInvalid
	}
	if err := ensureDataDirectory(dir); err != nil {
		return nil, ErrAudit
	}
	return &Audit{writer: &fileAudit{dir: dir, retentionDays: retentionDays, now: time.Now}}, nil
}
func (a *Audit) Record(op Operation) error {
	if !op.Identity.valid() || !validOutcome(op.Outcome) {
		return ErrInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writer.append(op)
}

type auditWriter interface {
	append(Operation) error
}

type fileAudit struct {
	dir           string
	retentionDays int
	now           func() time.Time
	syncDir       func(string) error
}

var auditFilename = regexp.MustCompile(`^audit-(\d{4}-\d{2}-\d{2})\.jsonl$`)
var auditTempFilename = regexp.MustCompile(`^\.audit-create-(\d{4}-\d{2}-\d{2})-[0-9]+$`)

// append is serialized by Service. Only these metadata fields are written;
// adding payload/error text to Operation cannot silently expand audit content.
func (a *fileAudit) append(op Operation) error {
	now := a.now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	// Daily rotation retains today and the preceding retentionDays-1 UTC days.
	cutoff := day.AddDate(0, 0, 1-a.retentionDays)
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return ErrAudit
	}
	for _, entry := range entries {
		matches := auditFilename.FindStringSubmatch(entry.Name())
		if matches == nil {
			matches = auditTempFilename.FindStringSubmatch(entry.Name())
		}
		if matches == nil {
			continue
		}
		date, err := time.Parse("2006-01-02", matches[1])
		if err == nil && date.Before(cutoff) {
			info, err := entry.Info()
			if err != nil {
				return ErrAudit
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if err := os.Remove(filepath.Join(a.dir, entry.Name())); err != nil {
				return ErrAudit
			}
		}
	}
	event := struct {
		At time.Time `json:"at"`
		Identity
		Outcome
	}{At: now, Identity: op.Identity, Outcome: op.Outcome}
	line, err := json.Marshal(event)
	if err != nil {
		return ErrAudit
	}
	path := filepath.Join(a.dir, "audit-"+day.Format("2006-01-02")+".jsonl")
	line = append(line, '\n')
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return ErrAudit
		}
		if err := appendAuditFile(path, line); err != nil {
			return ErrAudit
		}
	} else if !os.IsNotExist(err) {
		return ErrAudit
	} else if err := createAuditFile(a.dir, path, line); err != nil {
		return ErrAudit
	}
	syncDir := a.syncDir
	if syncDir == nil {
		syncDir = syncDataDirectory
	}
	if err := syncDir(a.dir); err != nil {
		return ErrAudit
	}
	return nil
}

func appendAuditFile(path string, line []byte) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return ErrAudit
	}
	n, writeErr := file.Write(line)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || n != len(line) || syncErr != nil || closeErr != nil {
		return ErrAudit
	}
	return nil
}

func createAuditFile(dir, path string, line []byte) error {
	date := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "audit-"), ".jsonl")
	file, err := os.CreateTemp(dir, ".audit-create-"+date+"-*")
	if err != nil {
		return ErrAudit
	}
	temporary := file.Name()
	// This is only the temporary file created by this call, never an audit
	// segment or caller-provided path. Dated crash leftovers use audit retention.
	defer os.Remove(temporary)
	n, writeErr := file.Write(line)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || n != len(line) || syncErr != nil || closeErr != nil {
		return ErrAudit
	}
	if err := publishAuditFile(temporary, path); err != nil {
		return ErrAudit
	}
	return nil
}
