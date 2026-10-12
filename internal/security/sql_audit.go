package security

import (
	"context"
	"time"
)

func (s *AuditStore) appendSQL(event AuditEvent) error {
	ctx := context.Background()
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	encoded := 0
	if event.Encoded {
		encoded = 1
	}
	if _, err := tx.ExecContext(ctx, s.db.Bind(`INSERT INTO security_audit
		(id, occurred_at, platform, user_id, instance_id, session_id, tool, stage,
		source, action, reason, content_hash, preview, encoded, category, risk_level)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		event.ID, event.At.UTC().Format(time.RFC3339Nano), event.Principal.Platform,
		event.Principal.UserID, event.Instance, event.Session, event.Tool, event.Stage,
		event.Source, event.Action, event.Reason, event.Hash, event.Preview,
		encoded, event.Category, event.RiskLevel); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM security_audit ORDER BY occurred_at DESC, id DESC`)
	if err != nil {
		return err
	}
	var remove []string
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		count++
		if count > s.limit {
			remove = append(remove, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range remove {
		if _, err := tx.ExecContext(ctx, s.db.Bind(`DELETE FROM security_audit WHERE id = ?`), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *AuditStore) listSQL(limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > s.limit {
		limit = s.limit
	}
	rows, err := s.db.SQL.QueryContext(context.Background(), s.db.Bind(`SELECT id, occurred_at,
		platform, user_id, instance_id, session_id, tool, stage, source, action,
		reason, content_hash, preview, encoded, category, risk_level
		FROM security_audit ORDER BY occurred_at DESC, id DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var at string
		var encoded int
		if err := rows.Scan(&event.ID, &at, &event.Principal.Platform, &event.Principal.UserID,
			&event.Instance, &event.Session, &event.Tool, &event.Stage, &event.Source,
			&event.Action, &event.Reason, &event.Hash, &event.Preview, &encoded,
			&event.Category, &event.RiskLevel); err != nil {
			return nil, err
		}
		if event.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		event.Encoded = encoded != 0
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}
