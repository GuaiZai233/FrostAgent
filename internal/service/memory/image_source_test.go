package memsvc

import (
	"context"
	"testing"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/memory"
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
