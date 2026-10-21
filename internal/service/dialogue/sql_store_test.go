package dialogue

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"reflect"
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
	examples := []llm.DialogueExample{{ID: "example-a", User: "hello", Preferred: "hi"}}
	// Existing databases may still contain the retired metadata columns.
	if _, err := db.SQL.Exec(`INSERT INTO dialogues
		(instance_id, position, id, scene, relation, user_text, preferred)
		VALUES ('test-instance', 0, 'example-a', 'meeting', 'friend', 'hello', 'hi')`); err != nil {
		t.Fatal(err)
	}
	legacy, err := LoadExamplesSQL(db, "test-instance")
	if err != nil || !reflect.DeepEqual(legacy, examples) {
		t.Fatalf("legacy dialogue not recovered: %#v, %v", legacy, err)
	}
	if err := SaveExamplesSQL(db, "test-instance", examples); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadExamplesSQL(db, "test-instance")
	if err != nil || !reflect.DeepEqual(loaded, examples) {
		t.Fatalf("SQL dialogue not recovered: %#v, %v", loaded, err)
	}
	var scene, relation string
	if err := db.SQL.QueryRow(`SELECT scene, relation FROM dialogues WHERE instance_id = 'test-instance'`).Scan(&scene, &relation); err != nil {
		t.Fatal(err)
	}
	if scene != "" || relation != "" {
		t.Fatalf("save retained retired metadata: scene=%q relation=%q", scene, relation)
	}
	prompt, err := LoadPromptSQL(db, "test-instance")
	if err != nil || prompt != llm.DefaultDialogueInstruction+"\n\nUser: hello\nAssistant: hi" {
		t.Fatalf("dialogue prompt not generated: %q, %v", prompt, err)
	}
}
