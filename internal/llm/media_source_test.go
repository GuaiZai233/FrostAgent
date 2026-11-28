package llm

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestStagedMediaDescriptionBindsToExactUserSlot(t *testing.T) {
	session := NewSessionManager().GetOrCreate("synthetic-media-session")
	first, _ := session.StageGroupCompactWithGuard(GroupCompactMessage{Role: "user", SenderID: "synthetic-user", Content: "[语音]"}, 10, "synthetic-user", nil)
	second, _ := session.StageGroupCompactWithGuard(GroupCompactMessage{Role: "user", SenderID: "synthetic-user", Content: "[视频]"}, 10, "synthetic-user", nil)
	if !first.SetMediaDescription("说话内容：明天见") || !first.SetImageDescription("图片内容") {
		t.Fatal("failed to attach transcription output")
	}
	first.Promote()
	second.Drop()
	if first.SetMediaDescription("过期转述") || second.SetMediaDescription("被丢弃的转述") {
		t.Fatal("finalized media slots must not be mutated or revived")
	}
	snapshot, ready := session.SnapshotGroupCompact(1)
	if !ready || len(snapshot.Messages) != 1 || snapshot.Messages[0].MediaDescription != "说话内容：明天见" ||
		snapshot.Messages[0].ImageDescription != "图片内容" {
		t.Fatalf("media metadata crossed sequence slots: %+v", snapshot.Messages)
	}
	if display := snapshot.Messages[0].FormatDisplay(); !strings.Contains(display, "【音视频转述结果】：说话内容：明天见") {
		t.Fatalf("display lost media transcription: %s", display)
	}
}

func TestMediaProvenanceSurvivesExtractionAndCompaction(t *testing.T) {
	for _, path := range []string{"turn", "compact"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			gm := memory.NewGroupManager(dir, nil)
			writer := memory.NewWriter(memory.NewStore(filepath.Join(dir, "brain.json")))
			writer.SetGroupManager(gm)
			writer.SetLLM(&mockBlockingExtractProvider{onChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				if !strings.Contains(req.Messages[0].Content.(string), `"media_description": "说话内容：我每周六游泳"`) {
					t.Fatal("media transcription lost before memory extraction")
				}
				return &core.ChatResponse{Message: core.ChatMessage{Content: `[{"source_msg_index":0,"evidence":"我每周六游泳","summary":"语音中说每周六游泳","tags":["游泳"]}]`}}, nil
			}}, "mock-writer")
			const groupID = "synthetic-media-group"
			if path == "turn" {
				session := NewSessionManager().GetOrCreate("synthetic-media-turn")
				batch, ready := session.EnqueuePendingTurn([]memory.PendingExtractionItem{{
					ScopeType: memory.ScopeGroup, GroupID: groupID, SpeakerID: "synthetic-sender",
					Message:   core.ChatMessage{Role: core.RoleUser, Content: "[语音]"},
					MessageID: "media-msg", MediaDescription: "说话内容：我每周六游泳",
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
					GroupCompactSnapshot{Messages: []GroupCompactMessage{{Role: "user", Content: "[语音]",
						SenderID: "synthetic-sender", MessageID: "media-msg", MediaDescription: "说话内容：我每周六游泳"}}})
			}
			gs, err := gm.GetGroupStore(groupID)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := gs.ListAll()
			if err != nil || len(entries) != 1 || entries[0].SourceMedia != memory.SourceMediaMedia {
				t.Fatalf("media provenance lost in %s path: %+v, err=%v", path, entries, err)
			}
		})
	}
}

func TestLastActualRequestOmitsInlineMedia(t *testing.T) {
	session := NewSessionManager().GetOrCreate("private:synthetic-media-request")
	audio := base64.StdEncoding.EncodeToString([]byte("RIFF\x24\x00\x00\x00WAVEfmt synthetic-audio-bytes"))
	session.SetLastActualRequest(0, "synthetic-model", core.ChatRequest{Messages: []core.ChatMessage{{
		Role: core.RoleUser,
		Content: []core.ContentPart{
			{Type: "text", Text: "User Message: [语音]"},
			{Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/gif;base64,R0lGODlh"}},
			{Type: "input_audio", InputAudio: &core.InputAudio{Data: audio, Format: "wav"}},
		},
	}}})
	stored, _ := session.LastActualRequest()
	if strings.Contains(stored, audio) || !strings.Contains(stored, "[audio omitted: format=wav") {
		t.Fatalf("checkpointed request kept audio bytes: %s", stored)
	}
	if !strings.Contains(stored, "data:image/gif;base64,R0lGODlh") {
		t.Fatalf("image diagnostics must be preserved: %s", stored)
	}
}
