package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/memory"
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
	requests  []core.ChatRequest
	responses []*core.ChatResponse
	errs      []error
}

func (m *mockLLMProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, req)
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

func TestAstrBotReplySilentTurnDoesNotAddAssistantHistory(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{{
						ID:   "call_silent",
						Type: "function",
						Function: core.ToolCallFunction{
							Name:      llm.StaySilentToolName,
							Arguments: `{}`,
						},
					}},
				},
			},
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "第二轮正常回复",
				},
			},
		},
	}
	engine := newTestEngine(provider)
	staySilent := tools.StaySilentTool()
	engine.ToolRegistry[staySilent.Name()] = staySilent
	privateEvent := func(messageID, text string) Event {
		return Event{
			Type:        "event",
			EventType:   "message",
			MessageID:   messageID,
			UserID:      "usr_silent",
			SenderName:  "SilentUser",
			Content:     text,
			Platform:    "astrbot",
			MessageType: "private",
			Timestamp:   time.Now().Unix(),
		}
	}

	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	waitHistory := func(want int) []llm.ChatMessage {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			history := engine.SessionManager.GetOrCreate("astrbot:private:usr_silent").Snapshot()
			if len(history) >= want {
				return history
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("等待 session history 达到 %d 条超时", want)
		return nil
	}

	firstEvent, err := json.Marshal(privateEvent("msg_silent_1", "第一轮无需回复"))
	if err != nil {
		t.Fatalf("序列化第一轮事件失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, firstEvent); err != nil {
		t.Fatalf("发送第一轮事件失败: %v", err)
	}
	firstHistory := waitHistory(1)
	if len(firstHistory) != 1 || firstHistory[0].Role != "user" {
		t.Fatalf("静默轮次应只保留 user message，实际=%+v", firstHistory)
	}
	if content, ok := firstHistory[0].Content.(string); ok && strings.TrimSpace(content) == llm.AssistantSilentMarker {
		t.Fatal("静默轮次不应写入 AssistantSilentMarker")
	}

	secondEvent, err := json.Marshal(privateEvent("msg_silent_2", "第二轮请回复"))
	if err != nil {
		t.Fatalf("序列化第二轮事件失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, secondEvent); err != nil {
		t.Fatalf("发送第二轮事件失败: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("设置读取超时失败: %v", err)
	}
	_, responseBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取第二轮回复失败: %v", err)
	}
	var action Action
	if err := json.Unmarshal(responseBytes, &action); err != nil {
		t.Fatalf("解析第二轮回复失败: %v", err)
	}
	if action.Content != "第二轮正常回复" {
		t.Fatalf("第二轮回复内容不符: %q", action.Content)
	}

	history := waitHistory(3)
	if len(history) != 3 {
		t.Fatalf("期望历史为 user/user/assistant 三条，实际=%d: %+v", len(history), history)
	}
	wantRoles := []string{"user", "user", "assistant"}
	for i, want := range wantRoles {
		if history[i].Role != want {
			t.Fatalf("history[%d] role 期望=%s，实际=%s", i, want, history[i].Role)
		}
		if content, ok := history[i].Content.(string); ok && strings.TrimSpace(content) == llm.AssistantSilentMarker {
			t.Fatalf("history[%d] 不应包含静默标记", i)
		}
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

	// 1. 测试未唤醒的群聊闲聊消息：应返回 noop 并不触发 LLM 回复
	unwokenEvent := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_grp_001",
		UserID:      "usr_123",
		SenderName:  "FoxMember",
		GroupID:     "grp_456",
		GroupName:   "FoxGroup",
		Content:     "群聊闲聊消息",
		Platform:    "astrbot",
		MessageType: "group",
		Timestamp:   time.Now().Unix(),
	}
	unwokenData, _ := json.Marshal(unwokenEvent)
	if err := conn.WriteMessage(websocket.TextMessage, unwokenData); err != nil {
		t.Fatalf("发送未唤醒群聊事件失败: %v", err)
	}

	_, noopBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 noop 响应失败: %v", err)
	}
	var noopAction Action
	if err := json.Unmarshal(noopBytes, &noopAction); err != nil {
		t.Fatalf("解析 noop action 失败: %v", err)
	}
	if noopAction.Action != "noop" {
		t.Errorf("未唤醒群聊期望 action=noop, 实际=%s", noopAction.Action)
	}

	// 2. 测试带唤醒标识 (IsWake/At/Mention) 的群聊消息：应正常回复
	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_grp_002",
		UserID:      "usr_123",
		SenderName:  "FoxMember",
		GroupID:     "grp_456",
		GroupName:   "FoxGroup",
		Content:     "霜降 你好呀",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
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

func TestAstrBotMentionOnlyInteractionRequiresAt(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  bool
	}{
		{
			name:  "mention only",
			event: Event{MessageType: "group", IsAt: true},
			want:  true,
		},
		{
			name:  "wake only",
			event: Event{MessageType: "group", IsWake: true},
		},
		{
			name:  "mention with text",
			event: Event{MessageType: "group", Content: "你好", IsWake: true, IsAt: true},
		},
		{
			name: "mention with attachment",
			event: Event{
				MessageType: "group",
				Attachments: []core.Attachment{{Type: core.AttachmentTypeImage}},
				IsWake:      true,
				IsAt:        true,
			},
		},
		{
			name:  "private mention",
			event: Event{MessageType: "private", IsWake: true, IsAt: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMentionOnlyInteraction(tt.event); got != tt.want {
				t.Fatalf("isMentionOnlyInteraction()=%v, want=%v", got, tt.want)
			}
		})
	}
}

func TestAstrBotMentionOnlyUsesRecentGroupContext(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	precedingEvent := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_mention_context",
		UserID:      "usr_mention",
		SenderName:  "用户A",
		GroupID:     "grp_mention",
		GroupName:   "提及测试群",
		Content:     "今晚吃什么？",
		Platform:    "astrbot",
		MessageType: "group",
		Timestamp:   time.Now().Unix(),
	}
	precedingData, _ := json.Marshal(precedingEvent)
	if err := conn.WriteMessage(websocket.TextMessage, precedingData); err != nil {
		t.Fatalf("发送前置群聊消息失败: %v", err)
	}

	_, noopBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取前置消息 noop 失败: %v", err)
	}
	var noopAction Action
	if err := json.Unmarshal(noopBytes, &noopAction); err != nil {
		t.Fatalf("解析前置消息 noop 失败: %v", err)
	}
	if noopAction.Action != "noop" {
		t.Fatalf("前置未唤醒消息期望 action=noop, 实际=%s", noopAction.Action)
	}

	mentionEvent := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_mention_only",
		UserID:      "usr_mention",
		SenderName:  "用户A",
		GroupID:     "grp_mention",
		GroupName:   "提及测试群",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		IsAt:        true,
		Timestamp:   time.Now().Unix(),
	}
	mentionData, _ := json.Marshal(mentionEvent)
	if err := conn.WriteMessage(websocket.TextMessage, mentionData); err != nil {
		t.Fatalf("发送 mention-only 事件失败: %v", err)
	}

	_, replyBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 mention-only 回复失败: %v", err)
	}
	var replyAction Action
	if err := json.Unmarshal(replyBytes, &replyAction); err != nil {
		t.Fatalf("解析 mention-only 回复失败: %v", err)
	}
	if replyAction.Action != "send_message" {
		t.Fatalf("mention-only 事件应触发回复, 实际 action=%s", replyAction.Action)
	}

	provider.mu.Lock()
	if len(provider.requests) == 0 {
		provider.mu.Unlock()
		t.Fatal("mention-only 事件应触发 LLM 请求")
	}
	lastRequest := provider.requests[len(provider.requests)-1]
	provider.mu.Unlock()

	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	requestContent, _ := lastMessage.Content.(string)
	if !strings.HasPrefix(requestContent, "User Message: \n\n") {
		t.Fatalf("mention-only 事件不应伪造用户文本, 实际: %s", requestContent)
	}
	if !strings.Contains(requestContent, "今晚吃什么？") {
		t.Fatalf("mention-only 请求应包含前置群聊上下文, 实际: %s", requestContent)
	}
	if !strings.Contains(requestContent, "<recent_group_messages>") {
		t.Fatalf("mention-only 请求应包含 recent_group_messages, 实际: %s", requestContent)
	}

	const systemContextStart = "<system_context>\n"
	start := strings.Index(requestContent, systemContextStart)
	end := strings.Index(requestContent, "\n</system_context>")
	if start == -1 || end <= start {
		t.Fatalf("mention-only 请求缺少完整 system_context: %s", requestContent)
	}
	var systemContext map[string]any
	if err := json.Unmarshal([]byte(requestContent[start+len(systemContextStart):end]), &systemContext); err != nil {
		t.Fatalf("解析 mention-only system_context 失败: %v", err)
	}
	for _, key := range []string{"is_wake", "is_at", "mention_only"} {
		if value, ok := systemContext[key].(bool); !ok || !value {
			t.Errorf("system_context.%s 应为 true, 实际=%v", key, systemContext[key])
		}
	}
	guidance, _ := systemContext["interaction_guidance"].(string)
	if !strings.Contains(guidance, "recent_group_messages") {
		t.Errorf("mention-only 指引应要求结合 recent_group_messages, 实际=%q", guidance)
	}

	groupContext := engine.SessionManager.
		GetOrCreate("astrbot:group:grp_mention").
		SnapshotGroupContext(10, 1000, "")
	precedingMessageFound := false
	for _, message := range groupContext.RecentStructuredMessages {
		if strings.TrimSpace(message.Content) == "" || message.MessageID == mentionEvent.MessageID {
			t.Fatalf("mention-only 空事件不应进入群聊 compact 历史: %+v", message)
		}
		if message.MessageID == precedingEvent.MessageID && message.Content == precedingEvent.Content {
			precedingMessageFound = true
		}
	}
	if !precedingMessageFound {
		t.Fatalf("群聊 compact 历史应保留前置消息, 实际=%+v", groupContext.RecentStructuredMessages)
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

func TestAstrBotSendHookEmptyFinalKeepsDeliveredReply(t *testing.T) {
	toolProvider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_empty_final",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "send_message",
								Arguments: `{"messages":[{"type":"plain","text":"工具已发送的实际回复"}]}`,
							},
						},
					},
				},
			},
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: nil,
				},
			},
		},
	}

	engine := newTestEngine(toolProvider)
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
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
		MessageID:   "msg_empty_final",
		UserID:      "usr_empty_final",
		SenderName:  "测试用户",
		GroupID:     "grp_empty_final",
		GroupName:   "空最终回复测试群",
		Content:     "请通过工具回复",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
	data, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("发送事件失败: %v", err)
	}

	_, hookBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取工具回复失败: %v", err)
	}
	var hookAction Action
	if err := json.Unmarshal(hookBytes, &hookAction); err != nil {
		t.Fatalf("解析工具回复失败: %v", err)
	}
	if !hookAction.IsIntermediate || hookAction.Content != "工具已发送的实际回复" {
		t.Fatalf("工具回复不正确: %+v", hookAction)
	}

	_, terminalBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取终止动作失败: %v", err)
	}
	var terminalAction Action
	if err := json.Unmarshal(terminalBytes, &terminalAction); err != nil {
		t.Fatalf("解析终止动作失败: %v", err)
	}
	if terminalAction.Action != "noop" {
		t.Fatalf("空最终回复不应再次发送空消息，实际 action=%+v", terminalAction)
	}

	session := engine.SessionManager.GetOrCreate("astrbot:group:grp_empty_final")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		history := session.Snapshot()
		if len(history) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	history := session.Snapshot()
	if len(history) != 2 {
		t.Fatalf("期望历史包含 user 和实际工具回复，实际=%+v", history)
	}
	if history[1].Role != "assistant" || history[1].Content != "工具已发送的实际回复" {
		t.Fatalf("工具回复未提升为最终 assistant 历史: %+v", history[1])
	}

	groupContext := session.SnapshotGroupContext(10, 1000, "")
	assistantMessages := 0
	for _, message := range groupContext.RecentStructuredMessages {
		if message.Role != "assistant" {
			continue
		}
		assistantMessages++
		if message.Content != "工具已发送的实际回复" {
			t.Fatalf("Prompt Inspector 中的 assistant 内容不正确: %+v", message)
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("Prompt Inspector 应保留一条实际工具回复，实际=%+v", groupContext.RecentStructuredMessages)
	}
}

func TestComposeReplyWithReceiptAvoidsLeadingBlankLines(t *testing.T) {
	if got := composeReplyWithReceipt("", "计费回执"); got != "计费回执" {
		t.Fatalf("空最终回复拼接计费回执不应产生前导空行，实际=%q", got)
	}
	if got := composeReplyWithReceipt("最终回复", "计费回执"); got != "最终回复\n\n计费回执" {
		t.Fatalf("非空最终回复应与计费回执分段，实际=%q", got)
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

func TestAstrBotGroupCompactAndMemoryIntegration(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "我是霜降，群聊总结与记忆测试正常！",
				},
			},
		},
	}
	engine := newTestEngine(provider)
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 1. 发送群聊闲聊消息（无唤醒）
	chatMsg := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_compact_001",
		UserID:      "usr_alice",
		SenderName:  "Alice",
		GroupID:     "group_fox_99",
		GroupName:   "FoxDen",
		Content:     "今天天气真不错呀",
		Platform:    "astrbot",
		MessageType: "group",
		Timestamp:   time.Now().Unix(),
	}
	chatData, _ := json.Marshal(chatMsg)
	if err := conn.WriteMessage(websocket.TextMessage, chatData); err != nil {
		t.Fatalf("发送闲聊消息失败: %v", err)
	}

	// 接收 noop
	_, noopBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 noop 失败: %v", err)
	}
	var noopAction Action
	_ = json.Unmarshal(noopBytes, &noopAction)
	if noopAction.Action != "noop" {
		t.Errorf("期望 action=noop, 实际=%s", noopAction.Action)
	}

	// 验证群聊 running compact buffer 已正确记录
	sess := engine.SessionManager.GetOrCreate("astrbot:group:group_fox_99")
	if sess == nil {
		t.Fatalf("期望创建 session astrbot:group:group_fox_99")
	}

	// 2. 发送唤醒对话消息
	wakeMsg := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_wake_002",
		UserID:      "usr_bob",
		SenderName:  "Bob",
		GroupID:     "group_fox_99",
		GroupName:   "FoxDen",
		Content:     "霜降 你好呀",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
	wakeData, _ := json.Marshal(wakeMsg)
	if err := conn.WriteMessage(websocket.TextMessage, wakeData); err != nil {
		t.Fatalf("发送唤醒消息失败: %v", err)
	}

	// 接收回复
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取回复失败: %v", err)
	}
	var respAction Action
	_ = json.Unmarshal(respBytes, &respAction)
	if respAction.Content != "我是霜降，群聊总结与记忆测试正常！" {
		t.Errorf("回复内容不符: %s", respAction.Content)
	}

	// 验证会话历史中已包含该轮对话
	history := sess.Snapshot()
	deadline := time.Now().Add(time.Second)
	for len(history) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		history = sess.Snapshot()
	}
	if len(history) < 2 {
		t.Fatalf("期望会话历史至少 2 条消息，实际=%d", len(history))
	}
}

func TestAstrBot_GroupRawContextAndDurableSeparation(t *testing.T) {
	mockLLM := &mockLLMProvider{}
	engine := newTestEngine(mockLLM)
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 1. 发送闲聊消息进入 compact buffer
	idleMsg := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_astr_001",
		UserID:      "usr_charlie",
		SenderName:  "Charlie",
		GroupID:     "grp_test_88",
		GroupName:   "FoxTest",
		Content:     "大家晚上好呀",
		Platform:    "astrbot",
		MessageType: "group",
		Timestamp:   time.Now().Unix(),
	}
	idleData, _ := json.Marshal(idleMsg)
	if err := conn.WriteMessage(websocket.TextMessage, idleData); err != nil {
		t.Fatalf("发送闲聊消息失败: %v", err)
	}

	_, noopBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 noop 失败: %v", err)
	}
	var noopAction Action
	_ = json.Unmarshal(noopBytes, &noopAction)
	if noopAction.Action != "noop" {
		t.Errorf("期望 action=noop, 实际=%s", noopAction.Action)
	}

	// 设置 running summary
	sess := engine.SessionManager.GetOrCreate("astrbot:group:grp_test_88")
	sess.SetGroupRunningSummary("群友互相打招呼")

	// 2. 发送触发消息 (@bot)
	wakeMsg := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_astr_002",
		UserID:      "usr_david",
		SenderName:  "David",
		GroupID:     "grp_test_88",
		GroupName:   "FoxTest",
		Content:     "霜降 晚上好！",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
	wakeData, _ := json.Marshal(wakeMsg)
	if err := conn.WriteMessage(websocket.TextMessage, wakeData); err != nil {
		t.Fatalf("发送唤醒消息失败: %v", err)
	}

	// 接收回复
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取回复失败: %v", err)
	}
	var respAction Action
	_ = json.Unmarshal(respBytes, &respAction)
	if respAction.Action != "send_message" {
		t.Errorf("期望 action=send_message, 实际=%s", respAction.Action)
	}

	// 3. 验证 LLM 接收到的临时请求上下文
	mockLLM.mu.Lock()
	if len(mockLLM.requests) == 0 {
		mockLLM.mu.Unlock()
		t.Fatalf("期望 LLM 收到请求，实际未收到")
	}
	lastReq := mockLLM.requests[len(mockLLM.requests)-1]
	mockLLM.mu.Unlock()

	lastMsg := lastReq.Messages[len(lastReq.Messages)-1]
	reqContent, _ := lastMsg.Content.(string)

	if !strings.Contains(reqContent, "<group_running_summary>\n群友互相打招呼\n</group_running_summary>") {
		t.Errorf("期望 LLM 请求包含 group_running_summary，实际内容: %s", reqContent)
	}
	if !strings.Contains(reqContent, "<recent_group_messages>") {
		t.Errorf("期望 LLM 请求包含 recent_group_messages，实际内容: %s", reqContent)
	}
	if !strings.Contains(reqContent, "大家晚上好呀") {
		t.Errorf("期望 LLM 请求包含闲聊消息，实际内容: %s", reqContent)
	}
	// 验证去重：当前触发消息内容不应重复出现在 recent_group_messages 中
	if strings.Contains(reqContent, "<recent_group_messages>") {
		recentBlock := reqContent[strings.Index(reqContent, "<recent_group_messages>"):strings.Index(reqContent, "</recent_group_messages>")]
		if strings.Contains(recentBlock, "晚上好！") {
			t.Errorf("触发消息文本 '晚上好！' 不应出现在 recent_group_messages 中: %s", recentBlock)
		}
	}

	// 4. 验证持久化 Session History 并没有被污染
	history := sess.Snapshot()
	deadline := time.Now().Add(time.Second)
	for len(history) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		history = sess.Snapshot()
	}
	if len(history) < 2 {
		t.Fatalf("期望 session 至少包含 2 条消息，实际=%d", len(history))
	}
	durableUserMsg := history[0].Content.(string)
	if strings.Contains(durableUserMsg, "<group_running_summary>") {
		t.Errorf("持久 Session History 严禁包含 <group_running_summary>: %s", durableUserMsg)
	}
	if strings.Contains(durableUserMsg, "<recent_group_messages>") {
		t.Errorf("持久 Session History 严禁包含 <recent_group_messages>: %s", durableUserMsg)
	}
}

func TestAstrBotTransportWriteFailureDoesNotCommitAssistantState(t *testing.T) {
	t.Setenv("MEMORY_EXTRACT_BATCH_MIN", "3")
	t.Setenv("MEMORY_EXTRACT_BATCH_MAX", "3")

	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "这是一条发送失败的回复",
				},
			},
		},
	}
	engine := newTestEngine(provider)
	engine.MemoryWriter = memory.NewWriter(nil)

	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_failed_write_001",
		UserID:      "usr_failed_write",
		SenderName:  "测试用户",
		GroupID:     "grp_failed_write",
		GroupName:   "传输失败测试群",
		Content:     "请回复这条消息",
		Platform:    "astrbot",
		MessageType: "group",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
	captureGroupCompactMessage(event, engine)

	reply(event, engine, &wsConn{})

	sess := engine.SessionManager.GetOrCreate("astrbot:group:grp_failed_write")
	history := sess.Snapshot()
	if len(history) != 1 {
		t.Fatalf("传输写入失败后只应保留 user 历史，实际消息数=%d", len(history))
	}
	if history[0].Role != "user" {
		t.Fatalf("传输写入失败后历史只应包含 user，实际 role=%s", history[0].Role)
	}
	if content, _ := history[0].Content.(string); strings.Contains(content, "这是一条发送失败的回复") {
		t.Fatalf("失败的 assistant 回复严禁进入历史: %s", content)
	}
	if pending := sess.PendingTurnCount(); pending != 0 {
		t.Fatalf("传输写入失败后不应累计自动记忆提取，实际 pending=%d", pending)
	}

	groupContext := sess.SnapshotGroupContext(10, 1000, "")
	for _, message := range groupContext.RecentStructuredMessages {
		if message.Role == "assistant" || strings.Contains(message.Content, "这是一条发送失败的回复") {
			t.Fatalf("失败的 assistant 回复严禁进入 compact buffer: %+v", message)
		}
	}

	failure := sess.TakeDeliveryFailure()
	if failure == nil {
		t.Fatal("传输写入失败后应记录 DeliveryFailure")
	}
	if failure.Platform != "astrbot" || failure.Action != "send_message" {
		t.Fatalf("DeliveryFailure 元数据不正确: %+v", failure)
	}
	if !strings.Contains(failure.Wording, "connection closed") {
		t.Fatalf("DeliveryFailure 应包含传输错误，实际: %+v", failure)
	}
}
