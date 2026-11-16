package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

type SessionStates struct {
	FormatVersion int                   `json:"format_version"`
	Records       []sessionstate.Record `json:"records"`
}

func ExportSessionStates(db *storage.DB, instanceID string) (SessionStates, error) {
	result := SessionStates{FormatVersion: FormatVersion}
	rows, err := db.SQL.Query(db.Bind(`SELECT session_id,revision,payload FROM session_states WHERE instance_id=? ORDER BY session_id`), instanceID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var record sessionstate.Record
		var payload string
		if err := rows.Scan(&record.SessionID, &record.Revision, &payload); err != nil {
			return result, err
		}
		record.Payload = json.RawMessage(payload)
		result.Records = append(result.Records, record)
	}
	return result, rows.Err()
}

func ValidateSessionStates(data SessionStates) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, record := range data.Records {
		var checkpoint llm.SessionCheckpoint
		if err := json.Unmarshal(record.Payload, &checkpoint); err != nil {
			return err
		}
		if record.SessionID == "" || record.Revision < 1 || checkpoint.Version != 1 || seen[record.SessionID] ||
			memory.CanonicalSessionKey(checkpoint.ConversationID) != record.SessionID {
			return fmt.Errorf("invalid or duplicate session checkpoint")
		}
		seen[record.SessionID] = true
	}
	return nil
}

// Instance ZIP restoration always creates a fresh instance and local directory.
func ImportSessionStates(db *storage.DB, instanceID string, data SessionStates) error {
	if err := ValidateSessionStates(data); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range data.Records {
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO session_states(instance_id,session_id,revision,payload) VALUES (?,?,?,?)`), instanceID, record.SessionID, record.Revision, string(record.Payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
