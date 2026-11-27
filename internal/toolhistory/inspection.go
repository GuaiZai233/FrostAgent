package toolhistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"time"
)

func (s *Store) ListForInspection(ctx context.Context, scope chathistory.Scope, limit int) ([]Record, chathistory.Coverage, error) {
	scope = scope.Canonical()
	var coverage chathistory.Coverage
	if err := scope.Validate(); err != nil {
		return nil, coverage, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	args := []any{s.instanceID, scope.Platform, scope.MessageType, scope.TargetID}
	where := `instance_id=? AND platform=? AND message_type=? AND target_id=?`
	var first, last sql.NullInt64
	err := s.db.SQL.QueryRowContext(ctx, s.db.Bind(`SELECT MIN(occurred_at),MAX(occurred_at),COUNT(*) FROM tool_history WHERE `+where), args...).Scan(&first, &last, &coverage.Count)
	if err != nil {
		return nil, coverage, err
	}
	if first.Valid {
		coverage.Since = time.UnixMilli(first.Int64).UTC()
		coverage.Until = time.UnixMilli(last.Int64).UTC()
	}
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT record_json FROM tool_history WHERE `+where+` ORDER BY occurred_at DESC,ref_id DESC LIMIT ?`), append(args, limit)...)
	if err != nil {
		return nil, coverage, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var raw string
		var record Record
		if err = rows.Scan(&raw); err != nil {
			return nil, coverage, err
		}
		if err = json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, coverage, err
		}
		out = append(out, record.ForModel())
	}
	return out, coverage, rows.Err()
}
