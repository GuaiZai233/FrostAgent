package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestChatHistoryToolTrustedScope(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: "fixture-instance", Name: "Fixture", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	store := chathistory.NewStore(db, "fixture-instance")
	for _, scope := range []chathistory.Scope{
		{Platform: "qq", MessageType: "private", TargetID: "fixture-user"},
		{Platform: "qq", MessageType: "private", TargetID: "fixture-other"},
		{Platform: "qq", MessageType: "group", TargetID: "fixture-group"},
		{Platform: "qq", MessageType: "group", TargetID: "fixture-foreign"},
		{Platform: "telegram", MessageType: "group", TargetID: "fixture-group"},
	} {
		r := chathistory.Record{Scope: scope, ID: scope.Platform + scope.MessageType + scope.TargetID,
			Role: "user", Content: "needle " + scope.TargetID, OccurredAt: time.Now(), DeliveryStatus: "received"}
		if err := store.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewChatHistoryTool(&llm.Engine{ChatHistory: chathistory.NewRecorder(store, nil, nil)})
	for _, tc := range []struct {
		name string
		run  llm.RunContext
		want string
	}{
		{"private", llm.RunContext{ActorUserID: "fixture-user", ActorPlatform: "onebot"}, "fixture-user"},
		{"group", llm.RunContext{ActorUserID: "fixture-user", ActorPlatform: "qq", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "fixture-group"}}, "fixture-group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runCtx := llm.WithRunContext(ctx, tc.run)
			result, err := tool.ExecuteContext(runCtx, `{"keywords":["needle"]}`)
			var payload struct {
				Records []chathistory.Hit `json:"records"`
			}
			if err != nil || json.Unmarshal([]byte(result), &payload) != nil || len(payload.Records) != 1 || payload.Records[0].TargetID != tc.want {
				t.Fatalf("scope leak: %s %v", result, err)
			}
			for _, args := range []string{`{"keywords":["needle"],"target_id":"fixture-other"}`, `{"keywords":["needle"],"platform":"telegram"}`, `{"keywords":["needle"]} {}`, `{"keywords":[]}`, `{"keywords":["needle"],"before":"yesterday"}`} {
				if _, err := tool.ExecuteContext(runCtx, args); err == nil {
					t.Fatalf("accepted invalid/foreign scope: %s", args)
				}
			}
		})
	}
	for _, run := range []llm.RunContext{{}, {ActorUserID: "fixture-user"}, {ActorPlatform: "qq"}, {ActorUserID: "fixture-user", ActorPlatform: "qq", Mock: true},
		{ActorUserID: "fixture-user", ActorPlatform: "telegram", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "fixture-group"}},
	} {
		result, err := tool.ExecuteContext(llm.WithRunContext(ctx, run), `{"keywords":["needle"]}`)
		if err != nil || !strings.Contains(result, "没有") || strings.Contains(result, "needle") {
			t.Fatalf("untrusted access: %s %v", result, err)
		}
	}
}
