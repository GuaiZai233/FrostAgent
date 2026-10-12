package dialogue

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestSQLDialogueExamplesRoundTrip(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "dialogues.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	examples := []llm.DialogueExample{{ID: "example-a", Scene: "meeting", Relation: "friend", User: "hello", Preferred: "hi"}}
	if err := SaveExamplesSQL(db, "test-instance", examples); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadExamplesSQL(db, "test-instance")
	if err != nil || len(loaded) != 1 || loaded[0].Relation != "friend" {
		t.Fatalf("SQL dialogue not recovered: %#v, %v", loaded, err)
	}
	prompt, err := LoadPromptSQL(db, "test-instance")
	if err != nil || prompt == "" {
		t.Fatalf("dialogue prompt not generated: %q, %v", prompt, err)
	}
}
