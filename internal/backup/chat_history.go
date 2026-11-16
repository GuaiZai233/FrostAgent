package backup

import (
	"context"
	"fmt"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

type ChatHistory struct {
	FormatVersion int                  `json:"format_version"`
	Records       []chathistory.Record `json:"records"`
}

func ExportChatHistory(db *storage.DB, instanceID string) (ChatHistory, error) {
	records, err := chathistory.NewStore(db, instanceID).Export(context.Background())
	return ChatHistory{FormatVersion: FormatVersion, Records: records}, err
}

func ValidateChatHistory(data ChatHistory) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, record := range data.Records {
		if err := record.Validate(); err != nil {
			return err
		}
		if record.Scope != record.Scope.Canonical() || seen[record.ID] {
			return fmt.Errorf("duplicate or noncanonical chat history record")
		}
		seen[record.ID] = true
	}
	return nil
}

func ImportChatHistory(db *storage.DB, instanceID string, data ChatHistory) error {
	if err := ValidateChatHistory(data); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	store := chathistory.NewStore(db, instanceID)
	for _, record := range data.Records {
		if err := store.SaveTx(ctx, tx, record); err != nil {
			return err
		}
	}
	return tx.Commit()
}
