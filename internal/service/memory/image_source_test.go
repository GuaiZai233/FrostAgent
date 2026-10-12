package memsvc

import (
	"context"
	"testing"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/backup"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/storage"
	"connectrpc.com/connect"
)

func TestImageSourceSurvivesListAndManualEdit(t *testing.T) {
	svc, _, gm := setupTestService(t)
	const groupID = "synthetic-image-service-group"
	gs, err := gm.GetGroupStore(groupID)
	if err != nil {
		t.Fatal(err)
	}
	entry := memory.MemoryEntry{ID: "image-memory", Owner: memory.GroupOwnerExplicit,
		Content: "海报显示每周六游泳", Evidence: "海报显示每周六游泳", Summary: "图片中游泳活动在周六",
		Source: memory.SourceExtract, SourceMedia: memory.SourceMediaImage, SourceMessageID: "image-msg"}
	if err := gs.Save(entry); err != nil {
		t.Fatal(err)
	}
	listed, err := svc.ListMemories(context.Background(), connect.NewRequest(&v1.ListMemoriesRequest{Scope: "group", GroupId: groupID}))
	if err != nil || len(listed.Msg.Memories) != 1 || listed.Msg.Memories[0].SourceMedia != "image" ||
		listed.Msg.Memories[0].Summary != entry.Summary {
		t.Fatalf("image metadata lost in RPC list: %+v, err=%v", listed, err)
	}
	updated, err := svc.UpdateMemory(context.Background(), connect.NewRequest(&v1.UpdateMemoryRequest{
		Id: entry.ID, Scope: "group", GroupId: groupID, Content: "人工修订为每周日游泳",
	}))
	if err != nil || !updated.Msg.Success {
		t.Fatalf("manual edit failed: %+v, err=%v", updated, err)
	}
	entries, err := gs.ListAll()
	if err != nil || len(entries) != 1 || entries[0].SourceMedia != "image" || entries[0].Evidence != entry.Evidence ||
		entries[0].Source != memory.SourceManual {
		t.Fatalf("manual edit erased image provenance: %+v, err=%v", entries, err)
	}
}

func TestSQLPrivateImageSourceSurvivesEditAndBackup(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const instanceID = "synthetic-instance"
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('synthetic-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	store := memory.NewSQLStore(db, instanceID)
	entry := memory.MemoryEntry{ID: "image-memory", Owner: "synthetic-user", OwnerType: memory.OwnerUser,
		Content: "image evidence", Summary: "image summary", Evidence: "image evidence",
		Source: memory.SourceManual, SourceMedia: memory.SourceMediaImage, Visibility: memory.VisibilityPrivate,
		SourceMessageID: "synthetic-message", SourceSenderID: "synthetic-sender", CreatedAt: time.Now()}
	if err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	svc := New(store, memory.NewSQLGroupManager(db, instanceID, nil), nil)
	for _, content := range []string{entry.Content, "edited content"} {
		response, err := svc.UpdateMemory(context.Background(), connect.NewRequest(&v1.UpdateMemoryRequest{
			Id: entry.ID, Content: content, Tags: []string{"edited-tag"},
		}))
		if err != nil || !response.Msg.Success {
			t.Fatalf("private edit failed: %+v, %v", response, err)
		}
		listed, err := svc.ListMemories(context.Background(), connect.NewRequest(&v1.ListMemoriesRequest{}))
		if err != nil || len(listed.Msg.Memories) != 1 || listed.Msg.Memories[0].SourceMedia != memory.SourceMediaImage {
			t.Fatalf("private RPC lost image source: %+v, %v", listed, err)
		}
		exported, err := backup.ExportMemories(db, instanceID)
		if err != nil || len(exported.PrivateEntries) != 1 {
			t.Fatalf("export edited private memory: %+v, %v", exported, err)
		}
		got := exported.PrivateEntries[0]
		if got.Content != content || len(got.Tags) != 1 || got.Tags[0] != "edited-tag" ||
			got.SourceMedia != entry.SourceMedia || got.Summary != entry.Summary || got.Evidence != entry.Evidence ||
			got.SourceMessageID != entry.SourceMessageID || got.SourceSenderID != entry.SourceSenderID ||
			got.OwnerType != entry.OwnerType || got.Visibility != entry.Visibility {
			t.Fatalf("edit lost private image provenance: %+v", got)
		}
	}
}
