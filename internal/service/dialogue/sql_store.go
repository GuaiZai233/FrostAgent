package dialogue

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/storage"
	"context"
	"database/sql"
	"fmt"
)

func LoadExamplesSQL(db *storage.DB, instanceID string) ([]llm.DialogueExample, error) {
	rows, err := db.SQL.QueryContext(context.Background(), db.Bind(`SELECT id, scene, relation,
		user_text, preferred FROM dialogues WHERE instance_id = ? ORDER BY position`), instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var examples []llm.DialogueExample
	for rows.Next() {
		var id string
		var example llm.DialogueExample
		if err := rows.Scan(&id, &example.Scene, &example.Relation, &example.User, &example.Preferred); err != nil {
			return nil, err
		}
		example.ID = id
		examples = append(examples, example)
	}
	return examples, rows.Err()
}

func SaveExamplesSQL(db *storage.DB, instanceID string, examples []llm.DialogueExample) error {
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := WriteExamplesTx(ctx, db, tx, instanceID, examples); err != nil {
		return err
	}
	return tx.Commit()
}

func WriteExamplesTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, examples []llm.DialogueExample) error {
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM dialogues WHERE instance_id = ?`), instanceID); err != nil {
		return err
	}
	for position, example := range examples {
		id := ""
		if example.ID != nil {
			id = fmt.Sprint(example.ID)
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO dialogues
			(instance_id, position, id, scene, relation, user_text, preferred)
			VALUES (?, ?, ?, ?, ?, ?, ?)`), instanceID, position, id,
			example.Scene, example.Relation, example.User, example.Preferred); err != nil {
			return err
		}
	}
	return nil
}

func LoadPromptSQL(db *storage.DB, instanceID string) (string, error) {
	examples, err := LoadExamplesSQL(db, instanceID)
	if err != nil {
		return "", err
	}
	return llm.FormatDialoguePrompt(examples), nil
}
