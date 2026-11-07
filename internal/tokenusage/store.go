// Package tokenusage records provider-reported consumption without retaining
// prompts, user IDs, credentials, or response content.
package tokenusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

const Scope = "token_usage"

var Modules = []string{"dialogue", "subagent", "vision", "group_compact", "sticker_summary", "memory_extract", "reflection", "embedding", "security"}

// Counter retains known quantities alongside the number of unreported calls.
type Counter struct {
	Value               int64 `json:"value"`
	UnestimatedRequests int64 `json:"unestimated_requests"`
}

type Module struct {
	Module    string  `json:"module"`
	Requests  int64   `json:"requests"`
	Input     Counter `json:"input"`
	Output    Counter `json:"output"`
	Total     Counter `json:"total"`
	Cached    Counter `json:"cached"`
	CacheHits Counter `json:"cache_hits"`
}

type Snapshot struct {
	Version int      `json:"version"`
	Modules []Module `json:"modules"`
	// Incomplete marks known gaps caused by an accounting storage failure.
	Incomplete bool `json:"incomplete,omitempty"`
}

type Store struct {
	db         *storage.DB
	instanceID string
	path       string
	mu         sync.Mutex
	modules    map[string]Module
	incomplete atomic.Bool
}

// New uses a separate settings scope for bounded accounting rows, keeping
// usage outside editable/exported configuration without a schema migration.
// The file fallback supports legacy runtimes that have not selected SQL.
func New(db *storage.DB, instanceID, path string) (*Store, error) {
	s := &Store{db: db, instanceID: instanceID, path: path, modules: map[string]Module{}}
	if db == nil && path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			var snap Snapshot
			if err := json.Unmarshal(data, &snap); err != nil {
				return nil, err
			}
			if err := Validate(snap); err != nil {
				return nil, err
			}
			for _, row := range snap.Modules {
				s.modules[row.Module] = row
			}
			s.incomplete.Store(snap.Incomplete)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return s, nil
}

func knownModule(module string) bool {
	for _, name := range Modules {
		if name == module {
			return true
		}
	}
	return false
}

func (c *Counter) add(value int, known bool) {
	if !known || value < 0 {
		c.UnestimatedRequests++
		return
	}
	c.Value += int64(value)
}

func (m *Module) add(usage *core.Usage) {
	m.Requests++
	if usage == nil {
		m.Input.add(0, false)
		m.Output.add(0, false)
		m.Total.add(0, false)
		m.Cached.add(0, false)
		m.CacheHits.add(0, false)
		return
	}
	reported := usage.Reported
	if reported == nil {
		reported = &core.UsageReported{Prompt: true, Completion: true, Total: true}
	}
	m.Input.add(usage.PromptTokens, reported.Prompt)
	m.Output.add(usage.CompletionTokens, reported.Completion)
	m.Total.add(usage.TotalTokens, reported.Total)
	cached, known := 0, usage.CachedPromptTokens != nil
	if known {
		cached = *usage.CachedPromptTokens
		known = cached >= 0
	}
	m.Cached.add(cached, known)
	hit := 0
	if cached > 0 {
		hit = 1
	}
	m.CacheHits.add(hit, known)
}

// Record commits one response before the provider returns. Errors in accounting
// are surfaced separately and never change the model response or billing.
func (s *Store) Record(module string, usage *core.Usage) (err error) {
	if s == nil {
		return nil
	}
	defer func() {
		if err != nil {
			s.incomplete.Store(true)
			if markerErr := s.persistIncomplete(); markerErr != nil {
				err = errors.Join(err, fmt.Errorf("persist incomplete usage marker: %w", markerErr))
			}
		}
	}()
	if !knownModule(module) {
		return fmt.Errorf("unknown token usage module %q", module)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		row := s.modules[module]
		row.Module = module
		row.add(usage)
		s.modules[module] = row
		if s.path != "" {
			data, err := json.Marshal(s.snapshotMemory())
			if err != nil {
				return err
			}
			return instanceconfig.WriteAtomic(s.path, data, 0600)
		}
		return nil
	}
	// Use a separate bounded context: cancellation after the paid API response
	// must not discard accounting, and accounting must not hang shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var present int
	if err = tx.QueryRowContext(ctx, s.db.Bind(`SELECT COUNT(*) FROM instances WHERE id=? AND deleting=0`), s.instanceID).Scan(&present); err != nil {
		return err
	}
	if present != 1 {
		return sql.ErrNoRows
	}
	var raw string
	row := Module{Module: module}
	err = tx.QueryRowContext(ctx, s.db.Bind(`SELECT value FROM settings WHERE scope=? AND instance_id=? AND key=?`), Scope, s.instanceID, module).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &row); err != nil {
			return err
		}
	}
	if row.Module != module {
		return fmt.Errorf("token usage module mismatch")
	}
	if err := Validate(Snapshot{Version: 1, Modules: []Module{row}}); err != nil {
		return err
	}
	row.add(usage)
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, s.db.Bind(`INSERT INTO settings(scope,instance_id,key,value) VALUES(?,?,?,?)
 ON CONFLICT(scope,instance_id,key) DO UPDATE SET value=excluded.value`), Scope, s.instanceID, module, string(data))
	if err != nil {
		return err
	}
	if s.incomplete.Load() {
		if _, err = tx.ExecContext(ctx, s.db.Bind(`INSERT INTO settings(scope,instance_id,key,value) VALUES(?,?,?,?) ON CONFLICT(scope,instance_id,key) DO UPDATE SET value=excluded.value`), Scope, s.instanceID, "__incomplete", "true"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// persistIncomplete runs after the failed transaction has rolled back. The
// marker survives restart even when there is no subsequent model response.
// If storage itself is unavailable, this is best effort and the original
// accounting error still reaches the runtime log.
func (s *Store) persistIncomplete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		if s.path == "" {
			return nil
		}
		data, err := json.Marshal(s.snapshotMemory())
		if err != nil {
			return err
		}
		return instanceconfig.WriteAtomic(s.path, data, 0600)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.db.SQL.ExecContext(ctx, s.db.Bind(`INSERT INTO settings(scope,instance_id,key,value)
      SELECT ?,id,?,? FROM instances WHERE id=? AND deleting=0
      ON CONFLICT(scope,instance_id,key) DO UPDATE SET value=excluded.value`), Scope, "__incomplete", "true", s.instanceID)
	return err
}

func (s *Store) snapshotMemory() Snapshot {
	result := Snapshot{Version: 1, Incomplete: s.incomplete.Load()}
	for _, name := range Modules {
		row := s.modules[name]
		row.Module = name
		result.Modules = append(result.Modules, row)
	}
	return result
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	if s == nil {
		return Snapshot{Version: 1}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return s.snapshotMemory(), nil
	}
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT key,value FROM settings WHERE scope=? AND instance_id=?`), Scope, s.instanceID)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	values := map[string]Module{}
	incomplete := s.incomplete.Load()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return Snapshot{}, err
		}
		if key == "__incomplete" {
			if raw != "true" {
				return Snapshot{}, fmt.Errorf("invalid incomplete marker")
			}
			incomplete = true
			continue
		}
		if !knownModule(key) {
			return Snapshot{}, fmt.Errorf("unknown persisted token usage module")
		}
		var row Module
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return Snapshot{}, err
		}
		if key != row.Module {
			return Snapshot{}, fmt.Errorf("token usage module mismatch")
		}
		values[key] = row
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Version: 1, Incomplete: incomplete}
	for _, name := range Modules {
		row := values[name]
		row.Module = name
		result.Modules = append(result.Modules, row)
	}
	return result, Validate(result)
}

func Validate(snap Snapshot) error {
	if snap.Version != 1 {
		return fmt.Errorf("unsupported token usage format %d", snap.Version)
	}
	seen := map[string]bool{}
	for _, m := range snap.Modules {
		if !knownModule(m.Module) || seen[m.Module] || m.Requests < 0 {
			return fmt.Errorf("invalid token usage module")
		}
		seen[m.Module] = true
		for _, c := range []Counter{m.Input, m.Output, m.Total, m.Cached, m.CacheHits} {
			if c.Value < 0 || c.UnestimatedRequests < 0 || c.UnestimatedRequests > m.Requests {
				return fmt.Errorf("invalid token usage counter")
			}
		}
		if m.CacheHits.Value > m.Requests {
			return fmt.Errorf("invalid cache hit count")
		}
	}
	return nil
}

// Restore is used only for a newly created, stopped instance during full ZIP
// restore. Configuration-only imports and copies never copy consumption.
func (s *Store) Restore(ctx context.Context, snap Snapshot) error {
	if err := Validate(snap); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("token usage restore requires SQL")
	}
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, s.db.Bind(`DELETE FROM settings WHERE scope=? AND instance_id=?`), Scope, s.instanceID); err != nil {
		return err
	}
	for _, row := range snap.Modules {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, s.db.Bind(`INSERT INTO settings(scope,instance_id,key,value) VALUES(?,?,?,?)`), Scope, s.instanceID, row.Module, string(data)); err != nil {
			return err
		}
	}
	if snap.Incomplete {
		if _, err = tx.ExecContext(ctx, s.db.Bind(`INSERT INTO settings(scope,instance_id,key,value) VALUES(?,?,?,?)`), Scope, s.instanceID, "__incomplete", "true"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
