package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type mockLLMProvider struct {
	mu        sync.Mutex
	reqCount  int
	responses []*core.ChatResponse
	errs      []error
}

func (m *mockLLMProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.reqCount
	m.reqCount++

	if idx < len(m.errs) && m.errs[idx] != nil {
		return nil, m.errs[idx]
	}

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "你好！这是 AstrBot 适配器的测试回复。",
		},
		Usage: &core.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}, nil
}

func newTestEngine(provider core.LLMProvider) *llm.Engine {
	return &llm.Engine{
		MaxIterations:  3,
		ToolRegistry:   make(map[string]llm.ToolExecutor),
		Provider:       provider,
		BaseURL:        "http://mock",
		APIKey:         "mock-key",
		ModelName:      "mock-model",
		SessionManager: llm.NewSessionManager(),
		Dispatcher:     core.NewDefaultDispatcher(),
	}
}

func startWSTestServer(engine *llm.Engine) (*httptest.Server, *Adapter, string) {
	mux := http.NewServeMux()
	adapter := NewAdapter(engine)
	if engine != nil && engine.Dispatcher != nil {
		engine.Dispatcher.RegisterAdapter(adapter)
	}
	mux.HandleFunc("/ws/astrbot", adapter.Handler())
	srv := httptest.NewServer(mux)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/astrbot"
	return srv, adapter, wsURL
}

func TestAstrBotPrivateMessage(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_001",
		UserID:      "usr_123",
		SenderName:  "TestUser",
		Content:     "Hello from AstrBot!",
		Platform:    "astrbot",
		MessageType: "private",
		Timestamp:   time.Now().Unix(),
	}
	data, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("发送事件失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action Action
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析 action 失败: %v, raw: %s", err, string(respBytes))
	}

	if action.Action != "send_message" {
		t.Errorf("期望 action=send_message, 实际=%s", action.Action)
	}
	if action.UserID != "usr_123" {
		t.Errorf("期望 user_id=usr_123, 实际=%s", action.UserID)
	}
	if action.MessageType != "private" {
		t.Errorf("期望 message_type=private, 实际=%s", action.MessageType)
	}
	if action.Content != "你好！这是 AstrBot 适配器的测试回复。" {
		t.Errorf("期望回复文本不符: %s", action.Content)
	}
}

func TestAstrBotGroupMessage(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_grp_001",
		UserID:      "usr_123",
		SenderName:  "FoxMember",
		GroupID:     "grp_456",
		GroupName:   "FoxGroup",
		Content:     "群聊测试消息",
		Platform:    "astrbot",
		MessageType: "group",
		Timestamp:   time.Now().Unix(),
	}
	data, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("发送群聊事件失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action Action
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析 action 失败: %v, raw: %s", err, string(respBytes))
	}

	if action.Action != "send_message" {
		t.Errorf("期望 action=send_message, 实际=%s", action.Action)
	}
	if action.GroupID != "grp_456" {
		t.Errorf("期望 group_id=grp_456, 实际=%s", action.GroupID)
	}
	if action.MessageType != "group" {
		t.Errorf("期望 message_type=group, 实际=%s", action.MessageType)
	}
}

func TestAstrBotSendHook(t *testing.T) {
	toolProvider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "send_message",
								Arguments: `{"messages":[{"type":"plain","text":"正在调用搜索工具..."}]}`,
							},
						},
					},
				},
			},
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "搜索完成，这是最终回答。",
				},
			},
		},
	}

	engine := newTestEngine(toolProvider)
	sendTool := tools.SendMsgTool()
	engine.ToolRegistry[sendTool.Name()] = sendTool

	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_hook_001",
		UserID:      "usr_123",
		Content:     "查一下天气",
		Platform:    "astrbot",
		MessageType: "private",
		Timestamp:   time.Now().Unix(),
	}
	data, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("发送事件失败: %v", err)
	}

	// 读取第 1 个响应（sendHook 中间消息）
	_, hookBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取中间响应失败: %v", err)
	}
	var hookAction Action
	if err := json.Unmarshal(hookBytes, &hookAction); err != nil {
		t.Fatalf("解析 hookAction 失败: %v", err)
	}
	if !hookAction.IsIntermediate {
		t.Errorf("期望 is_intermediate=true, 实际=false")
	}
	if hookAction.Content != "正在调用搜索工具..." {
		t.Errorf("期望中间内容为 '正在调用搜索工具...', 实际=%s", hookAction.Content)
	}

	// 读取第 2 个响应（最终回复）
	_, finalBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取最终响应失败: %v", err)
	}
	var finalAction Action
	if err := json.Unmarshal(finalBytes, &finalAction); err != nil {
		t.Fatalf("解析 finalAction 失败: %v", err)
	}
	if finalAction.IsIntermediate {
		t.Errorf("期望 finalAction is_intermediate=false, 实际=true")
	}
	if finalAction.Content != "搜索完成，这是最终回答。" {
		t.Errorf("期望最终回复为 '搜索完成，这是最终回答。', 实际=%s", finalAction.Content)
	}
}

func TestAstrBotDispatcherSend(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	outMsg := core.OutgoingMessage{
		TargetID:    "target_user_888",
		MessageType: "private",
		Platform:    "astrbot",
		Content:     "Dispatcher proactive notification",
	}

	if err := engine.Dispatcher.Dispatch(context.Background(), "astrbot", outMsg); err != nil {
		t.Fatalf("Dispatcher 分发失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取分发消息失败: %v", err)
	}

	var action Action
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析 action 失败: %v", err)
	}
	if action.UserID != "target_user_888" {
		t.Errorf("期望 user_id=target_user_888, 实际=%s", action.UserID)
	}
	if action.Content != "Dispatcher proactive notification" {
		t.Errorf("期望内容不符: %s", action.Content)
	}
}
