package backup

import (
	"context"
	"fmt"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

type ToolHistory struct {
	FormatVersion int                  `json:"format_version"`
	Records       []toolhistory.Record `json:"records"`
}

func ExportToolHistory(db *storage.DB, instanceID string) (ToolHistory, error) {
	records, err := toolhistory.NewStore(db, instanceID).Export(context.Background())
	return ToolHistory{FormatVersion: FormatVersion, Records: records}, err
}

func ValidateToolHistory(data ToolHistory) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range data.Records {
		if err := r.Validate(); err != nil {
			return err
		}
		if seen[r.RefID] {
			return fmt.Errorf("duplicate tool history reference")
		}
		seen[r.RefID] = true
	}
	return nil
}

func ImportToolHistory(db *storage.DB, instanceID string, data ToolHistory) error {
	if err := ValidateToolHistory(data); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	store := toolhistory.NewStore(db, instanceID)
	for _, r := range data.Records {
		if err := store.SaveTx(ctx, tx, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}
