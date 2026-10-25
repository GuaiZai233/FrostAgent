package tools

import (
	"context"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestMemoryToolImageWriteUsesTrustedAdapterSource(t *testing.T) {
	for _, tc := range []struct {
		name, args, image, wantMedia string
	}{
		{"image summary", `{"action":"write","content":"图片中每周六游泳","source_media":"image"}`, "海报：每周六游泳", "image"},
		{"legacy call with image", `{"action":"write","content":"图片中每周六游泳"}`, "海报：每周六游泳", "image"},
		{"verbatim text in mixed message", `{"action":"write","content":"我喜欢跑步","source_media":"text"}`, "海报：每周六游泳", ""},
		{"text summary with direct evidence", `{"action":"write","content":"群友喜欢跑步","source_media":"text","evidence":"我喜欢跑步"}`, "海报：每周六游泳", ""},
		{"fake text evidence", `{"action":"write","content":"图片中每周六游泳","source_media":"text","evidence":"每周六游泳"}`, "海报：每周六游泳", "image"},
		{"unproven text claim", `{"action":"write","content":"图片中每周六游泳","source_media":"text"}`, "海报：每周六游泳", "image"},
		{"model cannot invent image origin", `{"action":"write","content":"我喜欢跑步","source_media":"image"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gm := memory.NewGroupManager(t.TempDir(), nil)
			engine := &llm.Engine{GroupManager: gm}
			tool := NewMemoryTool(engine)
			ctx := llm.WithRunContext(context.Background(), llm.RunContext{
				Owner: "group:synthetic-image-tool-group", ActorUserID: "synthetic-sender",
				RouteScope:   modelrouter.Scope{GroupID: "synthetic-image-tool-group"},
				MemorySource: memory.GroupMessage{Content: "我喜欢跑步", ImageDescription: tc.image, MessageID: "image-msg"},
			})
			result, err := tool.ExecuteContext(ctx, tc.args)
			if err != nil || result != "群记忆已写入" {
				t.Fatalf("write failed: %q, err=%v", result, err)
			}
			gs, err := gm.GetGroupStore("synthetic-image-tool-group")
			if err != nil {
				t.Fatal(err)
			}
			entries, err := gs.ListAll()
			if err != nil || len(entries) != 1 || entries[0].SourceMedia != tc.wantMedia {
				t.Fatalf("unexpected image attribution: %+v, err=%v", entries, err)
			}
			if tc.wantMedia == "image" && (entries[0].SourceSenderID != "synthetic-sender" || entries[0].SourceMessageID != "image-msg") {
				t.Fatalf("trusted image message attribution lost: %+v", entries[0])
			}
		})
	}
}
