package backup

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/mcp"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/service/dialogue"
	settingsservice "FrostAgent/internal/service/settings"
	"FrostAgent/internal/storage"
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ImportSettings replaces all classified non-secret settings and the exported
// router, MCP, and dialogue configuration in one SQL transaction. Secrets in
// an uploaded file are ignored; model endpoint credentials are cleared.
func ImportSettings(db *storage.DB, instanceID string, data Settings) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var present int
	if err := tx.QueryRowContext(ctx, db.Bind(`SELECT COUNT(*) FROM instances WHERE id = ? AND deleting = 0`), instanceID).Scan(&present); err != nil {
		return err
	}
	if present != 1 {
		return sql.ErrNoRows
	}
	for key := range settingsservice.ExportableValueKeys() {
		if instanceconfig.GlobalKeys[key] && !instanceconfig.SharedKeys[key] {
			continue
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM settings
			WHERE scope = 'instance' AND instance_id = ? AND key = ?`), instanceID, key); err != nil {
			return err
		}
		if value, exists := data.Values[key]; exists {
			if strings.ContainsRune(value, 0) {
				return fmt.Errorf("setting %s contains NUL", key)
			}
			if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO settings(scope, instance_id, key, value)
				VALUES ('instance', ?, ?, ?)`), instanceID, key, value); err != nil {
				return err
			}
		}
	}
	if err := modelrouter.WriteImportedConfigurationTx(ctx, db, tx, instanceID, data.ModelRouter); err != nil {
		return err
	}
	for i := range data.MCP.Servers {
		data.MCP.Servers[i].Transport.URL = publicURL(data.MCP.Servers[i].Transport.URL)
	}
	if err := mcp.WriteImportedConfigTx(ctx, db, tx, instanceID, &data.MCP); err != nil {
		return err
	}
	if err := dialogue.WriteExamplesTx(ctx, db, tx, instanceID, data.Dialogues); err != nil {
		return err
	}
	return tx.Commit()
}
