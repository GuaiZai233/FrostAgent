package botstatus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
	resultRequest := connect.NewRequest(&v1.GetToolHistoryEntryRequest{RefId: record.RefID, IncludeResult: true})
	response, err := service.GetToolHistoryEntry(ctx, request)
	if err != nil || response.Msg.ArgumentsRaw != record.ArgumentsRaw || response.Msg.Tool != record.Tool || !response.Msg.ArgumentsAvailable || response.Msg.RefId != record.RefID {
		t.Fatalf("parameter original changed: %v %v", response, err)
	}
	if response.Msg.ResultRaw != "" || response.Msg.ResultAvailable {
		t.Fatal("arguments-only expansion transferred the result")
	}
	result, err := service.GetToolHistoryEntry(ctx, resultRequest)
	if err != nil || result.Msg.ResultRaw != record.ResultRaw || !result.Msg.ResultAvailable {
		t.Fatal("completed call result original not returned", err)
	}
	other := New(&llm.Engine{ToolHistory: toolhistory.NewStore(db, "fixture-b")}, "test", "")
	if _, err := other.GetToolHistoryEntry(ctx, request); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("cross-instance read accepted", err)
	}
	if _, err := service.GetToolHistoryEntry(ctx, connect.NewRequest(&v1.GetToolHistoryEntryRequest{RefId: "invalid"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid reference accepted", err)
	}
	for _, state := range []string{"restricted", "running", "executed"} {
		record.Restricted, record.State = state == "restricted", "complete"
		if state != "restricted" {
			record.State, record.Restricted = state, true
		}
		if err := store.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
		response, err := service.GetToolHistoryEntry(ctx, request)
		if err != nil || response.Msg.ArgumentsAvailable || response.Msg.ArgumentsRaw != "" || response.Msg.ArgumentsPending != (state != "restricted") {
			t.Fatal("uninspected parameters escaped quarantine", err)
		}
		result, err := service.GetToolHistoryEntry(ctx, resultRequest)
		if err != nil || result.Msg.ResultAvailable || result.Msg.ResultRaw != "" {
			t.Fatal("uninspected result escaped quarantine", err)
		}
	}
	record.State, record.Restricted = "complete", false
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	response, err = service.GetToolHistoryEntry(ctx, request)
	if err != nil || !response.Msg.ArgumentsAvailable || response.Msg.ArgumentsPending || response.Msg.ArgumentsRaw != record.ArgumentsRaw {
		t.Fatal("completed call did not become available", err)
	}
	if _, err := New(&llm.Engine{}, "test", "").GetToolHistoryEntry(ctx, request); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("missing archive availability ignored", err)
	}
}

// This actual RPC response is also consumed by the frontend projection test.
func TestGroupToolHistoryContextWireFixture(t *testing.T) {
	manager := llm.NewSessionManager()
	session := manager.GetOrCreate("group:fixture-group")
	note := "【工具执行记录】\n来源消息: \"fixture-message\"\n- 工具调用: send_sticker (ref_id: fa_toolcall_550e8400-e29b-41d4-a716-446655440000)"
	session.ReplaceMessages([]llm.ChatMessage{
		{Role: "user", Content: "fixture internal trigger"},
		{Role: "assistant", Content: note, ToolHistoryRunID: "fixture-run"},
		{Role: "assistant", Content: "fixture group answer"},
	})
	session.AppendGroupCompactMessage(llm.GroupCompactMessage{Role: "user", Sender: "Fixture member", SenderID: "fixture-member", Content: "fixture group question", MessageID: "fixture-message", Time: "09:30:00"}, 20)
	session.AppendGroupCompactMessage(llm.GroupCompactMessage{Role: "assistant", Sender: "Fixture bot", Content: "fixture group answer", Time: "09:31:00"}, 20)
	service := New(&llm.Engine{SessionManager: manager}, "test", "")
	response, err := service.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: session.ID(), RecentLimit: 50}))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Msg.History) != 3 || response.Msg.History[1].ToolHistoryRunId != "fixture-run" || len(response.Msg.RecentStructuredMessages) != 2 {
		t.Fatal("group projection fixture lost its independent sources")
	}
	path := filepath.Join("..", "..", "..", "apps", "web", "tests", "fixtures", "group-tool-context.json")
	if os.Getenv("FROSTAGENT_UPDATE_TOOL_CONTEXT_FIXTURE") == "1" {
		data, err := (protojson.MarshalOptions{Multiline: true, Indent: "  "}).Marshal(response.Msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture v1.GetSessionContextResponse
	if err := protojson.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(response.Msg, &fixture) {
		t.Fatal("frontend fixture differs from the actual group GetSessionContext response")
	}
}

func TestToolHistoryResultInspectionIsPaged(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: "fixture-a", Name: "fixture-a", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	store := toolhistory.NewStore(db, "fixture-a")
	// Larger than the 64 KiB model output limit; every page must stay bounded.
	large := strings.Repeat("原文一二三", 30000)
	record := toolhistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-user"}, RefID: toolhistory.NewRef(), RunID: "fixture-run", ActorID: "fixture-user", Tool: "fixture_tool", State: "complete", OccurredAt: time.Now().UTC(),
		ArgumentsRaw: `{"blob":"` + strings.Repeat("参", 40000) + `"}`, ResultRaw: large, ModelResultRaw: "complete"}
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	service := New(&llm.Engine{ToolHistory: store}, "test", "")
	total := int32(len([]rune(large)))
	var assembled strings.Builder
	offset, pages := int32(0), 0
	for {
		response, err := service.GetToolHistoryEntry(ctx, connect.NewRequest(&v1.GetToolHistoryEntryRequest{RefId: record.RefID, IncludeResult: true, ResultOffset: offset}))
		if err != nil {
			t.Fatal(err)
		}
		if n := len([]rune(response.Msg.ResultRaw)); n > 8000 {
			t.Fatalf("page exceeds bound: %d characters", n)
		}
		if response.Msg.ResultTotalChars != total || response.Msg.ResultOffset != offset {
			t.Fatal("paging metadata wrong")
		}
		if response.Msg.ArgumentsRaw != "" {
			t.Fatal("result page transferred the arguments payload")
		}
		assembled.WriteString(response.Msg.ResultRaw)
		pages++
		if !response.Msg.ResultHasMore {
			break
		}
		offset = response.Msg.ResultNextOffset
	}
	if pages < 2 || assembled.String() != large {
		t.Fatalf("paged result did not reassemble exactly: pages=%d", pages)
	}
	if _, err := service.GetToolHistoryEntry(ctx, connect.NewRequest(&v1.GetToolHistoryEntryRequest{RefId: record.RefID, IncludeResult: true, ResultOffset: total + 1})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("out-of-range offset accepted", err)
	}
	if _, err := service.GetToolHistoryEntry(ctx, connect.NewRequest(&v1.GetToolHistoryEntryRequest{RefId: record.RefID, IncludeResult: true, ResultOffset: -1})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("negative offset accepted", err)
	}
}

func TestResultPageCountsCharactersAcrossMixedWidths(t *testing.T) {
	text := "a中😀b"
	page, total, end, ok := resultPage(text, 1, 2)
	if !ok || page != "中😀" || total != 4 || end != 3 {
		t.Fatalf("mixed-width page wrong: %q %d %d %v", page, total, end, ok)
	}
	page, total, end, ok = resultPage(text, 4, 2)
	if !ok || page != "" || total != 4 || end != 4 {
		t.Fatalf("page at end wrong: %q %d %d %v", page, total, end, ok)
	}
	if _, _, _, ok := resultPage(text, 5, 2); ok {
		t.Fatal("offset past end accepted")
	}
}

func TestSessionToolActivityListsMetadataForDisplayedSources(t *testing.T) {
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
	scope := chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-user"}
	base := time.Now().UTC().Truncate(time.Second)
	save := func(store *toolhistory.Store, sc chathistory.Scope, source, tool, state string, at int) toolhistory.Record {
		record := toolhistory.Record{Scope: sc, RefID: toolhistory.NewRef(), RunID: "fixture-run", ActorID: "fixture-user", Tool: tool,
			SourceMessageID: source, State: state, OccurredAt: base.Add(time.Duration(at) * time.Second),
			ArgumentsRaw: "fixture-private-arguments", ResultRaw: "fixture-private-result"}
		if err := store.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	store := toolhistory.NewStore(db, "fixture-a")
	first := save(store, scope, "source-1", "memory", "complete", 1)
	second := save(store, scope, "source-1", "send_message", "running", 2)
	save(store, scope, "source-2", "stay_silent", "complete", 3)
	save(store, chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-other-user"}, "source-1", "other_scope_tool", "complete", 4)
	save(toolhistory.NewStore(db, "fixture-b"), scope, "source-1", "other_instance_tool", "complete", 5)

	service := New(&llm.Engine{ToolHistory: store}, "test", "")
	response, err := service.GetSessionToolActivity(ctx, connect.NewRequest(&v1.GetSessionToolActivityRequest{
		SessionId: "private:fixture-user", SourceMessageIds: []string{"source-1", "source-absent"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Msg.Activities) != 1 {
		t.Fatalf("only the requested source with calls may be listed: %v", response.Msg.Activities)
	}
	activity := response.Msg.Activities[0]
	if activity.SourceMessageId != "source-1" || activity.Total != 2 || len(activity.Entries) != 2 {
		t.Fatalf("unexpected activity: %v", activity)
	}
	if activity.Entries[0].RefId != first.RefID || activity.Entries[0].Tool != "memory" {
		t.Fatalf("calls must be listed oldest first: %v", activity.Entries[0])
	}
	if activity.Entries[1].RefId != second.RefID || activity.Entries[1].Tool != "send_message" {
		t.Fatalf("unfinished calls must stay listed: %v", activity.Entries[1])
	}
	wire, err := protojson.Marshal(response.Msg)
	if err != nil || strings.Contains(string(wire), "fixture-private") {
		t.Fatalf("listing leaked arguments or results: %s %v", wire, err)
	}
}

func TestSessionToolActivityRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	manager := llm.NewSessionManager()
	mock := manager.GetOrCreate("private:fixture-mock-user")
	mock.MarkMock()
	service := New(&llm.Engine{ToolHistory: &toolhistory.Store{}, SessionManager: manager}, "test", "")
	tooMany := make([]string, sessionToolActivityMaxSources+1)
	for i := range tooMany {
		tooMany[i] = "source"
	}
	for name, request := range map[string]*v1.GetSessionToolActivityRequest{
		"empty session":  {},
		"bad session":    {SessionId: "not-a-session"},
		"too many ids":   {SessionId: "private:fixture-user", SourceMessageIds: tooMany},
		"oversized id":   {SessionId: "private:fixture-user", SourceMessageIds: []string{strings.Repeat("x", sessionToolActivityMaxSourceLen+1)}},
		"simulated chat": {SessionId: mock.ID(), SourceMessageIds: []string{"source"}},
	} {
		if _, err := service.GetSessionToolActivity(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	unavailable := New(&llm.Engine{}, "test", "")
	if _, err := unavailable.GetSessionToolActivity(ctx, connect.NewRequest(&v1.GetSessionToolActivityRequest{SessionId: "private:fixture-user"})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("missing archive availability ignored", err)
	}
}
