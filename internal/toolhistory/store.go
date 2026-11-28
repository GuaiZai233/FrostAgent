// Package toolhistory retains tool execution originals separately from model context.
package toolhistory

import (
	"context"
	"database/sql"
	"encoding/json"
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

// DiscardUnstarted removes a reservation whose caller has prevented execution.
func (s *Store) DiscardUnstarted(ctx context.Context, scope chathistory.Scope, ref string) error {
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return err
	}
	if !ValidRef(ref) {
		return fmt.Errorf("invalid tool history reference")
	}
	_, err := s.db.SQL.ExecContext(ctx, s.db.Bind(`DELETE FROM tool_history
		WHERE instance_id=? AND platform=? AND message_type=? AND target_id=? AND ref_id=?`),
		s.instanceID, scope.Platform, scope.MessageType, scope.TargetID, ref)
	return err
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

// RecentSource is the newest part of one source message's calls.
type RecentSource struct {
	SourceMessageID string
	// Total counts every archived call of the source message in the scope.
	Total int
	// Refs holds at most the requested number of newest calls, newest first.
	Refs []string
}

// RecentBySource returns the maxSources source messages with the newest calls in
// one conversation scope (newest first), and for each its newest perSource
// references. Selection is per source and answered from the covering scope index
// alone, so one source with many calls can never crowd the others out and no
// archived body is read.
func (s *Store) RecentBySource(ctx context.Context, scope chathistory.Scope, maxSources, perSource int) ([]RecentSource, error) {
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	sources := []RecentSource{}
	if maxSources <= 0 || perSource <= 0 {
		return sources, nil
	}
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT source_message_id, COUNT(*) FROM tool_history
		WHERE instance_id=? AND platform=? AND message_type=? AND target_id=?
		GROUP BY source_message_id ORDER BY MAX(occurred_at) DESC, source_message_id DESC LIMIT ?`),
		s.instanceID, scope.Platform, scope.MessageType, scope.TargetID, maxSources)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var source RecentSource
		if err := rows.Scan(&source.SourceMessageID, &source.Total); err != nil {
			rows.Close()
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range sources {
		refs, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT ref_id FROM tool_history
			WHERE instance_id=? AND platform=? AND message_type=? AND target_id=? AND source_message_id=?
			ORDER BY occurred_at DESC, ref_id DESC LIMIT ?`),
			s.instanceID, scope.Platform, scope.MessageType, scope.TargetID, sources[i].SourceMessageID, perSource)
		if err != nil {
			return nil, err
		}
		for refs.Next() {
			var ref string
			if err := refs.Scan(&ref); err != nil {
				refs.Close()
				return nil, err
			}
			sources[i].Refs = append(sources[i].Refs, ref)
		}
		err = refs.Err()
		refs.Close()
		if err != nil {
			return nil, err
		}
	}
	return sources, nil
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

// SourceCall is the metadata of one archived call.
type SourceCall struct {
	RefID string
	Tool  string
}

// SourceCalls is the newest part of one source message's calls, oldest first.
type SourceCalls struct {
	SourceMessageID string
	// Total counts every archived call of the source message in the scope.
	Total int
	Calls []SourceCall
}

// toolNameExpr extracts the tool name inside the database, so no archived body
// is transferred or decoded in Go.
func (s *Store) toolNameExpr(column string) string {
	if s.db.Backend == storage.Postgres {
		return "(" + column + "::jsonb)->>'tool'"
	}
	return "json_extract(" + column + ", '$.tool')"
}

// CallsBySources returns, for each requested source message that has archived
// calls in one conversation scope, its exact call total and its newest perSource
// calls in chronological order. Sources without calls are omitted and the result
// order is unspecified. A single statement answers all sources, so each total
// and its calls come from one snapshot, and only metadata leaves the database.
func (s *Store) CallsBySources(ctx context.Context, scope chathistory.Scope, sourceIDs []string, perSource int) ([]SourceCalls, error) {
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	sources := []SourceCalls{}
	args := []any{s.instanceID, scope.Platform, scope.MessageType, scope.TargetID}
	seen := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			args = append(args, id)
		}
	}
	if perSource <= 0 || len(args) == 4 {
		return sources, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)-4), ",")
	args = append(args, s.instanceID, perSource)
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT t.source_message_id, t.ref_id, t.total, `+s.toolNameExpr("h.record_json")+`
		FROM (SELECT source_message_id, ref_id,
				COUNT(*) OVER (PARTITION BY source_message_id) AS total,
				ROW_NUMBER() OVER (PARTITION BY source_message_id ORDER BY occurred_at DESC, ref_id DESC) AS rn
			FROM tool_history
			WHERE instance_id=? AND platform=? AND message_type=? AND target_id=? AND source_message_id IN (`+placeholders+`)) t
		JOIN tool_history h ON h.instance_id=? AND h.ref_id=t.ref_id
		WHERE t.rn <= ?
		ORDER BY t.source_message_id, t.rn DESC`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var call SourceCall
		var total int
		var tool sql.NullString
		if err := rows.Scan(&source, &call.RefID, &total, &tool); err != nil {
			return nil, err
		}
		if !tool.Valid || tool.String == "" {
			continue
		}
		call.Tool = tool.String
		if n := len(sources); n == 0 || sources[n-1].SourceMessageID != source {
			sources = append(sources, SourceCalls{SourceMessageID: source, Total: total})
		}
		sources[len(sources)-1].Calls = append(sources[len(sources)-1].Calls, call)
	}
	return sources, rows.Err()
}
