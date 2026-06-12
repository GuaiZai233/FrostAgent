package onebot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/model"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockLLMProvider 实现 core.LLMProvider，返回预设的响应
type mockLLMProvider struct {
	mu       sync.Mutex
	reqCount int
	// responses 按顺序返回，如果为空则返回默认文本
	responses []*core.ChatResponse
}

func (m *mockLLMProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.reqCount
	m.reqCount++

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	// 默认返回纯文本
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "你好！这是默认的模拟回复。",
		},
	}, nil
}

// newTestEngine 创建一个用于测试的 Engine
func newTestEngine(provider core.LLMProvider) *llm.Engine {
	return &llm.Engine{
		MaxIterations:  3,
		ToolRegistry:   make(map[string]llm.ToolExecutor),
		Provider:       provider,
		BaseURL:        "http://mock",
		APIKey:         "mock-key",
		ModelName:      "mock-model",
		SessionManager: llm.NewSessionManager(),
	}
}

// startWSTestServer 启动一个带 WebSocket handler 的测试服务器
func startWSTestServer(engine *llm.Engine) (*httptest.Server, string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/frostagent", HandleWS(engine))
	srv := httptest.NewServer(mux)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/frostagent"
	return srv, wsURL
}

func TestHandleWSPrivateMessage(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	// 连接 WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 构造一个私聊消息事件
	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   1,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"你好"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	// 发送
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// 读取响应
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	// 解析响应
	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v\n原始响应: %s", err, string(respBytes))
	}

	// 验证 action
	if action.Action != "send_private_msg" {
		t.Errorf("期望 action=send_private_msg, 实际=%s", action.Action)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}

	// 验证 user_id
	if fmt.Sprintf("%v", params["user_id"]) != "987654" {
		t.Errorf("期望 user_id=987654, 实际=%v", params["user_id"])
	}

	// 验证 message 是默认文本回复
	if msg, ok := params["message"].(string); ok {
		if msg != "你好！这是默认的模拟回复。" {
			t.Errorf("期望回复内容=%q, 实际=%q", "你好！这是默认的模拟回复。", msg)
		}
	} else {
		t.Errorf("message 不是字符串: %T", params["message"])
	}

	t.Logf("✅ 私聊消息测试通过，回复内容: %v", params["message"])
}

func TestHandleWSGroupMessageMentioned(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 构造一个群聊 @ 机器人消息
	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "group",
		GroupID:     1040527846,
		UserID:      3127306807,
		MessageID:   2,
		Message: json.RawMessage(fmt.Sprintf(
			`[{"type":"at","data":{"qq":"123456"}},{"type":"text","data":{"text":"你好"}}]`,
		)),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	var action model.OneBotAction
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析响应失败: %v\n原始响应: %s", err, string(respBytes))
	}

	if action.Action != "send_group_msg" {
		t.Errorf("期望 action=send_group_msg, 实际=%s", action.Action)
	}

	params, ok := action.Params.(map[string]interface{})
	if !ok {
		t.Fatalf("params 不是 map: %T", action.Params)
	}

	if fmt.Sprintf("%v", params["group_id"]) != "1040527846" {
		t.Errorf("期望 group_id=1040527846, 实际=%v", params["group_id"])
	}

	t.Logf("✅ 群聊@消息测试通过，回复内容: %v", params["message"])
}

func TestHandleWSGroupMessageNotMentioned(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 群聊消息但没有 @ 机器人
	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "group",
		GroupID:     1919810,
		UserID:      114514,
		MessageID:   3,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"你好"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// 设置读超时，因为没有 @ 机器人不应该收到回复
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("期望没有回复（未@机器人），但收到了消息")
	} else {
		t.Logf("✅ 未@机器人无回复测试通过: %v", err)
	}
}

func TestHandleWSSendMessageToolHook(t *testing.T) {
	// 模拟 LLM：第一轮调用 send_message 工具
	mock := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "好的，我来发送消息~",
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_001",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "send_message",
								Arguments: `{"messages":[{"type":"plain","text":"稍等~ 喵宝正在查询天气！🐱"}]}`,
							},
						},
					},
				},
			},
			{
				// 第二轮：LLM 收到 "消息已发送" 后给出最终回复
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "已发送！",
				},
			},
		},
	}

	engine := newTestEngine(mock)
	// 注册 send_message 工具
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   4,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"查询天气"}}]`),
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// 第一条消息：SendHook 通过 send_message 发出的
	_, resp1Bytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取第一条响应失败: %v", err)
	}

	var action1 model.OneBotAction
	if err := json.Unmarshal(resp1Bytes, &action1); err != nil {
		t.Fatalf("解析第一条响应失败: %v\n原始: %s", err, string(resp1Bytes))
	}

	params1, _ := action1.Params.(map[string]interface{})
	msg1, _ := params1["message"].([]interface{})
	if len(msg1) == 0 {
		t.Fatalf("第一条消息内容为空")
	}
	seg1, _ := msg1[0].(map[string]interface{})
	if seg1["type"] != "text" {
		t.Errorf("期望第一条消息 type=text, 实际=%v", seg1["type"])
	}
	t.Logf("✅ SendHook 发送成功: type=%s, data=%v", seg1["type"], seg1["data"])

	// 第二条消息：LLM 最终文本回复
	_, resp2Bytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取第二条响应失败: %v", err)
	}

	var action2 model.OneBotAction
	if err := json.Unmarshal(resp2Bytes, &action2); err != nil {
		t.Fatalf("解析第二条响应失败: %v\n原始: %s", err, string(resp2Bytes))
	}

	params2, _ := action2.Params.(map[string]interface{})
	msg2, _ := params2["message"].(string)
	if msg2 != "已发送！" {
		t.Errorf("期望最终回复='已发送！', 实际=%q", msg2)
	}
	t.Logf("✅ 最终回复测试通过: %s", msg2)
}
