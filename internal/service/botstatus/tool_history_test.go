package botstatus

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

func TestToolHistoryInspectionKeepsInternalIdentity(t *testing.T) {
	manager := llm.NewSessionManager()
	session := manager.GetOrCreate("private:fixture-history")
	session.ReplaceMessages([]llm.ChatMessage{
		{Role: "user", Content: "【工具执行记录】 user text"},
		{Role: "assistant", Content: "fixture-tool-note", ToolHistoryRunID: "fixture-run"},
		{Role: "assistant", Content: "fixture-reply"},
	})
	service := New(&llm.Engine{SessionManager: manager}, "test", "")
	response, err := service.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: session.ID()}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.History[1].ToolHistoryRunId != "fixture-run" || response.Msg.RecentStructuredMessages[1].ToolHistoryRunId != "fixture-run" || response.Msg.RecentStructuredMessages[0].ToolHistoryRunId != "" || response.Msg.RecentStructuredMessages[2].ToolHistoryRunId != "" {
		t.Fatal("inspection confused user text, tool records and public replies")
	}
	snapshot, err := session.SnapshotPrivateCompact()
	if err != nil || !session.CommitPrivateCompact(snapshot, "fixture-summary") {
		t.Fatal("compact failed", err)
	}
	response, err = service.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: session.ID()}))
	if err != nil || response.Msg.SummaryGroups[0].StructuredMessages[1].ToolHistoryRunId != "fixture-run" {
		t.Fatal("compact batch lost internal tool identity", err)
	}
}

func TestToolHistoryParameterInspectionIsInstanceBoundAndQuarantined(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"fixture-a", "fixture-b"} {
		if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 2); err != nil {
			t.Fatal(err)
		}
	}
	store := toolhistory.NewStore(db, "fixture-a")
	record := toolhistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-user"}, RefID: toolhistory.NewRef(), RunID: "fixture-run", ActorID: "fixture-user", Tool: "send_sticker", State: "complete", OccurredAt: time.Now().UTC(),
		ArgumentsRaw: `{ "query": "好奇 探头", "id":7700000000000000001 }`, ResultRaw: "fixture-private-result", Error: "fixture-private-diagnostic"}
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	service := New(&llm.Engine{ToolHistory: store}, "test", "")
	request := connect.NewRequest(&v1.GetToolHistoryEntryRequest{RefId: record.RefID})
	response, err := service.GetToolHistoryEntry(ctx, request)
	if err != nil || response.Msg.ArgumentsRaw != record.ArgumentsRaw || response.Msg.Tool != record.Tool || !response.Msg.ArgumentsAvailable || response.Msg.RefId != record.RefID {
		t.Fatalf("parameter original changed: %v %v", response, err)
	}
	other := New(&llm.Engine{ToolHistory: toolhistory.NewStore(db, "fixture-b")}, "test", "")
	if _, err := other.GetToolHistoryEntry(ctx, request); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("cross-instance read accepted", err)
	}
	if _, err := service.GetToolHistoryEntry(ctx, connect.NewRequest(&v1.GetToolHistoryEntryRequest{RefId: "invalid"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid reference accepted", err)
	}
	for _, state := range []string{"restricted", "incomplete"} {
		record.Restricted, record.State = state == "restricted", "complete"
		if state == "incomplete" {
			record.State = "executed"
		}
		if err := store.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
		response, err := service.GetToolHistoryEntry(ctx, request)
		if err != nil || response.Msg.ArgumentsAvailable || response.Msg.ArgumentsRaw != "" {
			t.Fatal("uninspected parameters escaped quarantine", err)
		}
	}
	if _, err := New(&llm.Engine{}, "test", "").GetToolHistoryEntry(ctx, request); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("missing archive availability ignored", err)
	}
}
