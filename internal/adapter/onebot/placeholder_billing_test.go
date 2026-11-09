package onebot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// With billing enabled, a confirmed send_message followed by a placeholder final
// must send the delivered text and the receipt, never "(empty)" joined to it.
func TestOneBotBilledPlaceholderAfterDeliveryDropsPlaceholder(t *testing.T) {
	alcyoneSrv, _ := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{{
						ID:       "call_send_1",
						Type:     "function",
						Function: core.ToolCallFunction{Name: "send_message", Arguments: `{"messages":[{"type":"plain","text":"已发送的回复"}]}`},
					}},
				},
				Usage: &core.Usage{PromptTokens: 200, CompletionTokens: 50, TotalTokens: 250},
			},
			{
				Message: core.ChatMessage{Role: core.RoleAssistant, Content: "(empty)"},
				Usage:   &core.Usage{PromptTokens: 200, CompletionTokens: 5, TotalTokens: 205},
			},
		},
	}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{"PROACTIVE_REPLY_PROBABILITY": "0"})
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	engine.BillingClient = billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
	engine.BillingConfig = billing.Config{
		Enabled:          true,
		BaseURL:          alcyoneSrv.URL,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  2048,
		SafetyMultiplier: 1.2,
		ModelName:        "deepseek-chat",
	}

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      30001,
		PostType:    "message",
		MessageType: "group",
		GroupID:     10001,
		UserID:      20001,
		MessageID:   611,
		Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 帮我回复一下"}}]`),
	}
	b, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("发送事件失败: %v", err)
	}

	var sent []string
	deadline := time.Now().Add(5 * time.Second)
	sawReceipt := false
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if sawReceipt {
				break
			}
			continue
		}
		var act model.OneBotAction
		if err := json.Unmarshal(raw, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		switch act.Action {
		case "get_group_info":
			_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": act.Echo, "data": map[string]any{"group_id": 10001, "group_name": "测试群"}})
		case "send_group_msg":
			_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": act.Echo, "data": map[string]any{"message_id": 9}})
			body, _ := json.Marshal(act.Params)
			sent = append(sent, string(body))
			if strings.Contains(string(body), "本次消耗") || strings.Contains(string(body), "雪花") {
				sawReceipt = true
			}
		}
	}

	if !sawReceipt {
		t.Fatalf("未收到计费回执，发送记录: %q", sent)
	}
	joined := strings.Join(sent, "\n")
	if strings.Contains(joined, "(empty)") {
		t.Fatalf("占位式最终文本被发送: %q", sent)
	}
	if !strings.Contains(joined, "已发送的回复") {
		t.Fatalf("已送达的文本缺失: %q", sent)
	}
}
