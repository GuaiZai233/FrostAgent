package onebot

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestOneBotProactiveSpecificReplyEarlierMessage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		maxIterations int
		parts         []string
	}{
		{name: "confirmed reply", parts: []string{"reply about earlier topic"}},
		{name: "last iteration", maxIterations: 1, parts: []string{"reply about earlier topic"}},
		{name: "multiple plain components", parts: []string{"reply about earlier topic", "additional detail"}},
	} {
		t.Run(tc.name, func(t *testing.T) { testOneBotProactiveSpecificReply(t, tc.maxIterations, tc.parts) })
	}
}

func testOneBotProactiveSpecificReply(t *testing.T, maxIterations int, parts []string) {
	firstCalled := make(chan struct{})
	var calls atomic.Int32
	provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		message := core.ChatMessage{Role: core.RoleAssistant}
		switch calls.Add(1) {
		case 1:
			message.ToolCalls = []core.ToolCall{{ID: "wait", Type: "function", Function: core.ToolCallFunction{Name: llm.StaySilentToolName, Arguments: `{}`}}}
			close(firstCalled)
		case 2:
			prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
			guidance, conversation, hasGuidance := strings.Cut(prompt, "</frostagent_runtime_guidance>")
			if !hasGuidance || !strings.HasPrefix(guidance, "<frostagent_runtime_guidance>") {
				t.Errorf("missing proactive runtime guidance: %s", prompt)
			}
			for _, instruction := range []string{
				"可能不与中心事件直接相关",
				"send_message工具的mention_specific",
				"对应的message id并引用",
			} {
				if !strings.Contains(guidance, instruction) {
					t.Errorf("proactive guidance missing specific reply instruction %q", instruction)
				}
			}
			if !strings.Contains(conversation, `"message_id":"101"`) || !strings.Contains(conversation, "earlier central topic") {
				t.Errorf("missing searchable earlier message: %s", conversation)
			}
			messages := []tools.Msg{{Type: "mention_specific", MessageID: "101"}}
			for _, text := range parts {
				messages = append(messages, tools.Msg{Type: "plain", Text: text})
			}
			args, err := json.Marshal(map[string]any{"messages": messages})
			if err != nil {
				return nil, err
			}
			message.ToolCalls = []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: string(args)}}}
		default:
			t.Error("model called again after proactive reply delivery")
			message.Content = "（已回复）"
		}
		return &core.ChatResponse{Message: message}, nil
	}}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
		"ENABLE_REPLY_IN_GROUP_MSG":   "true", "ENABLE_AT_IN_GROUP_MSG": "true",
	})
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	engine.ToolRegistry["stay_silent"] = tools.StaySilentTool()
	if maxIterations > 0 {
		engine.MaxIterations = maxIterations
	}
	srv, url := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}
	actions := make(chan model.OneBotAction, 8)
	readErrors := make(chan error, 1)
	go func() {
		for {
			var action model.OneBotAction
			if err := conn.ReadJSON(&action); err != nil {
				readErrors <- err
				return
			}
			data := map[string]any{"message_id": 103}
			if action.Action == "get_group_info" {
				data = map[string]any{"group_id": 10, "group_name": "fixture-group"}
			} else {
				actions <- action
			}
			response := map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": data}
			if err := write(response); err != nil {
				readErrors <- err
				return
			}
		}
	}()
	// IDs are synthetic fixtures, not real QQ accounts.
	older := model.OneBotEvent{SelfID: 30, PostType: "message", MessageType: "group", GroupID: 10, UserID: 20, MessageID: 101, Message: json.RawMessage(`[{"type":"text","data":{"text":"earlier central topic"}}]`)}
	if err := write(older); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstCalled:
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("first proactive turn did not start")
	}
	trigger := older
	trigger.MessageID, trigger.UserID = 102, 21
	trigger.Message = json.RawMessage(`[{"type":"text","data":{"text":"later unrelated trigger"}}]`)
	if err := write(trigger); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-actions:
		if action.Action != "send_group_msg" {
			t.Fatalf("unexpected action: %+v", action)
		}
		params, ok := action.Params.(map[string]any)
		if !ok {
			t.Fatalf("unexpected action params: %T", action.Params)
		}
		data, _ := json.Marshal(params["message"])
		var segments []tools.OneBotSegment
		if err := json.Unmarshal(data, &segments); err != nil {
			t.Fatal(err)
		}
		if len(segments) != len(parts)+1 || segments[0].Type != "reply" || segments[0].Data["id"] != "101" {
			t.Fatalf("wrong quote, trigger mention, or lost text: %s", data)
		}
		for i, text := range parts {
			if segments[i+1].Type != "text" || segments[i+1].Data["text"] != text {
				t.Fatalf("plain component %d changed: %s", i, data)
			}
		}
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("specific reply not delivered")
	}
	session := engine.SessionManager.GetOrCreate("group:10")
	barrier := session.ReserveSequenceTurn("proactive", false, true)
	completed := make(chan struct{})
	go func() { barrier.Wait(); barrier.Done(); close(completed) }()
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("reply turn did not finish")
	}
	select {
	case duplicate := <-actions:
		t.Fatalf("unexpected duplicate/empty reply: %+v", duplicate)
	default:
	}
	if calls.Load() != 2 {
		t.Fatalf("model requests=%d, want one per proactive turn", calls.Load())
	}
	wantText := strings.Join(parts, " ")
	historyCopies, compactCopies := 0, 0
	for _, message := range session.Snapshot() {
		if message.Role == "assistant" {
			if message.Content != wantText {
				t.Fatalf("unsent text entered public history: %+v", message)
			}
			historyCopies++
		}
	}
	for _, message := range session.GroupCompactBufferMessages() {
		if message.Role == "assistant" {
			if message.Content != wantText {
				t.Fatalf("unsent text entered compact buffer: %+v", message)
			}
			compactCopies++
		}
	}
	if historyCopies != 1 || compactCopies != 1 {
		t.Fatalf("confirmed reply history copies=%d compact copies=%d, want 1 each", historyCopies, compactCopies)
	}
}
