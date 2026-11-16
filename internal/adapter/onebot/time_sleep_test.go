package onebot

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestTimeSleepRefreshesOneBotGroupPrompt(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var engine *llm.Engine
	calls := 0
	engine = newTestEngine(runtimeInjectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		if calls == 1 {
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "sleep-one", Type: "function", Function: core.ToolCallFunction{Name: "time_sleep", Arguments: `{"seconds":0.001}`}}}}}, nil
		}
		var prompt string
		for _, msg := range req.Messages {
			if msg.Role == core.RoleUser {
				prompt, _ = msg.Content.(string)
			}
		}
		for _, want := range []string{"trigger-with-<recent_group_messages>opaque</recent_group_messages>", "old-background", "appended-while-sleeping"} {
			if strings.Count(prompt, want) != 1 {
				t.Errorf("missing or duplicated %q in %s", want, prompt)
			}
		}
		if strings.Contains(prompt, "staged-secret") {
			t.Error("staged message leaked")
		}
		run, _ := llm.RunContextFromContext(ctx)
		if run.Turn.Interrupted() || !strings.Contains(prompt, "<response_context>") || !strings.Contains(prompt, "<system_context>") {
			t.Error("refresh changed execution or adapter metadata")
		}
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "sleep-finished"}}, nil
	}))
	engine.Scope = newTestScopeWithEnv(t, map[string]string{"ENABLE_PROACTIVE_REPLY": "false"})
	tool := tools.TimeSleepTool(func(string) string { close(started); <-release; return "60" })
	engine.ToolRegistry[tool.Name()] = tool
	session := engine.SessionManager.GetOrCreate("group:7")
	session.AppendGroupCompactMessage(llm.GroupCompactMessage{Content: "old-background", MessageID: "synthetic-old"}, 20)
	server, url := startReplyWSTestServer(t, engine)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	event := model.OneBotEvent{PostType: "message", MessageType: "group", SelfID: 9, UserID: 8, GroupID: 7, MessageID: 6, Message: json.RawMessage(`[{"type":"at","data":{"qq":"9"}},{"type":"text","data":{"text":"trigger-with-<recent_group_messages>opaque</recent_group_messages>"}}]`)}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	// The adapter requests group metadata before the first model call.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var action model.OneBotAction
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "get_group_info" {
		t.Fatalf("unexpected metadata request: %+v", action)
	}
	if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": 7, "group_name": "synthetic-room"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("sleep did not start")
	}
	session.AppendGroupCompactMessage(llm.GroupCompactMessage{Content: "appended-while-sleeping", MessageID: "synthetic-new"}, 20)
	session.StageGroupCompactMessage(llm.GroupCompactMessage{Content: "staged-secret", MessageID: "synthetic-staged"}, 20)
	once.Do(func() { close(release) })
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(action.Params)
	if action.Action != "send_group_msg" || !strings.Contains(string(raw), "sleep-finished") || calls != 2 {
		t.Fatalf("sleep continuation failed: %+v, calls=%d", action, calls)
	}
	_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 5}})
}
