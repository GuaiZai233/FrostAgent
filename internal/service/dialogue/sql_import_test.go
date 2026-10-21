package dialogue

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/storage"
	"connectrpc.com/connect"
)

func TestSQLDialogueYAMLImportExport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"instance-a", "instance-b"} {
		if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES (?, ?, 'now')`, id, id); err != nil {
			t.Fatal(err)
		}
		if err := SaveExamplesSQL(db, id, []llm.DialogueExample{{ID: "original", User: "original question", Preferred: "original answer"}}); err != nil {
			t.Fatal(err)
		}
	}
	engine := &llm.Engine{}
	svc := NewSQL(db, "instance-a", engine)
	// SQL endpoints must never read or write the old file storage path.
	svc.filePath = filepath.Join(root, "unused", "dialogue.yml")
	_, handler := frostagentv1connect.NewDialogueServiceHandler(svc)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := frostagentv1connect.NewDialogueServiceClient(server.Client(), server.URL)
	importYAML := func(content string) *v1.UpdateRawDialogueFileResponse {
		t.Helper()
		response, err := client.UpdateRawDialogueFile(ctx, connect.NewRequest(&v1.UpdateRawDialogueFileRequest{Content: content}))
		if err != nil {
			t.Fatalf("import RPC failed: %v", err)
		}
		return response.Msg
	}
	exportYAML := func() string {
		t.Helper()
		response, err := client.GetRawDialogueFile(ctx, connect.NewRequest(&v1.GetRawDialogueFileRequest{}))
		if err != nil {
			t.Fatalf("export RPC failed: %v", err)
		}
		if response.Msg.FilePath != "" {
			t.Fatalf("SQL export exposes a source file: %q", response.Msg.FilePath)
		}
		return response.Msg.Content
	}

	content := `- id: 7
  scene: 问候
  relation: 朋友
  user: |-
    你好
    今天怎么样？
  preferred: |-
    你好呀！
    一切顺利。
- id: second
  user: "test: question"
  preferred: "test: answer"
`
	response := importYAML(content)
	if !response.Success || !strings.Contains(response.PromptPreview, "你好呀！") || engine.PersonaDialogue() != response.PromptPreview {
		t.Fatalf("import did not refresh the prompt: %v", response)
	}
	expected := []llm.DialogueExample{
		{ID: "7", User: "你好\n今天怎么样？", Preferred: "你好呀！\n一切顺利。"},
		{ID: "second", User: "test: question", Preferred: "test: answer"},
	}
	loaded, err := LoadExamplesSQL(db, "instance-a")
	if err != nil || !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("import not persisted in order: %#v, %v", loaded, err)
	}
	// A fresh service reads persisted examples, not an in-memory YAML cache.
	fresh := NewSQL(db, "instance-a", nil)
	if fresh.Prompt() != response.PromptPreview {
		t.Fatal("fresh service cannot load imported prompt")
	}
	exported := exportYAML()
	if strings.Contains(exported, "scene:") || strings.Contains(exported, "relation:") {
		t.Fatalf("export retained removed fields from legacy YAML: %s", exported)
	}
	parsed, err := llm.ParseDialogueYAML([]byte(exported))
	if err != nil || !reflect.DeepEqual(parsed, expected) {
		t.Fatalf("export lost fields: %#v, %v", parsed, err)
	}
	if response := importYAML(exported); !response.Success {
		t.Fatalf("export cannot be reimported: %v", response)
	}
	previousPrompt := engine.PersonaDialogue()
	for _, invalid := range []string{": invalid: [yaml", "user: not-a-list", "", " \n", "# comment only\n", "null", "---\n", "scalar"} {
		response := importYAML(invalid)
		if response.Success || !strings.Contains(response.Error, "YAML") {
			t.Fatalf("invalid YAML accepted: %v", response)
		}
		if exportYAML() != exported || engine.PersonaDialogue() != previousPrompt {
			t.Fatal("invalid import changed stored data or prompt")
		}
	}
	// Fail after DELETE to verify the transaction rolls back before prompt update.
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_dialogue_import BEFORE INSERT ON dialogues
		WHEN NEW.instance_id = 'instance-a' BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	response = importYAML(content)
	if response.Success || !strings.Contains(response.Error, "保存示例对话失败") {
		t.Fatalf("write failure not reported: %v", response)
	}
	if exportYAML() != exported || engine.PersonaDialogue() != previousPrompt {
		t.Fatal("failed SQL transaction changed stored data or prompt")
	}
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_dialogue_import`); err != nil {
		t.Fatal(err)
	}
	response = importYAML("[]\n")
	if !response.Success || response.PromptPreview != "" || engine.PersonaDialogue() != "" || exportYAML() != "[]\n" {
		t.Fatalf("empty-list round trip failed: %v", response)
	}
	other, err := LoadExamplesSQL(db, "instance-b")
	if err != nil || len(other) != 1 || other[0].ID != "original" {
		t.Fatalf("import affected another instance: %#v, %v", other, err)
	}
	if _, err := os.Stat(svc.filePath); !os.IsNotExist(err) {
		t.Fatalf("SQL import accessed old YAML file storage: %v", err)
	}
}
