// Package toolhistory retains tool execution originals separately from model context.
package toolhistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/google/uuid"
)

const Prefix = "fa_toolcall_"

func NewRef() string { return Prefix + uuid.NewString() }

func ValidRef(ref string) bool {
	if !strings.HasPrefix(ref, Prefix) {
		return false
	}
	id, err := uuid.Parse(strings.TrimPrefix(ref, Prefix))
	return err == nil && id.Version() == 4 && id.Variant() == uuid.RFC4122 && Prefix+id.String() == ref
}

type Record struct {
	chathistory.Scope
	RefID           string    `json:"ref_id"`
	RunID           string    `json:"run_id"`
	SourceMessageID string    `json:"source_message_id,omitempty"`
	ActorID         string    `json:"actor_id"`
	Tool            string    `json:"tool"`
	ArgumentsRaw    string    `json:"arguments_raw"`
	ResultRaw       string    `json:"result_raw"`
	MCPResultRaw    string    `json:"mcp_result_raw,omitempty"`
	ModelResultRaw  string    `json:"model_result_raw"`
	Error           string    `json:"error,omitempty"`
	State           string    `json:"state"`
	Restricted      bool      `json:"restricted"`
	OccurredAt      time.Time `json:"occurred_at"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
}

func (r Record) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if r.Scope != r.Scope.Canonical() || !ValidRef(r.RefID) || r.RunID == "" || r.ActorID == "" || r.Tool == "" || r.OccurredAt.IsZero() {
		return fmt.Errorf("invalid tool history record")
	}
	switch r.State {
	case "running", "executed", "complete":
	default:
		return fmt.Errorf("invalid tool history state")
	}
	return nil
}

// ForModel preserves the quarantine boundary even when originals remain archived.
func (r Record) ForModel() Record {
	if r.Restricted || r.State != "complete" {
		r.ArgumentsRaw, r.ResultRaw, r.MCPResultRaw, r.Error = "", "", "", ""
	}
	return r
}

type Store struct {
	db         *storage.DB
	instanceID string
}

func NewStore(db *storage.DB, instanceID string) *Store { return &Store{db, instanceID} }

func (s *Store) Save(ctx context.Context, r Record) error { return s.SaveTx(ctx, s.db.SQL, r) }

func (s *Store) SaveTx(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, s.db.Bind(`INSERT INTO tool_history
		(instance_id, ref_id, platform, message_type, target_id, source_message_id, occurred_at, record_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (instance_id, ref_id) DO UPDATE SET record_json=excluded.record_json
		WHERE tool_history.platform=excluded.platform AND tool_history.message_type=excluded.message_type
		AND tool_history.target_id=excluded.target_id AND tool_history.source_message_id=excluded.source_message_id
		AND tool_history.occurred_at=excluded.occurred_at`),
		s.instanceID, r.RefID, r.Platform, r.MessageType, r.TargetID, r.SourceMessageID, r.OccurredAt.UnixMilli(), string(data))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("tool history reference identity conflict")
	}
	return nil
}

func (s *Store) Read(ctx context.Context, scope chathistory.Scope, ref string) (Record, error) {
	var r Record
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return r, err
	}
	if !ValidRef(ref) {
		return r, fmt.Errorf("invalid tool history reference")
	}
	var data string
	err := s.db.SQL.QueryRowContext(ctx, s.db.Bind(`SELECT record_json FROM tool_history
		WHERE instance_id=? AND platform=? AND message_type=? AND target_id=? AND ref_id=?`),
		s.instanceID, scope.Platform, scope.MessageType, scope.TargetID, ref).Scan(&data)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(data), &r)
	return r, err
}

func (s *Store) References(ctx context.Context, scope chathistory.Scope, messageID string) ([]string, error) {
	refs := []string{}
	if messageID == "" {
		return refs, nil
	}
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT ref_id FROM tool_history
		WHERE instance_id=? AND platform=? AND message_type=? AND target_id=? AND source_message_id=? ORDER BY occurred_at, ref_id`),
		s.instanceID, scope.Platform, scope.MessageType, scope.TargetID, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// Recent returns the newest records of one conversation scope, oldest first.
// Records are located through the covering scope index before any archived
// body is read, so a scope with many large tool results stays cheap to query.
func (s *Store) Recent(ctx context.Context, scope chathistory.Scope, limit int) ([]Record, error) {
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []Record{}, nil
	}
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT ref_id FROM tool_history
		WHERE instance_id=? AND platform=? AND message_type=? AND target_id=? ORDER BY occurred_at DESC, ref_id DESC LIMIT ?`),
		s.instanceID, scope.Platform, scope.MessageType, scope.TargetID, limit)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, limit)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	records := make([]Record, 0, len(refs))
	for i := len(refs) - 1; i >= 0; i-- {
		record, err := s.Read(ctx, scope, refs[i])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // Removed between the two reads.
			}
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// ReadForInspection is for the authenticated instance management API. Model
// tools must use Read, which additionally enforces their conversation scope.
func (s *Store) ReadForInspection(ctx context.Context, ref string) (Record, error) {
	var record Record
	if !ValidRef(ref) {
		return record, fmt.Errorf("invalid tool history reference")
	}
	var data string
	if err := s.db.SQL.QueryRowContext(ctx, s.db.Bind(`SELECT record_json FROM tool_history WHERE instance_id=? AND ref_id=?`), s.instanceID, ref).Scan(&data); err != nil {
		return record, err
	}
	err := json.Unmarshal([]byte(data), &record)
	return record, err
}

func (s *Store) Export(ctx context.Context) ([]Record, error) {
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT record_json FROM tool_history WHERE instance_id=? ORDER BY occurred_at, ref_id`), s.instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	for rows.Next() {
		var data string
		var r Record
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
