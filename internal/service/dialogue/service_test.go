package dialogue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/llm"
)

func TestDialogueService_CRUD(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "dialogue.yml")

	initialYAML := `
- id: "1"
  scene: 场景A
  relation: 熟人
  user: |
    你好
  preferred: |
    你好呀！
`
	if err := os.WriteFile(filePath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	engine := &llm.Engine{}
	svc := New(filePath, engine)
	ctx := context.Background()

	// 1. ListDialogues
	listResp, err := svc.ListDialogues(ctx, connect.NewRequest(&v1.ListDialoguesRequest{}))
	if err != nil {
		t.Fatalf("ListDialogues error: %v", err)
	}
	if len(listResp.Msg.GetDialogues()) != 1 {
		t.Fatalf("expected 1 dialogue, got %d", len(listResp.Msg.GetDialogues()))
	}
	if listResp.Msg.GetDialogues()[0].GetRelation() != "熟人" {
		t.Errorf("expected relation '熟人', got %q", listResp.Msg.GetDialogues()[0].GetRelation())
	}
	if !strings.Contains(listResp.Msg.GetPromptPreview(), "User: 你好\nAssistant: 你好呀！") {
		t.Errorf("prompt preview missing content: %q", listResp.Msg.GetPromptPreview())
	}

	// 2. SaveDialogues
	saveReq := &v1.SaveDialoguesRequest{
		Dialogues: []*v1.DialogueItem{
			{
				Id:        "1",
				Scene:     "场景A",
				Relation:  "熟人",
				User:      "问题一",
				Preferred: "回答一",
			},
			{
				Id:        "2",
				Scene:     "场景B",
				Relation:  "朋友",
				User:      "问题二",
				Preferred: "回答二",
			},
		},
	}
	saveResp, err := svc.SaveDialogues(ctx, connect.NewRequest(saveReq))
	if err != nil {
		t.Fatalf("SaveDialogues error: %v", err)
	}
	if !saveResp.Msg.GetSuccess() {
		t.Fatalf("SaveDialogues failed: %s", saveResp.Msg.GetError())
	}
	if !strings.Contains(engine.DialoguePrompt, "User: 问题二\nAssistant: 回答二") {
		t.Errorf("engine dialogue prompt not updated: %q", engine.DialoguePrompt)
	}

	// 3. GetRawDialogueFile
	rawResp, err := svc.GetRawDialogueFile(ctx, connect.NewRequest(&v1.GetRawDialogueFileRequest{}))
	if err != nil {
		t.Fatalf("GetRawDialogueFile error: %v", err)
	}
	if !strings.Contains(rawResp.Msg.GetContent(), "问题一") {
		t.Errorf("raw content missing expected text: %q", rawResp.Msg.GetContent())
	}

	// 4. UpdateRawDialogueFile - Valid
	newRaw := `
- id: "10"
  scene:
  relation: 朋友
  user: |
    直接修改YAML测试
  preferred: |
    收到修改！
`
	updateRawResp, err := svc.UpdateRawDialogueFile(ctx, connect.NewRequest(&v1.UpdateRawDialogueFileRequest{Content: newRaw}))
	if err != nil {
		t.Fatalf("UpdateRawDialogueFile error: %v", err)
	}
	if !updateRawResp.Msg.GetSuccess() {
		t.Fatalf("UpdateRawDialogueFile failed: %s", updateRawResp.Msg.GetError())
	}
	if !strings.Contains(engine.DialoguePrompt, "User: 直接修改YAML测试\nAssistant: 收到修改！") {
		t.Errorf("engine prompt not updated after raw file update: %q", engine.DialoguePrompt)
	}

	// 5. UpdateRawDialogueFile - Invalid YAML
	invalidRawResp, err := svc.UpdateRawDialogueFile(ctx, connect.NewRequest(&v1.UpdateRawDialogueFileRequest{Content: ": invalid: [yaml"}))
	if err != nil {
		t.Fatalf("UpdateRawDialogueFile returned unexpected transport error: %v", err)
	}
	if invalidRawResp.Msg.GetSuccess() {
		t.Errorf("expected validation failure for invalid YAML")
	}
	if !strings.Contains(invalidRawResp.Msg.GetError(), "YAML") {
		t.Errorf("expected YAML error message, got: %q", invalidRawResp.Msg.GetError())
	}
}
