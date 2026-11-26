package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// replyBotSleepStub stands in for time_sleep so the test does not wait the real
// ten seconds; it only records the arguments the backend passed.
type replyBotSleepStub struct {
	mu    sync.Mutex
	calls []string
}

func (s *replyBotSleepStub) Name() string               { return timesleep.ToolName }
func (s *replyBotSleepStub) Description() string        { return "synthetic sleep" }
func (s *replyBotSleepStub) Parameters() map[string]any { return nil }
func (s *replyBotSleepStub) Execute(args string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, args)
	return "已挂起 10 秒", nil
}

func (s *replyBotSleepStub) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func TestReplyToBotMentionOnlyForcesBackendSleep(t *testing.T) {
	const selfID = 9
	cases := []struct {
		name      string
		message   string
		wantSleep bool
	}{
		{
			name:      "reply to bot with only @bot",
			message:   `[{"type":"reply","data":{"id":"44"}},{"type":"at","data":{"qq":"9"}}]`,
			wantSleep: true,
		},
		{
			name:      "reply to bot with @bot and text",
			message:   `[{"type":"reply","data":{"id":"44"}},{"type":"at","data":{"qq":"9"}},{"type":"text","data":{"text":" 你好"}}]`,
			wantSleep: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ENABLE_QUOTE_TRACE", "true")
			t.Setenv("QUOTE_TRACE_DEPTH", "1")
			dialogue := &mockLLMProvider{responses: []*core.ChatResponse{{
				Message: core.ChatMessage{Role: core.RoleAssistant, Content: "收到"},
			}}}
			engine := newTestEngine(dialogue)
			stub := &replyBotSleepStub{}
			engine.ToolRegistry[timesleep.ToolName] = stub

			srv, wsURL := startReplyWSTestServer(t, engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))

			event := model.OneBotEvent{
				SelfID: selfID, PostType: "message", MessageType: "group",
				UserID: 8, GroupID: 7, MessageID: 45,
				Message: json.RawMessage(tc.message),
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			answerGroupTurnLookups(t, conn, selfID)

			dialogue.mu.Lock()
			reqCount := dialogue.reqCount
			var requests []core.ChatRequest
			requests = append(requests, dialogue.requests...)
			dialogue.mu.Unlock()
			if reqCount != 1 {
				t.Fatalf("dialogue calls = %d, want 1", reqCount)
			}

			sleeps := stub.snapshot()
			if !tc.wantSleep {
				if len(sleeps) != 0 {
					t.Fatalf("unexpected backend sleep: %v", sleeps)
				}
				return
			}
			if len(sleeps) != 1 || sleeps[0] != `{"seconds":10}` {
				t.Fatalf("backend sleep calls = %v, want one {\"seconds\":10}", sleeps)
			}
			if requestHasSleepExchange(requests[0].Messages) {
				t.Fatalf("backend wait leaked into the provider conversation as a tool call: %+v", requests[0].Messages)
			}
		})
	}
}

// answerGroupTurnLookups answers the adapter's metadata and quote lookups until
// the group reply is sent. The quoted message is a bot-authored message in group 7.
func answerGroupTurnLookups(t *testing.T, conn *websocket.Conn, selfID int64) {
	t.Helper()
	for {
		var action model.OneBotAction
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatalf("read adapter action: %v", err)
		}
		var data any = map[string]any{}
		switch action.Action {
		case "send_group_msg":
			return
		case "get_group_info":
			data = map[string]any{"group_id": 7, "group_name": "synthetic-room"}
		case "get_msg":
			data = map[string]any{
				"message_id": 44, "message_type": "group", "group_id": 7,
				"user_id": selfID,
				"message": []map[string]any{{"type": "text", "data": map[string]any{"text": "Bot: 上一条"}}},
			}
		}
		if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": data}); err != nil {
			t.Fatal(err)
		}
	}
}

// requestHasSleepExchange reports whether a request carries a time_sleep call
// followed by its tool result.
func requestHasSleepExchange(messages []core.ChatMessage) bool {
	callIDs := map[string]bool{}
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			if call.Function.Name == timesleep.ToolName {
				callIDs[call.ID] = true
			}
		}
	}
	for _, msg := range messages {
		if msg.Role == core.RoleTool && callIDs[msg.ToolCallID] {
			return true
		}
	}
	return false
}

var _ llm.ToolExecutor = (*replyBotSleepStub)(nil)

func TestReplyToBotMentionWaitInterruptedBySameSender(t *testing.T) {
	t.Setenv("ENABLE_QUOTE_TRACE", "true")
	t.Setenv("QUOTE_TRACE_DEPTH", "1")
	const selfID = 9
	dialogue := &mockLLMProvider{responses: []*core.ChatResponse{{
		Message: core.ChatMessage{Role: core.RoleAssistant, Content: "收到新消息"},
	}}}
	engine := newTestEngine(dialogue)
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	const instanceID = "fixture-instance"
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: instanceID, Name: "Fixture", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	engine.ToolHistory = toolhistory.NewStore(db, instanceID)
	started := make(chan struct{})
	var signalStarted sync.Once
	engine.ToolRegistry[timesleep.ToolName] = tools.TimeSleepTool(func(string) string {
		signalStarted.Do(func() { close(started) })
		return ""
	})

	srv, wsURL := startReplyWSTestServer(t, engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Answer every adapter lookup from a reader goroutine so the test can wait on the wait itself.
	sent := make(chan model.OneBotAction, 8)
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}
	go func() {
		for {
			var action model.OneBotAction
			if err := conn.ReadJSON(&action); err != nil {
				close(sent)
				return
			}
			var data any = map[string]any{}
			switch action.Action {
			case "get_group_info":
				data = map[string]any{"group_id": 7, "group_name": "synthetic-room"}
			case "get_msg":
				data = map[string]any{
					"message_id": 44, "message_type": "group", "group_id": 7, "user_id": selfID,
					"message": []map[string]any{{"type": "text", "data": map[string]any{"text": "Bot: 上一条"}}},
				}
			case "send_group_msg":
				sent <- action
			}
			_ = write(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": data})
		}
	}()

	first := model.OneBotEvent{
		SelfID: selfID, PostType: "message", MessageType: "group", UserID: 8, GroupID: 7, MessageID: 45,
		Message: json.RawMessage(`[{"type":"reply","data":{"id":"44"}},{"type":"at","data":{"qq":"9"}}]`),
	}
	if err := write(first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("backend wait did not start")
	}
	records, err := engine.ToolHistory.Export(ctx)
	if err != nil || len(records) != 1 || records[0].State != "running" {
		t.Fatalf("expected one running wait in the archive, got %+v, %v", records, err)
	}
	beforeInterrupt := records[0]
	if beforeInterrupt.SourceMessageID != fmt.Sprint(first.MessageID) || beforeInterrupt.Tool != timesleep.ToolName {
		t.Fatalf("running wait attributed to the wrong message or tool: %+v", beforeInterrupt)
	}

	// The same sender interrupts the wait with a new @bot message.
	second := model.OneBotEvent{
		SelfID: selfID, PostType: "message", MessageType: "group", UserID: 8, GroupID: 7, MessageID: 46,
		Message: json.RawMessage(`[{"type":"at","data":{"qq":"9"}},{"type":"text","data":{"text":" 新消息"}}]`),
	}
	if err := write(second); err != nil {
		t.Fatal(err)
	}

	select {
	case action, ok := <-sent:
		if !ok {
			t.Fatal("connection closed before any reply")
		}
		raw, _ := json.Marshal(action.Params)
		if !strings.Contains(string(raw), "收到新消息") {
			t.Fatalf("first reply should come from the interrupting message only: %s", raw)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("interrupting message produced no reply")
	}

	dialogue.mu.Lock()
	reqCount := dialogue.reqCount
	var lastUser string
	if reqCount > 0 {
		for _, msg := range dialogue.requests[reqCount-1].Messages {
			if msg.Role == core.RoleUser {
				lastUser = fmt.Sprint(msg.Content)
			}
		}
	}
	dialogue.mu.Unlock()
	if reqCount != 1 {
		t.Fatalf("dialogue calls = %d, want 1 (only the interrupting turn)", reqCount)
	}
	if !strings.Contains(lastUser, "新消息") {
		t.Fatalf("model answered a stale turn: %s", lastUser)
	}
	if !strings.Contains(lastUser, beforeInterrupt.RefID) {
		t.Fatalf("later turn did not receive the interrupted wait's activity metadata: %s", lastUser)
	}

	// The turn can exit before the tool worker finishes saving its cancellation.
	archiveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		record, err := engine.ToolHistory.Read(archiveCtx, beforeInterrupt.Scope, beforeInterrupt.RefID)
		if err != nil {
			t.Fatalf("interrupted wait disappeared from the archive: %v", err)
		}
		if record.State == "executed" {
			if !record.Restricted || record.Error != context.Canceled.Error() || record.CompletedAt.IsZero() ||
				record.ResultRaw != "" || record.ModelResultRaw != "" {
				t.Fatalf("interrupted wait should retain a restricted cancellation with no result: %+v", record)
			}
			visible := record.ForModel()
			if visible.ArgumentsRaw != "" || visible.Error != "" {
				t.Fatalf("interrupted wait exposed restricted fields: %+v", visible)
			}
			break
		}
		if record.State != "running" {
			t.Fatalf("interrupted wait settled in an unexpected state: %+v", record)
		}
		select {
		case <-archiveCtx.Done():
			t.Fatalf("interrupted wait did not settle: %+v", record)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
