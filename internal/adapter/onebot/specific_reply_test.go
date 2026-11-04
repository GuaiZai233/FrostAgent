package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestOneBotProactiveSpecificReplyEarlierMessage(t *testing.T) {
	for _, tc := range []struct {
		name, final, wantFollowup string
		terminal                  string
	}{
		{name: "empty final"},
		{name: "duplicate", final: "reply about earlier topic"},
		{name: "whitespace", final: " \nreply about earlier topic\n"},
		{name: "different", final: "different follow-up", wantFollowup: "different follow-up"},
		{name: "structured duplicate", final: `{"messages":[{"type":"plain","text":"reply about earlier topic"}]}`},
		{name: "structured split duplicate", final: `{"messages":[{"type":"plain","text":"reply about "},{"type":"plain","text":"earlier topic"}]}`},
		{name: "structured different", final: `{"messages":[{"type":"plain","text":"different follow-up"}]}`, wantFollowup: "different follow-up"},
		{name: "structured extra text", final: `{"messages":[{"type":"plain","text":"reply about earlier topic"},{"type":"plain","text":"extra content"}]}`, wantFollowup: "extra content"},
		{name: "structured media", final: `{"messages":[{"type":"plain","text":"reply about earlier topic"},{"type":"image","url":"https://example.invalid/fixture.png"}]}`, wantFollowup: "fixture.png"},
		{name: "ignored sticker flag", final: `{"messages":[{"type":"plain","text":"reply about earlier topic","is_sticker":false}]}`},
		{name: "ignored fields", final: `{"extra":"ignored","messages":[{"type":"plain","text":"reply about earlier topic","url":"ignored","path":"ignored","mention_user_id":"fixture-user","custom":{"text":"ignored"}}]}`},
		{name: "second tool call", terminal: "repeat"},
		{name: "stay silent after delivery", terminal: "silent"},
		{name: "provider error after delivery", terminal: "error"},
		{name: "security block after delivery", terminal: "blocked", final: "blocked final"},
		{name: "iteration limit after delivery", terminal: "limit"},
		{name: "render failure after delivery", final: `{"messages":[{"type":"quote","message_id":"999999"},{"type":"plain","text":"untrusted target"}]}`},
		{name: "final delivery failed", final: "failed follow-up", wantFollowup: "failed follow-up", terminal: "failed final"},
	} {
		t.Run(tc.name, func(t *testing.T) { testOneBotProactiveSpecificReply(t, tc.final, tc.wantFollowup, tc.terminal) })
	}
}

func testOneBotProactiveSpecificReply(t *testing.T, final, wantFollowup, terminal string) {
	firstCalled, replyCompleted := make(chan struct{}), make(chan struct{})
	calls := 0
	provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		message := core.ChatMessage{Role: core.RoleAssistant}
		switch calls {
		case 1:
			message.ToolCalls = []core.ToolCall{{ID: "wait", Type: "function", Function: core.ToolCallFunction{Name: llm.StaySilentToolName, Arguments: `{}`}}}
			close(firstCalled)
		case 2:
			prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
			if !strings.Contains(prompt, proactive.PromptPrefix) || !strings.Contains(prompt, `"message_id":"101"`) || !strings.Contains(prompt, "earlier central topic") {
				t.Errorf("missing searchable earlier message or proactive guidance: %s", prompt)
			}
			message.ToolCalls = []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{
				Name: "send_message", Arguments: `{"messages":[{"type":"mention_specific","message_id":"101"},{"type":"plain","text":"reply about earlier topic"}]}`,
			}}}
			if terminal == "limit" {
				close(replyCompleted)
			}
		case 3:
			message.Content = final
			if terminal == "repeat" {
				message.ToolCalls = []core.ToolCall{{ID: "second", Type: "function", Function: core.ToolCallFunction{
					Name: "send_message", Arguments: `{"messages":[{"type":"mention_specific","message_id":"101"},{"type":"plain","text":"second reply"}]}`,
				}}}
			}
			result, _ := req.Messages[len(req.Messages)-1].Content.(string)
			if result != "消息已发送" {
				t.Errorf("delivery did not wait for ACK: %s", result)
			}
			close(replyCompleted)
			if terminal == "error" {
				return nil, errors.New("fixture provider failure")
			}
			if terminal == "silent" {
				message.ToolCalls = []core.ToolCall{{ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: "stay_silent", Arguments: `{}`}}}
			}
		default:
			t.Errorf("unexpected model round %d", calls)
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
	if terminal == "limit" {
		engine.MaxIterations = 1
	}
	if terminal == "blocked" {
		engine.Security = security.NewController(t.TempDir())
		engine.Security.SetMode(security.ControlModeAggressive)
		engine.Security.Watchdog.SetClassifier(&mockClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
			if input.Stage == security.StageModelOutput {
				return security.ClassificationResult{Category: security.RiskCategoryViolenceTerrorism, RiskLevel: security.RiskLevelCritical}, nil
			}
			return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
		}})
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
		sends := 0
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
				sends++
				actions <- action
			}
			response := map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": data}
			if terminal == "failed final" && sends == 2 {
				response["status"], response["retcode"] = "failed", 100
			}
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
		if len(segments) != 2 || segments[0].Type != "reply" || segments[0].Data["id"] != "101" || segments[1].Type != "text" || !strings.Contains(segments[1].Data["text"].(string), "reply about earlier topic") {
			t.Fatalf("wrong quote, trigger mention, or lost text: %s", data)
		}
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("specific reply not delivered")
	}
	select {
	case <-replyCompleted:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not receive delivery ACK")
	}
	if wantFollowup != "" {
		select {
		case action := <-actions:
			data, _ := json.Marshal(action.Params)
			if !strings.Contains(string(data), wantFollowup) {
				t.Fatalf("different final reply was lost: %s", data)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("different final reply was suppressed")
		}
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
	if wantFollowup == "" || terminal == "failed final" {
		historyCopies, compactCopies := 0, 0
		for _, message := range session.Snapshot() {
			if terminal == "failed final" && message.Role == "assistant" && message.Content == final {
				t.Fatal("failed final reply committed as public history")
			}
			if message.Role == "assistant" && message.Content == "reply about earlier topic" {
				historyCopies++
			}
		}
		for _, message := range session.GroupCompactBufferMessages() {
			if message.Role == "assistant" && message.Content == "reply about earlier topic" {
				compactCopies++
			}
		}
		if historyCopies != 1 || compactCopies != 1 {
			t.Fatalf("confirmed reply history copies=%d compact copies=%d, want 1 each", historyCopies, compactCopies)
		}
	}
}
