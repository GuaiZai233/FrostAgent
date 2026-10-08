package onebot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"FrostAgent/internal/proactive"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
	"FrostAgent/internal/tools"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newTestScopeWithEnv(t *testing.T, kv map[string]string) *runtimescope.Scope {
	envPath := filepath.Join(t.TempDir(), ".env")
	var lines []string
	for k, v := range kv {
		lines = append(lines, k+"="+v)
	}
	_ = os.WriteFile(envPath, []byte(strings.Join(lines, "\n")), 0600)
	store, _ := instanceconfig.Open(envPath, false)
	return runtimescope.New(store, nil, logs.General)
}

func TestDetectGroupWakeSignalsProactive(t *testing.T) {
	groupEvent := model.OneBotEvent{
		PostType:    "message",
		MessageType: "group",
		GroupID:     10001,
		UserID:      20001,
		SelfID:      30001,
		Message:     json.RawMessage(`"大家今天过得怎么样？"`),
	}

	t.Run("disabled by default", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, nil)
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.001 }, scope)
		if signals.Proactive || signals.Any() {
			t.Fatalf("expected no wake signals, got %+v", signals)
		}
	})

	t.Run("group reply disabled", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.50",
			"GROUP_REPLY_ON_MENTION":      "false",
		})
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.1 }, scope)
		if signals.Proactive || signals.Any() {
			t.Fatalf("expected proactive reply to be suppressed by GROUP_REPLY_ON_MENTION=false")
		}
	})

	t.Run("hit proactive probability", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.35",
		})
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.20 }, scope)
		if !signals.Proactive || !signals.Any() {
			t.Fatalf("expected proactive signal to hit, got %+v", signals)
		}
	})

	t.Run("miss proactive probability", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.35",
		})
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.50 }, scope)
		if signals.Proactive || signals.Any() {
			t.Fatalf("expected proactive signal to miss, got %+v", signals)
		}
	})

	t.Run("explicit at bot does not roll proactive", func(t *testing.T) {
		atEvent := model.OneBotEvent{
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			SelfID:      30001,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":"你好"}}]`),
		}
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
		})
		signals := DetectGroupWakeSignalsWithRNG(atEvent, func() float64 { return 0.01 }, scope)
		if !signals.AtBot {
			t.Fatalf("expected AtBot signal")
		}
		if signals.Proactive {
			t.Fatalf("expected explicit AtBot not to be marked as Proactive")
		}
	})

	t.Run("explicit alias does not roll proactive", func(t *testing.T) {
		aliasEvent := model.OneBotEvent{
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			SelfID:      30001,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"霜降狐 你好呀"}}]`),
		}
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
		})
		signals := DetectGroupWakeSignalsWithRNG(aliasEvent, func() float64 { return 0.01 }, scope)
		if !signals.Alias {
			t.Fatalf("expected Alias signal")
		}
		if signals.Proactive {
			t.Fatalf("expected explicit Alias not to be marked as Proactive")
		}
	})
}

func TestBuildResponseContextWithProactive(t *testing.T) {
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     10001,
	}
	ctxStr := buildResponseContext(event, GroupWakeSignals{Proactive: true}, false)
	if !strings.Contains(ctxStr, `"proactive"`) {
		t.Fatalf("expected triggers to include 'proactive', got: %s", ctxStr)
	}
	if !strings.Contains(ctxStr, proactive.PromptPrefix[:10]) && !strings.Contains(ctxStr, "stay_silent") {
		t.Fatalf("expected silence guidance in response context: %s", ctxStr)
	}
}

func TestOneBotProactiveMultiTurnHistoryPurity(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})

	provider := &mockLLMProvider{
		responses: []*core.ChatResponse{
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{{
						ID:   "call_silent_1",
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
					Content: "这是第二轮显式唤醒的正常回复。",
				},
			},
		},
	}

	engine := newTestEngine(provider)
	engine.Scope = scope
	staySilent := tools.StaySilentTool()
	engine.ToolRegistry[staySilent.Name()] = staySilent

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// Turn 1: 概率触发的主动回复群消息
	event1 := model.OneBotEvent{
		SelfID:      30001,
		PostType:    "message",
		MessageType: "group",
		GroupID:     10001,
		UserID:      20001,
		MessageID:   101,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"大家今天过得怎么样？"}}]`),
	}
	b1, _ := json.Marshal(event1)
	if err := conn.WriteMessage(websocket.TextMessage, b1); err != nil {
		t.Fatalf("发送第一轮事件失败: %v", err)
	}

	// 等待第1轮进入并处理完毕（由于 stay_silent，不向群发送出站消息，历史中写入1条 user message）
	sess := engine.SessionManager.GetOrCreate("group:10001")
	deadline := time.Now().Add(2 * time.Second)
	for len(sess.Snapshot()) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(sess.Snapshot()) != 1 {
		t.Fatalf("期望第1轮后 session 历史有 1 条消息，实际=%d", len(sess.Snapshot()))
	}

	// 确认第1轮调用 LLM 时，requestPrompt 带有 proactive.PromptPrefix
	provider.mu.Lock()
	if len(provider.requests) < 1 {
		provider.mu.Unlock()
		t.Fatalf("期望第1轮触发 LLM 调用")
	}
	req1 := provider.requests[0]
	provider.mu.Unlock()

	lastMsg1, _ := req1.Messages[len(req1.Messages)-1].Content.(string)
	if !strings.Contains(lastMsg1, proactive.PromptPrefix) {
		t.Fatalf("期望第1轮 requestPrompt 包含主动回复引导词，实际: %s", lastMsg1)
	}

	// 验证持久化历史中的第1条消息纯净，不包含 proactive.PromptPrefix
	history1 := sess.Snapshot()
	content1, _ := history1[0].Content.(string)
	if strings.Contains(content1, proactive.PromptPrefix) {
		t.Fatalf("第1轮持久历史被主动回复指令污染: %s", content1)
	}

	// Turn 2: 用户显式 @ 机器人唤醒
	event2 := model.OneBotEvent{
		SelfID:      30001,
		PostType:    "message",
		MessageType: "group",
		GroupID:     10001,
		UserID:      20001,
		MessageID:   102,
		Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 请帮我写一份报告"}}]`),
	}
	b2, _ := json.Marshal(event2)
	if err := conn.WriteMessage(websocket.TextMessage, b2); err != nil {
		t.Fatalf("发送第二轮事件失败: %v", err)
	}

	// 读取第2轮响应（处理可能的前置 get_group_info）
	var action model.OneBotAction
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("读取第二轮响应失败: %v", err)
		}

		var act model.OneBotAction
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析第二轮响应失败: %v", err)
		}
		if act.Action == "get_group_info" {
			groupInfoResponse := map[string]any{
				"status":  "ok",
				"retcode": 0,
				"data": map[string]any{
					"group_id":   10001,
					"group_name": "测试群",
				},
				"echo": act.Echo,
			}
			b, _ := json.Marshal(groupInfoResponse)
			_ = conn.WriteMessage(websocket.TextMessage, b)
			continue
		}
		if act.Action == "send_group_msg" {
			action = act
			break
		}
	}
	if action.Action != "send_group_msg" {
		t.Fatalf("期望 action=send_group_msg, 实际=%s", action.Action)
	}

	// 验证第2轮发给 LLM 的请求上下文
	provider.mu.Lock()
	if len(provider.requests) < 2 {
		provider.mu.Unlock()
		t.Fatalf("期望第2轮触发 LLM 调用")
	}
	req2 := provider.requests[1]
	provider.mu.Unlock()

	// 校验 req2 中携带的上一轮历史消息，必须无主动回复提示词
	for i, m := range req2.Messages {
		contentStr, _ := m.Content.(string)
		if strings.Contains(contentStr, proactive.PromptPrefix) {
			t.Fatalf("第2轮 LLM 请求消息 [%d] (role=%s) 遭到主动回复前缀污染: %s", i, m.Role, contentStr)
		}
	}

	// 再次校验 session history 全量快照
	for i, m := range sess.Snapshot() {
		contentStr, _ := m.Content.(string)
		if strings.Contains(contentStr, proactive.PromptPrefix) {
			t.Fatalf("session history [%d] 遭到主动回复前缀污染: %s", i, contentStr)
		}
	}
}

func TestOneBotProactiveSecuritySilentDrop(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})

	t.Run("proactive roll hit with locked principal silently drops without direct reply", func(t *testing.T) {
		provider := &mockLLMProvider{}
		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = security.NewController(t.TempDir())

		// 将测试用户锁定在黑名单中
		principal, err := security.NewPrincipal("onebot", "20001")
		if err != nil {
			t.Fatalf("创建 principal 失败: %v", err)
		}
		if err := engine.Security.Lock(principal, "安全测试封禁"); err != nil {
			t.Fatalf("锁定用户失败: %v", err)
		}

		srv, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		// 发送未 @ 机器人的普通群消息（命中了主动回复概率）
		unaddressedEvent := model.OneBotEvent{
			SelfID:      30001,
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			MessageID:   201,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"普通群闲聊内容"}}]`),
		}
		b, _ := json.Marshal(unaddressedEvent)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		// 等待并检查是否有消息被发出（应该被静默丢弃，绝不能向群发送拒绝回复）
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, _, readErr := conn.ReadMessage()
		if readErr == nil {
			t.Fatalf("主动回复命中被安全拦截的消息应该被静默丢弃，但收到了一出站回复")
		}

		// LLM 也绝不能被调用
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if len(provider.requests) > 0 {
			t.Fatalf("安全拦截的消息不应调用 LLM")
		}
	})

	t.Run("proactive roll hit with security service failure silently drops", func(t *testing.T) {
		provider := &mockLLMProvider{}
		engine := newTestEngine(provider)
		engine.Scope = scope
		// 模拟安全服务异常 (AccessStore 为 nil，Fail-Closed)
		engine.Security = &security.Controller{
			Access:   nil,
			Watchdog: nil,
		}

		srv, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		unaddressedEvent := model.OneBotEvent{
			SelfID:      30001,
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			MessageID:   202,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"群闲聊消息"}}]`),
		}
		b, _ := json.Marshal(unaddressedEvent)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, _, readErr := conn.ReadMessage()
		if readErr == nil {
			t.Fatalf("安全服务异常时的主动回复应静默丢弃，但收到了一出站回复")
		}
	})

	t.Run("explicit wake with security block sends direct reject reply", func(t *testing.T) {
		provider := &mockLLMProvider{}
		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = security.NewController(t.TempDir())

		principal, err := security.NewPrincipal("onebot", "20001")
		if err != nil {
			t.Fatalf("创建 principal 失败: %v", err)
		}
		if err := engine.Security.Lock(principal, "安全测试封禁"); err != nil {
			t.Fatalf("锁定用户失败: %v", err)
		}

		srv, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		// 用户显式 @ 机器人
		explicitEvent := model.OneBotEvent{
			SelfID:      30001,
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			MessageID:   203,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 帮我回答问题"}}]`),
		}
		b, _ := json.Marshal(explicitEvent)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("显式唤醒被拦截应收到拒绝提示，但读取失败: %v", err)
		}

		var action model.OneBotAction
		if err := json.Unmarshal(respBytes, &action); err != nil {
			t.Fatalf("解析拒绝消息失败: %v", err)
		}
		if action.Action != "send_group_msg" {
			t.Fatalf("期望 action=send_group_msg, 实际=%s", action.Action)
		}
		params, ok := action.Params.(map[string]any)
		if !ok {
			t.Fatalf("params 格式错误: %T", action.Params)
		}
		msgStr, _ := params["message"].(string)
		if !strings.Contains(msgStr, "封禁") && !strings.Contains(msgStr, "security") {
			t.Fatalf("期望收到封禁拒绝提示，实际=%s", msgStr)
		}
	})
}

