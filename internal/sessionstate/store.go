// Package sessionstate stores session checkpoints independently of chat archives.
package sessionstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

type Record struct {
	SessionID string          `json:"session_id"`
	Revision  int64           `json:"revision"`
	Payload   json.RawMessage `json:"payload"`
}

var ErrConflict = errors.New("session checkpoint version conflict")
var ErrUnavailable = errors.New("session checkpoint unavailable")

type conflictError struct{ remote Record }

func (e *conflictError) Error() string { return ErrConflict.Error() }
func (e *conflictError) Unwrap() error { return ErrConflict }

type Status struct {
	Pending      int    `json:"pending"`
	Error        string `json:"error,omitempty"`
	LocalPending int    `json:"local_pending"`
}

type Store struct {
	db           *storage.DB
	instanceID   string
	dir          string
	mu           sync.Mutex
	flushMu      sync.Mutex
	pending      map[string]Record
	localPending map[string]Record
	latest       map[string]int64
	conflicts    map[string]Record
	readErrors   map[string]error
	lastError    string
	localError   string
	loadError    string
	validate     func(Record) error
	closed       bool
	report       func(string)
	write        func(context.Context, Record) error
}

func New(db *storage.DB, instanceID, dir string, report func(string)) *Store {
	s := &Store{db: db, instanceID: instanceID, dir: dir, pending: make(map[string]Record), localPending: make(map[string]Record), latest: make(map[string]int64), conflicts: make(map[string]Record), readErrors: make(map[string]error), report: report}
	s.write = s.writeSQL
	return s
}

// SetValidator must be configured before Load or Save is called.
func (s *Store) SetValidator(validate func(Record) error) { s.validate = validate }

func (s *Store) valid(record Record) bool {
	return record.SessionID != "" && record.Revision > 0 && json.Valid(record.Payload) && (s.validate == nil || s.validate(record) == nil)
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
}

// Load merges SQL and local records. No original chat archive is consulted.
// Source errors are returned alongside all records that remain readable.
func (s *Store) Load(ctx context.Context) ([]Record, error) {
	merged := make(map[string]Record)
	var loadErr error
	if s.db != nil {
		rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT session_id, revision, payload FROM session_states WHERE instance_id = ?`), s.instanceID)
		if err != nil {
			loadErr = err
		} else {
			for rows.Next() {
				var record Record
				var payload string
				if err = rows.Scan(&record.SessionID, &record.Revision, &payload); err != nil {
					loadErr = err
					break
				}
				record.Payload = json.RawMessage(payload)
				var compact bytes.Buffer
				if json.Compact(&compact, record.Payload) == nil {
					record.Payload = compact.Bytes()
				}
				if !s.valid(record) {
					loadErr = errors.Join(loadErr, errors.New("invalid SQL checkpoint"))
					continue
				}
				merged[record.SessionID] = record
			}
			loadErr = errors.Join(loadErr, rows.Err())
			_ = rows.Close()
		}
	}
	files, err := os.ReadDir(s.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		loadErr = errors.Join(loadErr, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".conflict" {
			loadErr = errors.Join(loadErr, ErrConflict)
			continue
		}
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.dir, file.Name())
		data, readErr := readFile(path)
		var record Record
		if readErr == nil {
			readErr = json.Unmarshal(data, &record)
		}
		if readErr != nil || !s.valid(record) || s.path(record.SessionID) != path {
			loadErr = errors.Join(loadErr, fmt.Errorf("invalid local session checkpoint %s", file.Name()))
			continue
		}
		if old, exists := merged[record.SessionID]; !exists || record.Revision > old.Revision {
			merged[record.SessionID] = record
			s.pending[record.SessionID] = record
		} else if record.Revision == old.Revision && !equalPayload(record.Payload, old.Payload) {
			loadErr = errors.Join(loadErr, errors.New("conflicting session checkpoint revisions"))
			// Preserve the local alternative before replacing its mirror with SQL.
			// Conflict files are diagnostic data, never automatic replay sources.
			if err := s.preserveConflict(record); err != nil {
				loadErr = errors.Join(loadErr, fmt.Errorf("preserve conflicting checkpoint: %w", err))
				// Keep the readable local record when its alternative cannot be saved.
				merged[record.SessionID] = record
			}
		}
	}
	result := make([]Record, 0, len(merged))
	for _, record := range merged {
		s.latest[record.SessionID] = record.Revision
		// SQL-only checkpoints must also survive a subsequent database outage.
		data, err := json.Marshal(record)
		if err == nil {
			err = writeAtomic(s.path(record.SessionID), data)
		}
		if err != nil {
			s.localPending[record.SessionID] = record
			s.localError = "本地会话快照写入失败"
		}
		result = append(result, record)
	}
	if loadErr != nil {
		s.loadError = "会话存储读取异常，已恢复仍可读取的快照"
	}
	return result, loadErr
}

// Save syncs an atomic local mirror before queuing SQL work. Delayed writers
// cannot replace a newer conversation state or a reset checkpoint.
func (s *Store) Save(record Record) error {
	if !s.valid(record) {
		return errors.New("invalid session checkpoint")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session checkpoint store closed")
	}
	if err := s.readErrors[record.SessionID]; err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if s.latest[record.SessionID] >= record.Revision {
		return nil
	}
	data, err := json.Marshal(record)
	if err == nil {
		err = writeAtomic(s.path(record.SessionID), data)
	}
	// SQL can still succeed when the local disk is unavailable. Report the
	// missing local durability while retaining the latest pending state.
	s.latest[record.SessionID] = record.Revision
	s.pending[record.SessionID] = record
	if err != nil {
		s.localPending[record.SessionID] = record
		s.localError = "本地会话快照写入失败，未提交内容可能无法跨重启恢复"
	} else {
		delete(s.localPending, record.SessionID)
		if len(s.localPending) == 0 {
			s.localError = ""
		}
	}
	return err
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return atomicReplaceFile(name, path)
}

func equalPayload(a, b json.RawMessage) bool {
	var left, right bytes.Buffer
	return json.Compact(&left, a) == nil && json.Compact(&right, b) == nil && bytes.Equal(left.Bytes(), right.Bytes())
}

func (s *Store) readSQL(ctx context.Context, id string) (Record, error) {
	if s.db == nil {
		return Record{}, errors.New("session database unavailable")
	}
	record := Record{SessionID: id}
	var payload string
	err := s.db.SQL.QueryRowContext(ctx, s.db.Bind(`SELECT revision, payload FROM session_states WHERE instance_id = ? AND session_id = ?`),
		s.instanceID, id).Scan(&record.Revision, &payload)
	if err != nil {
		return Record{}, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(payload)); err != nil {
		return Record{}, err
	}
	record.Payload = compact.Bytes()
	if !s.valid(record) {
		return Record{}, errors.New("invalid SQL checkpoint")
	}
	return record, nil
}

func (s *Store) writeSQL(ctx context.Context, record Record) error {
	if s.db == nil {
		return errors.New("session database unavailable")
	}
	result, err := s.db.SQL.ExecContext(ctx, s.db.Bind(`INSERT INTO session_states(instance_id, session_id, revision, payload)
		VALUES (?, ?, ?, ?) ON CONFLICT(instance_id, session_id) DO UPDATE
		SET revision = excluded.revision, payload = excluded.payload WHERE session_states.revision < excluded.revision`),
		s.instanceID, record.SessionID, record.Revision, string(record.Payload))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected > 0 {
		return err
	}
	remote, err := s.readSQL(ctx, record.SessionID)
	if err != nil {
		return err
	}
	if remote.Revision == record.Revision && equalPayload(remote.Payload, record.Payload) {
		return nil
	}
	return &conflictError{remote: remote}
}

func (s *Store) preserveConflict(record Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("%s.%x.conflict", s.path(record.SessionID), sha256.Sum256(data))
	return writeAtomic(path, data)
}

func (s *Store) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	var localErr, conflictErr error
	for id, record := range s.localPending {
		data, err := json.Marshal(record)
		if err == nil {
			err = writeAtomic(s.path(id), data)
		}
		if err != nil {
			localErr = errors.Join(localErr, err)
		} else {
			delete(s.localPending, id)
		}
	}
	if len(s.localPending) == 0 {
		s.localError = ""
	}
	batch := make([]Record, 0, len(s.pending))
	for id, record := range s.pending {
		if _, conflicted := s.conflicts[id]; conflicted {
			conflictErr = ErrConflict
			continue
		}
		batch = append(batch, record)
	}
	s.mu.Unlock()
	var sqlErr error
	for _, record := range batch {
		if err := s.write(ctx, record); err != nil {
			var conflict *conflictError
			if errors.As(err, &conflict) {
				s.mu.Lock()
				s.conflicts[record.SessionID] = conflict.remote
				// Retain both branches before any later local revision is written.
				conflictErr = errors.Join(conflictErr, err, s.preserveConflict(record), s.preserveConflict(conflict.remote))
				s.mu.Unlock()
			} else {
				sqlErr = errors.Join(sqlErr, err)
			}
			continue
		}
		s.mu.Lock()
		if current, exists := s.pending[record.SessionID]; exists && current.Revision == record.Revision {
			delete(s.pending, record.SessionID)
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	if sqlErr != nil {
		s.lastError = "数据库暂时不可用，会话使用本地快照，聊天继续"
	} else {
		s.lastError = ""
	}
	s.mu.Unlock()
	return errors.Join(localErr, sqlErr, conflictErr)
}

// Close fences late callbacks before an instance directory is removed.
// A cancelled instance deletion constructs a fresh store from durable records.
func (s *Store) Close() {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	var recoveryError, conflictError string
	if len(s.readErrors) > 0 {
		recoveryError = "部分会话快照无法恢复，已阻止这些会话覆盖历史，请检查存储状态"
	}
	if len(s.conflicts) > 0 {
		conflictError = "会话快照版本冲突，已保留待提交内容并暂停这些会话的 SQL 写入，请检查冲突副本"
	}
	return Status{Pending: len(s.pending), LocalPending: len(s.localPending), Error: strings.Join(nonempty(s.localError, s.lastError, s.loadError, recoveryError, conflictError), "; ")}
}

func nonempty(values ...string) []string {
	var result []string
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

// Read uses the latest queued revision before consulting durable copies. A
// missing or corrupt local mirror must never turn an existing ID into a new one.
func (s *Store) Read(id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Record{}, errors.New("session checkpoint store closed")
	}
	if record, ok := s.pending[id]; ok {
		delete(s.readErrors, id)
		return record, nil
	}
	if record, ok := s.localPending[id]; ok {
		delete(s.readErrors, id)
		return record, nil
	}
	data, err := readFile(s.path(id))
	var record Record
	if err == nil {
		err = json.Unmarshal(data, &record)
	}
	if err == nil && (record.SessionID != id || record.Revision != s.latest[id] || !s.valid(record)) {
		err = errors.New("invalid session checkpoint")
	}
	if err == nil {
		delete(s.readErrors, id)
		return record, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	remote, sqlErr := s.readSQL(ctx, id)
	if sqlErr == nil && remote.Revision < s.latest[id] {
		sqlErr = errors.New("SQL checkpoint is older than the missing local revision")
	}
	if sqlErr != nil {
		s.readErrors[id] = errors.Join(err, sqlErr)
		return Record{}, fmt.Errorf("%w: %v", ErrUnavailable, s.readErrors[id])
	}
	s.latest[id] = remote.Revision
	delete(s.readErrors, id)
	data, localErr := json.Marshal(remote)
	if localErr == nil {
		localErr = writeAtomic(s.path(id), data)
	}
	if localErr != nil {
		s.localPending[id] = remote
		s.localError = "本地会话快照写入失败，已从数据库恢复"
	}
	return remote, nil
}

func readFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return nil, errors.New("invalid checkpoint file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, (256<<20)+1))
}

func (s *Store) Revision(id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest[id]
}

// Records includes local revisions still waiting for SQL, for complete backups.
func (s *Store) Records() ([]Record, error) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.latest))
	for id := range s.latest {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	result := make([]Record, 0, len(ids))
	for _, id := range ids {
		record, err := s.Read(id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SessionID < result[j].SessionID })
	return result, nil
}

func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastReport := time.Time{}
	for {
		select {
		case <-ctx.Done():
			drain, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.Flush(drain)
			cancel()
			return
		case <-ticker.C:
			writeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := s.Flush(writeCtx)
			cancel()
			if err != nil && s.report != nil && time.Since(lastReport) > time.Minute {
				s.report(s.Status().Error)
				lastReport = time.Now()
			}
		}
	}
}
