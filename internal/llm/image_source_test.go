package llm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestStagedImageDescriptionBindsToExactUserSlot(t *testing.T) {
	session := NewSessionManager().GetOrCreate("synthetic-image-session")
	first, _ := session.StageGroupCompactWithGuard(GroupCompactMessage{Role: "user", SenderID: "synthetic-user", Content: "第一张图片"}, 10, "synthetic-user", nil)
	second, _ := session.StageGroupCompactWithGuard(GroupCompactMessage{Role: "user", SenderID: "synthetic-user", Content: "第二张图片"}, 10, "synthetic-user", nil)
	if !first.SetImageDescription("第一张海报内容") {
		t.Fatal("failed to attach vision output")
	}
	first.Promote()
	second.Drop()
	if first.SetImageDescription("过期识别结果") || second.SetImageDescription("被丢弃的识别结果") {
		t.Fatal("finalized image slots must not be mutated or revived")
	}
	snapshot, ready := session.SnapshotGroupCompact(1)
	if !ready || len(snapshot.Messages) != 1 || snapshot.Messages[0].Content != "第一张图片" ||
		snapshot.Messages[0].ImageDescription != "第一张海报内容" {
		t.Fatalf("image metadata crossed sequence slots: %+v", snapshot.Messages)
	}
}

func TestImageProvenanceSurvivesExtractionAndCompaction(t *testing.T) {
	for _, path := range []string{"turn", "compact"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			gm := memory.NewGroupManager(dir, nil)
			writer := memory.NewWriter(memory.NewStore(filepath.Join(dir, "brain.json")))
			writer.SetGroupManager(gm)
			writer.SetLLM(&mockBlockingExtractProvider{onChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				if !strings.Contains(req.Messages[0].Content.(string), `"image_description": "海报：我每周六游泳"`) {
					t.Fatal("vision output lost before memory extraction")
				}
				return &core.ChatResponse{Message: core.ChatMessage{Content: `[{"source_msg_index":0,"evidence":"我每周六游泳","summary":"图片中每周六游泳","is_self":true,"tags":["游泳"]}]`}}, nil
			}}, "mock-writer")
			const groupID = "synthetic-image-group"
			if path == "turn" {
				session := NewSessionManager().GetOrCreate("synthetic-image-turn")
				batch, ready := session.EnqueuePendingTurn([]memory.PendingExtractionItem{{
					ScopeType: memory.ScopeGroup, GroupID: groupID, SpeakerID: "synthetic-sender",
					Message:   core.ChatMessage{Role: core.RoleUser, Content: "请记住这张海报"},
					MessageID: "image-msg", ImageDescription: "海报：我每周六游泳",
				}}, 1, 1)
				if !ready {
					t.Fatal("extraction batch not ready")
				}
				engine := &Engine{MemoryWriter: writer}
				engine.extractPendingBatch(batch)
			} else {
				compactor := NewGroupCompactor(nil, nil, "", 1, 0)
				compactor.SetMemoryWriter(writer)
				compactor.distillGroupMemories(nil, "group:"+groupID, modelrouter.Scope{GroupID: groupID},
					GroupCompactSnapshot{Messages: []GroupCompactMessage{{Role: "user", Content: "请记住这张海报",
						SenderID: "synthetic-sender", MessageID: "image-msg", ImageDescription: "海报：我每周六游泳"}}})
			}
			gs, err := gm.GetGroupStore(groupID)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := gs.ListAll()
			if err != nil || len(entries) != 1 || entries[0].SourceMedia != memory.SourceMediaImage || entries[0].Owner != memory.GroupOwnerExplicit {
				t.Fatalf("image provenance lost in %s path: %+v, err=%v", path, entries, err)
			}
		})
	}
}
