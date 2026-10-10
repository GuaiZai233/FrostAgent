package onebot

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"FrostAgent/internal/proactive"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestOneBotProactiveBillingExemption(t *testing.T) {
	alcyoneSrv, state := mockAlcyoneBillingServer(t)
	defer alcyoneSrv.Close()

	scope := newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})

	t.Run("proactive reply calling stay_silent incurs zero reservation or commit charge", func(t *testing.T) {
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
					Usage: &core.Usage{
						PromptTokens:     100,
						CompletionTokens: 20,
						TotalTokens:      120,
					},
				},
			},
		}
		engine := newTestEngine(provider)
		engine.Scope = scope
		staySilent := tools.StaySilentTool()
		engine.ToolRegistry[staySilent.Name()] = staySilent
		billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
		engine.BillingClient = billingClient
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

		initialReserveCount := state.reserveCount
		initialCommitCount := state.commitCount

		event := model.OneBotEvent{
			SelfID:      30001,
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			MessageID:   501,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"有人在吗？"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		// 等待处理完毕，处理可能的 get_group_info 预热，但绝不能收到 send_group_msg
		for {
			_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			_, respBytes, readErr := conn.ReadMessage()
			if readErr != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err == nil && act.Action == "get_group_info" {
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
				t.Fatalf("stay_silent 主动回复不应向群发送出站消息: %+v", act)
			}
		}

		if state.reserveCount != initialReserveCount {
			t.Fatalf("主动回复 stay_silent 不应扣费预留: 期望 reserveCount=%d, 实际=%d", initialReserveCount, state.reserveCount)
		}
		if state.commitCount != initialCommitCount {
			t.Fatalf("主动回复 stay_silent 不应提交扣费: 期望 commitCount=%d, 实际=%d", initialCommitCount, state.commitCount)
		}
	})

	t.Run("proactive reply answering freely without billing deduction or receipt", func(t *testing.T) {
		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: "大家好，我是霜降！",
					},
					Usage: &core.Usage{
						PromptTokens:     100,
						CompletionTokens: 30,
						TotalTokens:      130,
					},
				},
			},
		}
		engine := newTestEngine(provider)
		engine.Scope = scope
		billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
		engine.BillingClient = billingClient
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

		initialReserveCount := state.reserveCount
		initialCommitCount := state.commitCount

		event := model.OneBotEvent{
			SelfID:      30001,
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			MessageID:   502,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"今天天气真好"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		var action model.OneBotAction
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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

		if state.reserveCount != initialReserveCount {
			t.Fatalf("主动回复不应预扣费: 期望 reserveCount=%d, 实际=%d", initialReserveCount, state.reserveCount)
		}
		if state.commitCount != initialCommitCount {
			t.Fatalf("主动回复不应提交计费: 期望 commitCount=%d, 实际=%d", initialCommitCount, state.commitCount)
		}

		params, _ := action.Params.(map[string]any)
		msgStr, _ := params["message"].(string)
		if strings.Contains(msgStr, "本次消耗") || strings.Contains(msgStr, "雪花") {
			t.Fatalf("主动回复不应附带计费账单回执，实际: %s", msgStr)
		}
	})

	t.Run("proactive reply with LLM error silently drops without sending error to group", func(t *testing.T) {
		provider := &mockLLMProvider{
			errs: []error{errors.New("mock upstream timeout 504")},
		}
		engine := newTestEngine(provider)
		engine.Scope = scope
		billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
		engine.BillingClient = billingClient
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
			MessageID:   503,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"今天有什么新鲜事？"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			_, respBytes, readErr := conn.ReadMessage()
			if readErr != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err == nil && act.Action == "get_group_info" {
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
				t.Fatalf("主动回复发生异常时应静默丢弃，绝不能向群发送错误消息: %+v", act)
			}
		}
	})

	t.Run("explicit wake preserves standard billing reservation and receipt", func(t *testing.T) {
		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: "收到，马上为您处理！",
					},
					Usage: &core.Usage{
						PromptTokens:     200,
						CompletionTokens: 50,
						TotalTokens:      250,
					},
				},
			},
		}
		engine := newTestEngine(provider)
		engine.Scope = scope
		billingClient := billing.NewClient(alcyoneSrv.URL, "test-token", 2*time.Second)
		engine.BillingClient = billingClient
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

		initialReserveCount := state.reserveCount
		initialCommitCount := state.commitCount

		event := model.OneBotEvent{
			SelfID:      30001,
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			MessageID:   504,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 帮我计算一下"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		var action model.OneBotAction
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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

		if state.reserveCount <= initialReserveCount {
			t.Fatalf("显式唤醒必须走计费预留: initial=%d, actual=%d", initialReserveCount, state.reserveCount)
		}
		if state.commitCount <= initialCommitCount {
			t.Fatalf("显式唤醒必须走计费结算: initial=%d, actual=%d", initialCommitCount, state.commitCount)
		}

		params, _ := action.Params.(map[string]any)
		msgStr, _ := params["message"].(string)
		if !strings.Contains(msgStr, "本次消耗") && !strings.Contains(msgStr, "雪花") {
			t.Fatalf("显式唤醒回复必须附带计费回执，实际: %s", msgStr)
		}
	})
}

func TestOneBotProactiveSecurityOptionA(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})

	t.Run("unaddressed proactive turn attempting ban_user does not send group msg and does not lock bystander", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl := security.NewController(tmpDir)
		ctrl.SetMode(security.ControlModeAggressive)
		ctrl.Watchdog.SetClassifier(&mockClassifier{})

		banTool := tools.NewBanUserTool(ctrl)

		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{
							{
								ID:       "call_ban_1",
								Type:     "function",
								Function: core.ToolCallFunction{Name: security.BanUserToolName, Arguments: `{"reason":"proactive ban attempt"}`},
							},
						},
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = ctrl
		engine.ToolRegistry[security.BanUserToolName] = banTool

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
			MessageID:   601,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"普通群聊未艾特"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				t.Fatalf("主动回复尝试 ban_user 不应向群发送消息: %+v", act)
			}
		}

		p, _ := security.NewPrincipal("qq", "20001")
		if ctrl.IsLocked(p) {
			t.Fatal("主动回复尝试 ban_user 不应封禁无辜旁观者")
		}
	})

	t.Run("unaddressed proactive turn with OutputBlocked drops silently without sending group msg", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl := security.NewController(tmpDir)
		ctrl.SetMode(security.ControlModeAggressive)
		ctrl.Watchdog.SetClassifier(&mockClassifier{
			fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
				if input.Stage == security.StageModelOutput {
					return security.ClassificationResult{
						Category:  security.RiskCategoryViolenceTerrorism,
						RiskLevel: security.RiskLevelCritical,
						Reason:    "blocked output in proactive turn",
					}, nil
				}
				return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
			},
		})

		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: "恶意内容输出",
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = ctrl

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
			MessageID:   602,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"普通闲聊"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				t.Fatalf("主动回复 OutputBlocked 时不应向群发送消息: %+v", act)
			}
		}
	})

	t.Run("explicit wake turn with OutputBlocked transmits security notice", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl := security.NewController(tmpDir)
		ctrl.SetMode(security.ControlModeAggressive)
		ctrl.Watchdog.SetClassifier(&mockClassifier{
			fn: func(ctx context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
				if input.Stage == security.StageModelOutput {
					return security.ClassificationResult{
						Category:  security.RiskCategoryViolenceTerrorism,
						RiskLevel: security.RiskLevelCritical,
						Reason:    "blocked output in explicit wake",
					}, nil
				}
				return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
			},
		})

		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: "恶意内容输出",
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = ctrl

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
			MessageID:   603,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 帮我回答"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		var action model.OneBotAction
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				action = act
				break
			}
		}

		params, _ := action.Params.(map[string]any)
		msgStr, _ := params["message"].(string)
		if !strings.Contains(msgStr, "FrostAgent安全控制：模型输出已拦截。") {
			t.Fatalf("显式唤醒 OutputBlocked 时应发送拦截提示，实际: %s", msgStr)
		}
	})

	t.Run("explicit wake turn with ban_user locks user and sends notice", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl := security.NewController(tmpDir)
		ctrl.SetMode(security.ControlModeAggressive)
		ctrl.Watchdog.SetClassifier(&mockClassifier{})

		banTool := tools.NewBanUserTool(ctrl)

		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{
							{
								ID:       "call_ban_2",
								Type:     "function",
								Function: core.ToolCallFunction{Name: security.BanUserToolName, Arguments: `{"reason":"explicit prompt injection"}`},
							},
						},
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = ctrl
		engine.ToolRegistry[security.BanUserToolName] = banTool

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
			MessageID:   604,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 恶意攻击提示词"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		var action model.OneBotAction
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				action = act
				break
			}
		}

		p, _ := security.NewPrincipal("qq", "20001")
		if !ctrl.IsLocked(p) {
			t.Fatal("显式唤醒 ban_user 必须成功锁定用户")
		}

		params, _ := action.Params.(map[string]any)
		msgStr, _ := params["message"].(string)
		if !strings.Contains(msgStr, security.RejectGatewayMsg) {
			t.Fatalf("显式唤醒 ban_user 应发送封禁拒绝提示，实际: %s", msgStr)
		}
	})

	t.Run("unaddressed proactive turn attempting side-effect tool drops silently without sending group msg", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl := security.NewController(tmpDir)

		cmdExecuted := atomic.Int32{}
		cmdTool := &dummyTestTool{name: "execute_command", executed: &cmdExecuted}

		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{
							{
								ID:       "call_cmd_1",
								Type:     "function",
								Function: core.ToolCallFunction{Name: "execute_command", Arguments: `{"cmd":"id"}`},
							},
						},
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = ctrl
		engine.ToolRegistry["execute_command"] = cmdTool

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
			MessageID:   701,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"普通群闲聊触发命令调用"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				t.Fatalf("主动回复尝试调用副作用工具不应向群发送消息: %+v", act)
			}
		}

		if cmdExecuted.Load() != 0 {
			t.Fatalf("主动回复中的副作用工具绝对不可执行，实际执行了 %d 次", cmdExecuted.Load())
		}
	})

	t.Run("unaddressed proactive turn exhausting max iterations drops silently without sending group msg", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl := security.NewController(tmpDir)

		provider := &mockLLMProvider{
			customChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				return &core.ChatResponse{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{
							{
								ID:       "call_mem_loop",
								Type:     "function",
								Function: core.ToolCallFunction{Name: "memory", Arguments: `{"action":"list"}`},
							},
						},
					},
				}, nil
			},
		}

		memTool := &dummyTestTool{name: "memory"}

		engine := newTestEngine(provider)
		engine.MaxIterations = 2
		engine.Scope = scope
		engine.Security = ctrl
		engine.ToolRegistry["memory"] = memTool

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
			MessageID:   702,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"普通群闲聊导致迭代耗尽"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				t.Fatalf("主动回复耗尽最大迭代次数时不应向群发送报错消息: %+v", act)
			}
		}
	})

	t.Run("explicit wake turn exhausting max iterations sends error notice to group", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl := security.NewController(tmpDir)

		provider := &mockLLMProvider{
			customChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				return &core.ChatResponse{
					Message: core.ChatMessage{
						Role: core.RoleAssistant,
						ToolCalls: []core.ToolCall{
							{
								ID:       "call_mem_loop_exp",
								Type:     "function",
								Function: core.ToolCallFunction{Name: "memory", Arguments: `{"action":"list"}`},
							},
						},
					},
				}, nil
			},
		}

		memTool := &dummyTestTool{name: "memory"}

		engine := newTestEngine(provider)
		engine.MaxIterations = 2
		engine.Scope = scope
		engine.Security = ctrl
		engine.ToolRegistry["memory"] = memTool

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
			MessageID:   703,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 显式唤醒但导致迭代耗尽"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		var action model.OneBotAction
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				action = act
				break
			}
		}

		params, _ := action.Params.(map[string]any)
		msgStr, _ := params["message"].(string)
		if !strings.Contains(msgStr, "达到最大迭代次数") {
			t.Fatalf("显式唤醒耗尽迭代次数应向群发送最大迭代错误提示，实际: %s", msgStr)
		}
	})

	t.Run("unaddressed proactive turn with invalid quote message drops silently without sending group msg", func(t *testing.T) {
		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: `{"messages":[{"type":"quote","message_id":"unobserved-quote-999"}]}`,
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope

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
			MessageID:   704,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"普通群聊触发无效引用"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				t.Fatalf("主动回复引用校验失败时不应向群发送报错消息: %+v", act)
			}
		}
	})

	t.Run("unaddressed proactive turn with invalid media build output drops silently without sending group msg", func(t *testing.T) {
		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: `{"messages":[{"type":"image","path":"/nonexistent/missing_file_123.png"}]}`,
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope

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
			MessageID:   705,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"普通群聊触发无效媒体"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				t.Fatalf("主动回复组装媒体失败时不应向群发送报错消息: %+v", act)
			}
		}
	})

	t.Run("explicit wake turn with invalid quote message sends error notice to group", func(t *testing.T) {
		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: `{"messages":[{"type":"quote","message_id":"unobserved-quote-999"}]}`,
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope

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
			MessageID:   706,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 引用不存在的消息"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		var action model.OneBotAction
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				action = act
				break
			}
		}

		params, _ := action.Params.(map[string]any)
		msgStr, _ := params["message"].(string)
		if !strings.Contains(msgStr, "引用消息校验失败") {
			t.Fatalf("显式唤醒引用校验失败应向群发送错误提示，实际: %s", msgStr)
		}
	})

	t.Run("explicit wake turn with invalid media build output sends error notice to group", func(t *testing.T) {
		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: `{"messages":[{"type":"image","path":"/nonexistent/missing_file_123.png"}]}`,
					},
				},
			},
		}

		engine := newTestEngine(provider)
		engine.Scope = scope

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
			MessageID:   707,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":" 发送不存在的图片"}}]`),
		}
		b, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		var action model.OneBotAction
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, respBytes, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			var act model.OneBotAction
			if err := json.Unmarshal(respBytes, &act); err != nil {
				t.Fatalf("解析响应失败: %v", err)
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
				respB, _ := json.Marshal(groupInfoResponse)
				_ = conn.WriteMessage(websocket.TextMessage, respB)
				continue
			}
			if act.Action == "send_group_msg" {
				action = act
				break
			}
		}

		params, _ := action.Params.(map[string]any)
		msgStr, _ := params["message"].(string)
		if !strings.Contains(msgStr, "组装消息失败") {
			t.Fatalf("显式唤醒组装媒体失败应向群发送错误提示，实际: %s", msgStr)
		}
	})
}

type dummyTestTool struct {
	name     string
	executed *atomic.Int32
}

func (d *dummyTestTool) Name() string                 { return d.name }
func (d *dummyTestTool) Description() string          { return "dummy test tool" }
func (d *dummyTestTool) Parameters() map[string]any   { return map[string]any{} }
func (d *dummyTestTool) Execute(args string) (string, error) {
	if d.executed != nil {
		d.executed.Add(1)
	}
	return "ok", nil
}

