package chathistory

import (
	"context"
	"database/sql"
	"time"
)

type Coverage struct {
	Since, Until time.Time
	Count        int64
}

func (s *Store) Coverage(ctx context.Context, scope Scope) (Coverage, error) {
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return Coverage{}, err
	}
	var first, last sql.NullInt64
	var c Coverage
	err := s.db.SQL.QueryRowContext(ctx, s.db.Bind(`SELECT MIN(occurred_at),MAX(occurred_at),COUNT(*) FROM chat_history WHERE instance_id=? AND platform=? AND message_type=? AND target_id=?`), s.instanceID, scope.Platform, scope.MessageType, scope.TargetID).Scan(&first, &last, &c.Count)
	if first.Valid {
		c.Since = time.UnixMilli(first.Int64).UTC()
		c.Until = time.UnixMilli(last.Int64).UTC()
	}
	return c, err
}
func (s *Store) Scopes(ctx context.Context) ([]Scope, error) {
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT DISTINCT platform,message_type,target_id FROM chat_history WHERE instance_id=?`), s.instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Scope{}
	for rows.Next() {
		var scope Scope
		if err = rows.Scan(&scope.Platform, &scope.MessageType, &scope.TargetID); err != nil {
			return nil, err
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}
