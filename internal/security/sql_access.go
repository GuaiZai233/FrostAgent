package security

import (
	"context"
	"encoding/json"
	"time"
)

func sqlTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseSQLTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func (s *AccessStore) loadSQL() (accessFile, error) {
	file := accessFile{Version: 1, Records: make(map[string]AccessRecord)}
	rows, err := s.db.SQL.QueryContext(context.Background(), `SELECT principal_key, platform, user_id, state,
		reason, locked_at, updated_at, strike_times_json, last_blocked_hash, last_blocked_at
		FROM access_records`)
	if err != nil {
		return file, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, lockedAt, updatedAt, strikes, lastBlockedAt string
		var record AccessRecord
		if err := rows.Scan(&key, &record.Principal.Platform, &record.Principal.UserID,
			&record.State, &record.Reason, &lockedAt, &updatedAt, &strikes,
			&record.LastBlockedHash, &lastBlockedAt); err != nil {
			return file, err
		}
		if record.LockedAt, err = parseSQLTime(lockedAt); err != nil {
			return file, err
		}
		if record.UpdatedAt, err = parseSQLTime(updatedAt); err != nil {
			return file, err
		}
		if record.LastBlockedAt, err = parseSQLTime(lastBlockedAt); err != nil {
			return file, err
		}
		if err := json.Unmarshal([]byte(strikes), &record.StrikeTimes); err != nil {
			return file, err
		}
		file.Records[key] = record
	}
	return file, rows.Err()
}

func (s *AccessStore) saveSQL(file accessFile) error {
	ctx := context.Background()
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, record := range file.Records {
		strikes, err := json.Marshal(record.StrikeTimes)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.db.Bind(`INSERT INTO access_records
			(principal_key, platform, user_id, state, reason, locked_at, updated_at,
			strike_times_json, last_blocked_hash, last_blocked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (principal_key) DO UPDATE SET platform = excluded.platform,
			user_id = excluded.user_id, state = excluded.state, reason = excluded.reason,
			locked_at = excluded.locked_at, updated_at = excluded.updated_at,
			strike_times_json = excluded.strike_times_json,
			last_blocked_hash = excluded.last_blocked_hash,
			last_blocked_at = excluded.last_blocked_at`),
			key, record.Principal.Platform, record.Principal.UserID, record.State, record.Reason,
			sqlTime(record.LockedAt), sqlTime(record.UpdatedAt), string(strikes),
			record.LastBlockedHash, sqlTime(record.LastBlockedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
