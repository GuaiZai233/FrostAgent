package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

func toolHistoryFixture(t *testing.T) (*llm.Engine, llm.RunContext) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: "fixture-instance", Name: "Fixture", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	return &llm.Engine{ToolHistory: toolhistory.NewStore(db, "fixture-instance"), ChatHistory: chathistory.NewRecorder(chathistory.NewStore(db, "fixture-instance"), nil, nil), SessionManager: llm.NewSessionManager()}, llm.RunContext{SessionID: "group:fixture-group", ActorUserID: "fixture-actor", ActorPlatform: "onebot", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "fixture-group"}, MemorySource: memory.GroupMessage{MessageID: "fixture-message"}}
}

func TestToolHistoryCrossTurnAndReadOfRead(t *testing.T) {
	engine, run := toolHistoryFixture(t)
	session := engine.SessionManager.GetOrCreate(run.SessionID)
	orderRaw := `{"orderId":7700000000000000001,"orderIdStr":"7700000000000000001","needPay":true}`
	createCount := 0
	create := Tool{name: "fixture_create_order", executeContext: func(context.Context, string) (string, error) { createCount++; return orderRaw, nil }}
	read := NewToolHistoryTool(engine)
	engine.ToolRegistry = map[string]llm.ToolExecutor{create.Name(): create, read.Name(): read}
	var target string
	for phase := 0; phase < 3; phase++ {
		turn := session.ReserveSequenceTurn("fixture-lane", false, false)
		turn.Wait()
		messages, _, _ := session.PrepareTurnMessages(turn, fmt.Sprintf("fixture-input-%d", phase), fmt.Sprintf("fixture-input-%d", phase), nil)
		if phase > 0 {
			encoded, _ := json.Marshal(messages)
			if strings.Contains(string(encoded), "7700000000000000001") || !strings.Contains(string(encoded), target) {
				t.Fatalf("cross-turn context must retain reference, not body: %s", encoded)
			}
		}
		calls := 0
		engine.Provider = specificReplyProvider{chat: func(req core.ChatRequest) core.ChatMessage {
			calls++
			if calls == 1 {
				name, args := create.Name(), `{"product":"fixture-matcha"}`
				if phase > 0 {
					name = read.Name()
					args = fmt.Sprintf(`{"read_target_ref_id":%q}`, target)
				}
				return core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: fmt.Sprintf("fixture-call-%d", phase), Type: "function", Function: core.ToolCallFunction{Name: name, Arguments: args}}}}
			}
			last := req.Messages[len(req.Messages)-1]
			body, _ := last.Content.(string)
			if last.Role != core.RoleTool || !strings.Contains(body, "7700000000000000001") || !strings.Contains(body, "(ref_id: fa_toolcall_") {
				t.Fatalf("tool original/reference missing: %s", body)
			}
			return core.ChatMessage{Role: core.RoleAssistant, Content: "fixture-final"}
		}}
		run.Turn = turn
		run.MemorySource.MessageID = fmt.Sprintf("fixture-message-%d", phase)
		result := engine.RunMessagesWithContext(messages, run)
		turn.Done()
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		records, err := engine.ToolHistory.Export(context.Background())
		if err != nil || len(records) != phase+1 {
			t.Fatalf("records: %v %v", records, err)
		}
		var latest toolhistory.Record
		for _, r := range records {
			if r.SourceMessageID == run.MemorySource.MessageID {
				latest = r
			}
		}
		if latest.State != "complete" || latest.Restricted {
			t.Fatalf("incomplete archive: %+v", latest)
		}
		if phase == 1 {
			var original toolhistory.Record
			if err := json.Unmarshal([]byte(latest.ResultRaw), &original); err != nil || original.RefID != target || original.ResultRaw != orderRaw {
				t.Fatalf("read did not return original: %+v %v", original, err)
			}
		}
		if phase == 2 {
			var previous, original toolhistory.Record
			if err := json.Unmarshal([]byte(latest.ResultRaw), &previous); err != nil || previous.RefID != target || previous.Tool != read.Name() {
				t.Fatalf("read-of-read lost shell: %+v %v", previous, err)
			}
			if err := json.Unmarshal([]byte(previous.ResultRaw), &original); err != nil || original.ResultRaw != orderRaw {
				t.Fatalf("nested original changed: %+v %v", original, err)
			}
		}
		target = latest.RefID
	}
	if createCount != 1 {
		t.Fatalf("history replay executed business tool %d times", createCount)
	}
}

func TestToolHistoryScopePagingAndInvalidArguments(t *testing.T) {
	engine, run := toolHistoryFixture(t)
	scope, _ := llm.ToolHistoryScope(run)
	r := toolhistory.Record{Scope: scope, RefID: toolhistory.NewRef(), RunID: "fixture-run", ActorID: run.ActorUserID, Tool: "fixture_tool", ArgumentsRaw: `{}`, ResultRaw: strings.Repeat("原文\"\\\n", 4000), State: "complete", OccurredAt: time.Now().UTC()}
	if err := engine.ToolHistory.Save(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	tool := NewToolHistoryTool(engine)
	ctx := llm.WithRunContext(context.Background(), run)
	var joined strings.Builder
	for offset := 0; ; {
		body, err := tool.ExecuteContext(ctx, fmt.Sprintf(`{"read_target_ref_id":%q,"offset":%d,"limit":127}`, r.RefID, offset))
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Offset int    `json:"offset"`
			Next   *int   `json:"next_offset"`
			JSON   string `json:"record_json"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil || page.Offset != offset || len(body) > llm.MaxToolOutputBytes {
			t.Fatalf("bad page: %s %v", body, err)
		}
		joined.WriteString(page.JSON)
		if page.Next == nil {
			break
		}
		offset = *page.Next
	}
	want, _ := json.Marshal(r)
	if joined.String() != string(want) {
		t.Fatal("pages did not reconstruct original record")
	}
	for _, args := range []string{`{}`, fmt.Sprintf(`{"read_target_ref_id":%q,"target_id":"fixture-foreign"}`, r.RefID), fmt.Sprintf(`{"read_target_ref_id":%q,"limit":0}`, r.RefID), fmt.Sprintf(`{"read_target_ref_id":%q,"offset":-1}`, r.RefID), `{} {}`} {
		if _, err := tool.ExecuteContext(ctx, args); err == nil {
			t.Fatalf("accepted invalid args: %s", args)
		}
	}
	for _, other := range []llm.RunContext{{}, {ActorUserID: run.ActorUserID, ActorPlatform: run.ActorPlatform, Mock: true}, {ActorUserID: "fixture-foreign", ActorPlatform: "qq"}, {ActorUserID: run.ActorUserID, ActorPlatform: "telegram", RouteScope: run.RouteScope}} {
		body, err := tool.ExecuteContext(llm.WithRunContext(context.Background(), other), fmt.Sprintf(`{"read_target_ref_id":%q}`, r.RefID))
		if err != nil || strings.Contains(body, "原文") {
			t.Fatalf("scope leak: %s %v", body, err)
		}
	}
	r.Restricted = true
	r.ModelResultRaw = "isolated"
	if err := engine.ToolHistory.Save(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	body, err := tool.ExecuteContext(ctx, fmt.Sprintf(`{"read_target_ref_id":%q}`, r.RefID))
	if err != nil || strings.Contains(body, "原文") || !strings.Contains(body, "isolated") {
		t.Fatalf("quarantine bypass: %s %v", body, err)
	}
}

func TestToolHistoryReferencesDiscoverableFromChatSearch(t *testing.T) {
	engine, run := toolHistoryFixture(t)
	ctx := llm.WithRunContext(context.Background(), run)
	scope, _ := llm.ToolHistoryScope(run)
	message := chathistory.Record{Scope: scope, ID: "fixture-chat", MessageID: run.MemorySource.MessageID,
		Role: "user", Content: "fixture-matcha", OccurredAt: time.Now().UTC(), DeliveryStatus: "received"}
	if err := engine.ChatHistory.Store.Save(ctx, message); err != nil {
		t.Fatal(err)
	}
	want := toolhistory.NewRef()
	for _, target := range []string{scope.TargetID, "fixture-foreign-group"} {
		r := toolhistory.Record{Scope: scope, RefID: want, RunID: "fixture-run", ActorID: run.ActorUserID,
			Tool: "fixture-create-order", SourceMessageID: message.MessageID, State: "complete", OccurredAt: time.Now().UTC()}
		r.TargetID = target
		if target != scope.TargetID {
			r.RefID = toolhistory.NewRef()
		}
		if err := engine.ToolHistory.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	body, err := NewChatHistoryTool(engine).ExecuteContext(ctx, `{"keywords":["fixture-matcha"]}`)
	var result struct {
		Records []struct {
			ToolRefs []string `json:"tool_refs"`
		} `json:"records"`
	}
	if err != nil || json.Unmarshal([]byte(body), &result) != nil || len(result.Records) != 1 || len(result.Records[0].ToolRefs) != 1 || result.Records[0].ToolRefs[0] != want {
		t.Fatalf("chat search lost source association or leaked foreign refs: %s %v", body, err)
	}
}
