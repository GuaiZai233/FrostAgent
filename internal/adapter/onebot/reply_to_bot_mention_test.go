package onebot

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
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
			if !requestHasSleepExchange(requests[0].Messages) {
				t.Fatalf("model request lacks the backend sleep exchange: %+v", requests[0].Messages)
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
