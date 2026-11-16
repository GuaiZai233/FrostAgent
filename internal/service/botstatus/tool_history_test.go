package botstatus

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
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
