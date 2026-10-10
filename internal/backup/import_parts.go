package backup

import (
	"FrostAgent/internal/storage"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ImportSummaries(db *storage.DB, instanceID string, data Summaries) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM group_summaries WHERE instance_id = ?`), instanceID); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, record := range data.Records {
		if record.SessionID == "" || record.Summary == "" || seen[record.SessionID] {
			return fmt.Errorf("empty or duplicate group summary %q", record.SessionID)
		}
		seen[record.SessionID] = true
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO group_summaries
			(instance_id, platform, group_id, summary, generation, created_at, updated_at)
			VALUES (?, 'qq', ?, ?, 0, ?, ?)`), instanceID, record.SessionID, record.Summary,
			record.CreatedAt.UTC().Format(time.RFC3339Nano), record.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ImportStickers(db *storage.DB, instanceID, imageDir string, data Stickers, images map[string][]byte) (resultErr error) {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	if err := os.MkdirAll(imageDir, 0700); err != nil {
		return err
	}
	seenIDs, seenFiles := map[string]bool{}, map[string]bool{}
	for _, entry := range data.Entries {
		if entry.ID == "" || seenIDs[entry.ID] || !validStickerFilename(entry.FileName) || seenFiles[strings.ToLower(entry.FileName)] {
			return fmt.Errorf("invalid or duplicate sticker %q", entry.ID)
		}
		seenIDs[entry.ID], seenFiles[strings.ToLower(entry.FileName)] = true, true
		if _, ok := images[entry.FileName]; !ok {
			return fmt.Errorf("sticker image %q is missing", entry.FileName)
		}
	}
	if len(images) != len(seenFiles) {
		return fmt.Errorf("sticker ZIP has unexpected images")
	}
	written := make([]string, 0, len(data.Entries))
	defer func() {
		if resultErr != nil {
			for _, path := range written {
				_ = os.Remove(path)
			}
		}
	}()
	for _, entry := range data.Entries {
		path := filepath.Join(imageDir, entry.FileName)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			if err == nil {
				return fmt.Errorf("sticker image %q already exists", entry.FileName)
			}
			return err
		}
		if err := os.WriteFile(path, images[entry.FileName], 0600); err != nil {
			return err
		}
		written = append(written, path)
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM sticker_entries WHERE instance_id = ?`), instanceID); err != nil {
		return err
	}
	for position, entry := range data.Entries {
		model, manual := 0, 0
		if entry.ModelSuspected {
			model = 1
		}
		if entry.ManualBlocked {
			manual = 1
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO sticker_entries
			(instance_id, id, file_name, description, weight, status, model_suspected, manual_blocked,
			created_at, updated_at, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			instanceID, entry.ID, entry.FileName, entry.Description, entry.Weight, entry.Status,
			model, manual, entry.CreatedAt, entry.UpdatedAt, position); err != nil {
			return err
		}
		for position, keyword := range entry.Keywords {
			if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO sticker_keywords
				(instance_id, sticker_id, position, keyword) VALUES (?, ?, ?, ?)`),
				instanceID, entry.ID, position, keyword); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func validStickerFilename(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\`)
}

// RemapEndpointIDsForInstance prevents a restored copy from claiming the
// source instance's globally reserved endpoint IDs.
func RemapEndpointIDsForInstance(settings *Settings, prefix string) error {
	refs := make(map[string]string)
	for i := range settings.ModelRouter.Endpoints {
		endpoint := &settings.ModelRouter.Endpoints[i]
		if endpoint.ID == "" {
			return fmt.Errorf("model endpoint ID is empty")
		}
		newID := prefix + "-" + endpoint.ID
		refs[endpoint.ID] = newID
		endpoint.ID = newID
	}
	for i := range settings.ModelRouter.Models {
		model := &settings.ModelRouter.Models[i]
		if next, ok := refs[model.EndpointID]; ok {
			model.EndpointID = next
		}
	}
	return nil
}
