package backup

import (
	"FrostAgent/internal/security"
	"FrostAgent/internal/storage"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// GlobalSecurity is a separate, typed backup of global access state and audit history.
type GlobalSecurity struct {
	FormatVersion int                     `json:"format_version"`
	AccessRecords []security.AccessRecord `json:"access_records"`
	AuditEvents   []security.AuditEvent   `json:"audit_events"`
}

func ExportGlobalSecurity(db *storage.DB) (GlobalSecurity, error) {
	result := GlobalSecurity{FormatVersion: FormatVersion}
	rows, err := db.SQL.QueryContext(context.Background(), `SELECT platform, user_id, state, reason,
		locked_at, updated_at, strike_times_json, last_blocked_hash, last_blocked_at
		FROM access_records ORDER BY principal_key`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item security.AccessRecord
		var lockedAt, updatedAt, strikes, blockedAt string
		if err := rows.Scan(&item.Principal.Platform, &item.Principal.UserID, &item.State,
			&item.Reason, &lockedAt, &updatedAt, &strikes, &item.LastBlockedHash, &blockedAt); err != nil {
			rows.Close()
			return result, err
		}
		if item.LockedAt, err = parseBackupTime(lockedAt); err != nil {
			rows.Close()
			return result, err
		}
		if item.UpdatedAt, err = parseBackupTime(updatedAt); err != nil {
			rows.Close()
			return result, err
		}
		if item.LastBlockedAt, err = parseBackupTime(blockedAt); err != nil {
			rows.Close()
			return result, err
		}
		if err := json.Unmarshal([]byte(strikes), &item.StrikeTimes); err != nil {
			rows.Close()
			return result, err
		}
		result.AccessRecords = append(result.AccessRecords, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	result.AuditEvents, err = security.NewSQLAuditStore(db, 1000).List(1000)
	return result, err
}

func ImportGlobalSecurity(db *storage.DB, data GlobalSecurity) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM access_records`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM security_audit`); err != nil {
		return err
	}
	for _, item := range data.AccessRecords {
		principal, err := security.NewPrincipal(item.Principal.Platform, item.Principal.UserID)
		if err != nil || principal != item.Principal || (item.State != security.AccessActive && item.State != security.AccessLocked) {
			return fmt.Errorf("invalid access record principal or state")
		}
		strikes, err := json.Marshal(item.StrikeTimes)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO access_records
			(principal_key, platform, user_id, state, reason, locked_at, updated_at,
			strike_times_json, last_blocked_hash, last_blocked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), principal.Key(), principal.Platform,
			principal.UserID, item.State, item.Reason, formatBackupTime(item.LockedAt),
			formatBackupTime(item.UpdatedAt), string(strikes), item.LastBlockedHash,
			formatBackupTime(item.LastBlockedAt)); err != nil {
			return err
		}
	}
	for _, event := range data.AuditEvents {
		principal, err := security.NewPrincipal(event.Principal.Platform, event.Principal.UserID)
		if err != nil || principal != event.Principal || event.ID == "" || event.At.IsZero() {
			return fmt.Errorf("invalid audit event")
		}
		encoded := 0
		if event.Encoded {
			encoded = 1
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO security_audit
			(id, occurred_at, platform, user_id, instance_id, session_id, tool, stage, source,
			action, reason, content_hash, preview, encoded, category, risk_level)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), event.ID,
			formatBackupTime(event.At), principal.Platform, principal.UserID,
			event.Instance, event.Session, event.Tool, event.Stage, event.Source, event.Action,
			event.Reason, event.Hash, event.Preview, encoded, event.Category, event.RiskLevel); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func parseBackupTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func formatBackupTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
