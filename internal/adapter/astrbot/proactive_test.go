package astrbot

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/proactive"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestShouldReplyWithRNG(t *testing.T) {
	groupEvent := Event{
		Type:        "event",
		EventType:   "message",
		MessageType: "group",
		GroupID:     "10001",
		UserID:      "20001",
		Content:     "今天天气真好",
	}

	t.Run("disabled by default", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, nil)
		reply := shouldReplyWithRNG(&groupEvent, func() float64 { return 0.01 }, scope)
		if reply {
			t.Fatalf("expected shouldReply to be false when proactive reply is disabled")
		}
	})

	t.Run("proactive reply hits probability", func(t *testing.T) {
		ev := groupEvent
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.40",
		})
		reply := shouldReplyWithRNG(&ev, func() float64 { return 0.20 }, scope)
		if !reply {
			t.Fatalf("expected shouldReply to be true when proactive roll hits")
		}
		if ev.Metadata == nil || ev.Metadata["_frostagent_proactive_reply"] != true {
			t.Fatalf("expected _frostagent_proactive_reply to be true in metadata, got %+v", ev.Metadata)
		}
	})

	t.Run("proactive reply misses probability", func(t *testing.T) {
		ev := groupEvent
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.40",
		})
		reply := shouldReplyWithRNG(&ev, func() float64 { return 0.60 }, scope)
		if reply {
			t.Fatalf("expected shouldReply to be false when proactive roll misses")
		}
	})

	t.Run("suppressed by GROUP_REPLY_ON_MENTION=false", func(t *testing.T) {
		ev := groupEvent
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
			"GROUP_REPLY_ON_MENTION":      "false",
		})
		reply := shouldReplyWithRNG(&ev, func() float64 { return 0.01 }, scope)
		if reply {
			t.Fatalf("expected shouldReply to be false when GROUP_REPLY_ON_MENTION=false")
		}
	})

	t.Run("explicit wake does not flag proactive", func(t *testing.T) {
		wakeEvent := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			IsWake:      true,
			Content:     "今天天气真好",
		}
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
		})
		reply := shouldReplyWithRNG(&wakeEvent, func() float64 { return 0.01 }, scope)
		if !reply {
			t.Fatalf("expected shouldReply to be true for explicit wake")
		}
		if wakeEvent.Metadata != nil && wakeEvent.Metadata["_frostagent_proactive_reply"] == true {
			t.Fatalf("expected explicit wake not to be flagged as proactive")
		}
	})
}

func TestAstrBotProactiveMultiTurnHistoryPurity(t *testing.T) {
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

	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// Turn 1: 概率触发的主动回复群消息（未 @ 机器人、非显式唤醒）
	event1 := Event{
		Type:        "event",
		EventType:   "message",
		MessageType: "group",
		GroupID:     "10001",
		UserID:      "20001",
		MessageID:   "msg_proactive_101",
		Content:     "大家今天过得怎么样？",
		Platform:    "astrbot",
		Timestamp:   time.Now().Unix(),
	}
	b1, err := json.Marshal(event1)
	if err != nil {
		t.Fatalf("序列化第一轮事件失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, b1); err != nil {
		t.Fatalf("发送第一轮事件失败: %v", err)
	}

	// 第1轮触发 stay_silent，验证收到协议级 noop 控制帧且 suppress_llm=true，保证 AstrBot 插件不超时且抑制下游 LLM
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, respBytes1, readErr1 := conn.ReadMessage()
	if readErr1 != nil {
		t.Fatalf("读取第1轮静默终态控制帧失败: %v", readErr1)
	}
	var action1 Action
	if err := json.Unmarshal(respBytes1, &action1); err != nil {
		t.Fatalf("解析第1轮动作失败: %v", err)
	}
	if action1.Action != "noop" {
		t.Fatalf("期望第1轮动作类型为 noop，实际=%s", action1.Action)
	}
	if !action1.SuppressLLM {
		t.Fatalf("期望第1轮动作设置 suppress_llm=true")
	}
	if action1.Echo != "reply_msg_proactive_101" {
		t.Fatalf("期望第1轮 echo=reply_msg_proactive_101, 实际=%s", action1.Echo)
	}

	// 等待第1轮进入并处理完毕（由于 stay_silent，不向群发送出站消息，历史中写入 1 条 user message）
	sess := engine.SessionManager.GetOrCreate("astrbot:group:10001")
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

	// Turn 2: 用户显式唤醒机器人
	event2 := Event{
		Type:        "event",
		EventType:   "message",
		MessageType: "group",
		GroupID:     "10001",
		UserID:      "20001",
		MessageID:   "msg_proactive_102",
		Content:     "请帮我写一份报告",
		Platform:    "astrbot",
		IsWake:      true,
		Timestamp:   time.Now().Unix(),
	}
	b2, err := json.Marshal(event2)
	if err != nil {
		t.Fatalf("序列化第二轮事件失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, b2); err != nil {
		t.Fatalf("发送第二轮事件失败: %v", err)
	}

	// 读取第2轮响应
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取第二轮响应失败: %v", err)
	}

	var action Action
	if err := json.Unmarshal(respBytes, &action); err != nil {
		t.Fatalf("解析第二轮响应失败: %v", err)
	}
	if action.Action != "send_message" {
		t.Fatalf("期望 action=send_message, 实际=%s", action.Action)
	}
	if action.Content != "这是第二轮显式唤醒的正常回复。" {
		t.Fatalf("第二轮回复内容不符: %s", action.Content)
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

func TestAstrBotProactiveSecuritySilentDrop(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})

	t.Run("proactive roll hit with locked principal silently drops without direct reply", func(t *testing.T) {
		provider := &mockLLMProvider{}
		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = security.NewController(t.TempDir())

		// 将测试用户锁定在黑名单中
		principal, err := security.NewPrincipal("astrbot", "20001")
		if err != nil {
			t.Fatalf("创建 principal 失败: %v", err)
		}
		if err := engine.Security.Lock(principal, "安全测试封禁"); err != nil {
			t.Fatalf("锁定用户失败: %v", err)
		}

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		// 发送未显式唤醒的普通群消息（命中了主动回复概率）
		unaddressedEvent := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_sec_201",
			Content:     "普通群闲聊内容",
			Platform:    "astrbot",
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(unaddressedEvent)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		// 校验收到协议级 terminal noop 动作且 suppress_llm=true，绝不能向群发送可见的 send_message 回复
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("期望收到安全拦截的终态 noop 控制动作，但读取失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action == "send_message" {
			t.Fatalf("主动回复命中被安全拦截的消息绝不能向群发送可见回复: %s", act.Content)
		}
		if act.Action != "noop" || !act.SuppressLLM {
			t.Fatalf("期望终态动作 action=noop 且 suppress_llm=true, 实际=%+v", act)
		}
		if act.Echo != "reply_msg_sec_201" {
			t.Fatalf("期望 echo=reply_msg_sec_201, 实际=%s", act.Echo)
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

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		unaddressedEvent := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_sec_202",
			Content:     "群闲聊消息",
			Platform:    "astrbot",
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(unaddressedEvent)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		// 校验收到协议级 terminal noop 动作且 suppress_llm=true
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("期望收到安全服务异常时的终态 noop 控制动作，但读取失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action == "send_message" {
			t.Fatalf("安全服务异常时的主动回复绝不能发送可见回复: %s", act.Content)
		}
		if act.Action != "noop" || !act.SuppressLLM {
			t.Fatalf("期望 action=noop 且 suppress_llm=true, 实际=%+v", act)
		}
		if act.Echo != "reply_msg_sec_202" {
			t.Fatalf("期望 echo=reply_msg_sec_202, 实际=%s", act.Echo)
		}
	})

	t.Run("explicit wake with security block sends direct reject reply", func(t *testing.T) {
		provider := &mockLLMProvider{}
		engine := newTestEngine(provider)
		engine.Scope = scope
		engine.Security = security.NewController(t.TempDir())

		principal, err := security.NewPrincipal("astrbot", "20001")
		if err != nil {
			t.Fatalf("创建 principal 失败: %v", err)
		}
		if err := engine.Security.Lock(principal, "安全测试封禁"); err != nil {
			t.Fatalf("锁定用户失败: %v", err)
		}

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		// 用户显式唤醒机器人
		explicitEvent := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_sec_203",
			Content:     "帮我回答问题",
			Platform:    "astrbot",
			IsWake:      true,
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(explicitEvent)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("显式唤醒被拦截应收到拒绝提示，但读取失败: %v", err)
		}

		var action Action
		if err := json.Unmarshal(respBytes, &action); err != nil {
			t.Fatalf("解析拒绝消息失败: %v", err)
		}
		if action.Action != "send_message" {
			t.Fatalf("期望 action=send_message, 实际=%s", action.Action)
		}
		if action.Content != security.RejectGatewayMsg && !strings.Contains(action.Content, "封禁") && !strings.Contains(action.Content, "security") {
			t.Fatalf("期望收到封禁拒绝提示，实际=%s", action.Content)
		}
	})

	t.Run("ordinary unaddressed group message without proactive hit sends plain noop without suppress_llm", func(t *testing.T) {
		provider := &mockLLMProvider{}
		engine := newTestEngine(provider)
		// PROACTIVE_REPLY_PROBABILITY unset (disabled)
		engine.Scope = newTestScopeWithEnv(t, nil)

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		unaddressedEvent := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_unwoken_301",
			Content:     "普通群闲聊内容",
			Platform:    "astrbot",
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(unaddressedEvent)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("期望收到普通未唤醒群聊的普通 noop，但读取失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action != "noop" {
			t.Fatalf("期望 action=noop, 实际=%s", act.Action)
		}
		if act.SuppressLLM {
			t.Fatalf("普通未唤醒群聊的 noop 绝不能设置 suppress_llm=true, 需保持原生事件传播语义")
		}
		if act.Echo != "reply_msg_unwoken_301" {
			t.Fatalf("期望 echo=reply_msg_unwoken_301, 实际=%s", act.Echo)
		}
	})

	t.Run("proactive roll hit with empty final response sends noop with suppress_llm", func(t *testing.T) {
		provider := &mockLLMProvider{
			responses: []*core.ChatResponse{
				{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: "",
					},
				},
			},
		}
		engine := newTestEngine(provider)
		engine.Scope = scope

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		ev := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_empty_401",
			Content:     "闲聊触发空回复",
			Platform:    "astrbot",
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("期望收到空回复时的终态 noop，但读取失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action != "noop" {
			t.Fatalf("期望 action=noop, 实际=%s", act.Action)
		}
		if !act.SuppressLLM {
			t.Fatalf("主动回复空最终响应必须设置 suppress_llm=true")
		}
		if act.Echo != "reply_msg_empty_401" {
			t.Fatalf("期望 echo=reply_msg_empty_401, 实际=%s", act.Echo)
		}
	})
}

type mockAlcyoneState struct {
	mu           sync.Mutex
	reserveCount int
	commitCount  int
	releaseCount int
	reserveHandler func(w http.ResponseWriter, r *http.Request)
	commitHandler  func(w http.ResponseWriter, r *http.Request)
	releaseHandler func(w http.ResponseWriter, r *http.Request)
	balanceHandler func(w http.ResponseWriter, r *http.Request)
}

func mockAlcyoneBillingServer(t *testing.T) (*httptest.Server, *mockAlcyoneState) {
	t.Helper()
	state := &mockAlcyoneState{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/balance" {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.balanceHandler != nil {
				state.balanceHandler(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": billing.BalanceResult{
					Exists:       true,
					Platform:     "astrbot",
					ExternalID:   "20001",
					BalanceMinor: 10000,
				},
			})
			return
		}

		if r.URL.Path == "/v1/billing/llm/reservations" && r.Method == http.MethodPost {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.reserveHandler != nil {
				state.reserveHandler(w, r)
				return
			}
			var req billing.LLMReserveRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			state.reserveCount++
			resID := fmt.Sprintf("res_%d", state.reserveCount)
			result := billing.LLMReservationResult{
				ReservationID: resID,
				UserUID:       "user_mock_uid",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusReserved,
				ReservedMinor: req.AmountMinor,
				BalanceMinor:  10000,
			}
			json.NewEncoder(w).Encode(map[string]any{"data": result})
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v1/billing/llm/reservations/") && strings.HasSuffix(r.URL.Path, "/commit") {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.commitHandler != nil {
				state.commitHandler(w, r)
				return
			}
			var req map[string]int64
			json.NewDecoder(r.Body).Decode(&req)
			actualMinor := req["actual_minor"]
			state.commitCount++
			result := billing.LLMReservationResult{
				ReservationID: "res_mock",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusCommitted,
				BalanceMinor:  10000 - actualMinor,
			}
			json.NewEncoder(w).Encode(map[string]any{"data": result})
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v1/billing/llm/reservations/") && strings.HasSuffix(r.URL.Path, "/release") {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.releaseHandler != nil {
				state.releaseHandler(w, r)
				return
			}
			state.releaseCount++
			result := billing.LLMReservationResult{
				ReservationID: "res_mock",
				Decision:      billing.DecisionReserved,
				Status:        billing.StatusReleased,
				BalanceMinor:  10000,
			}
			json.NewEncoder(w).Encode(map[string]any{"data": result})
			return
		}

		http.NotFound(w, r)
	})

	srv := httptest.NewServer(handler)
	return srv, state
}

func TestAstrBotProactiveBillingExemption(t *testing.T) {
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

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		initialReserveCount := state.reserveCount
		initialCommitCount := state.commitCount

		event := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_bill_501",
			Content:     "大家有人在吗？",
			Platform:    "astrbot",
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("读取静默终态动作失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析终态动作失败: %v", err)
		}
		if act.Action != "noop" || !act.SuppressLLM {
			t.Fatalf("期望收到 action=noop 且 suppress_llm=true, 实际=%+v", act)
		}
		if act.Echo != "reply_msg_bill_501" {
			t.Fatalf("期望 echo=reply_msg_bill_501, 实际=%s", act.Echo)
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

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		initialReserveCount := state.reserveCount
		initialCommitCount := state.commitCount

		event := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_bill_502",
			Content:     "今天天气真好",
			Platform:    "astrbot",
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("读取回复动作失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action != "send_message" {
			t.Fatalf("期望 action=send_message, 实际=%s", act.Action)
		}
		if act.Content != "大家好，我是霜降！" {
			t.Fatalf("期望回复内容='大家好，我是霜降！', 实际=%s", act.Content)
		}

		if state.reserveCount != initialReserveCount {
			t.Fatalf("主动回复不应预扣费: 期望 reserveCount=%d, 实际=%d", initialReserveCount, state.reserveCount)
		}
		if state.commitCount != initialCommitCount {
			t.Fatalf("主动回复不应提交计费: 期望 commitCount=%d, 实际=%d", initialCommitCount, state.commitCount)
		}

		if strings.Contains(act.Content, "本次消耗") || strings.Contains(act.Content, "雪花") {
			t.Fatalf("主动回复不应附带计费账单回执，实际: %s", act.Content)
		}
	})

	t.Run("proactive reply with LLM error silently drops with suppressed noop", func(t *testing.T) {
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
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_bill_503",
			Content:     "今天有什么新鲜事？",
			Platform:    "astrbot",
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("期望收到异常时的终态 noop，但读取失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action == "send_message" {
			t.Fatalf("主动回复发生异常时不应向群发送可见消息: %s", act.Content)
		}
		if act.Action != "noop" || !act.SuppressLLM {
			t.Fatalf("期望收到 action=noop 且 suppress_llm=true, 实际=%+v", act)
		}
		if act.Echo != "reply_msg_bill_503" {
			t.Fatalf("期望 echo=reply_msg_bill_503, 实际=%s", act.Echo)
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

		srv, _, wsURL := startWSTestServer(engine)
		defer srv.Close()

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("WebSocket 连接失败: %v", err)
		}
		defer conn.Close()

		initialReserveCount := state.reserveCount
		initialCommitCount := state.commitCount

		event := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_bill_504",
			Content:     "帮我计算一下",
			Platform:    "astrbot",
			IsWake:      true,
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("读取显式唤醒响应失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析显式唤醒响应失败: %v", err)
		}
		if act.Action != "send_message" {
			t.Fatalf("期望 action=send_message, 实际=%s", act.Action)
		}

		if state.reserveCount <= initialReserveCount {
			t.Fatalf("显式唤醒必须走计费预留: initial=%d, actual=%d", initialReserveCount, state.reserveCount)
		}
		if state.commitCount <= initialCommitCount {
			t.Fatalf("显式唤醒必须走计费结算: initial=%d, actual=%d", initialCommitCount, state.commitCount)
		}

		if !strings.Contains(act.Content, "本次消耗") && !strings.Contains(act.Content, "雪花") {
			t.Fatalf("显式唤醒回复必须附带计费回执，实际: %s", act.Content)
		}
	})
}

func TestAstrBotProactiveSecurityOptionA(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})

	t.Run("unaddressed proactive turn attempting ban_user drops silently with suppressed noop and does not lock bystander", func(t *testing.T) {
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
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_optA_1",
			Content:     "普通群聊未艾特",
			Platform:    "astrbot",
			IsWake:      false,
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("读取动作失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action != "noop" || !act.SuppressLLM {
			t.Fatalf("期望收到 action=noop 且 suppress_llm=true, 实际=%+v", act)
		}
		if act.Echo != "reply_msg_optA_1" {
			t.Fatalf("期望 echo=reply_msg_optA_1, 实际=%s", act.Echo)
		}

		p, _ := security.NewPrincipal("astrbot", "20001")
		if ctrl.IsLocked(p) {
			t.Fatal("主动回复尝试 ban_user 不应封禁无辜旁观者")
		}
	})

	t.Run("unaddressed proactive turn with OutputBlocked drops silently with suppressed noop", func(t *testing.T) {
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
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_optA_2",
			Content:     "普通群聊",
			Platform:    "astrbot",
			IsWake:      false,
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("读取动作失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action != "noop" || !act.SuppressLLM {
			t.Fatalf("期望收到 action=noop 且 suppress_llm=true, 实际=%+v", act)
		}
		if act.Echo != "reply_msg_optA_2" {
			t.Fatalf("期望 echo=reply_msg_optA_2, 实际=%s", act.Echo)
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
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_optA_3",
			Content:     "帮我写点东西",
			Platform:    "astrbot",
			IsWake:      true,
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("读取动作失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}
		if act.Action != "send_message" {
			t.Fatalf("期望 action=send_message, 实际=%s", act.Action)
		}
		if !strings.Contains(act.Content, "FrostAgent安全控制：模型输出已拦截。") {
			t.Fatalf("显式唤醒拦截应发送安全提示，实际=%s", act.Content)
		}
	})

	t.Run("explicit wake turn with ban_user locks user and transmits security notice", func(t *testing.T) {
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
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			MessageID:   "msg_optA_4",
			Content:     "恶意提示词攻击",
			Platform:    "astrbot",
			IsWake:      true,
			Timestamp:   time.Now().Unix(),
		}
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("序列化事件失败: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			t.Fatalf("发送事件失败: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respBytes, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("读取动作失败: %v", readErr)
		}
		var act Action
		if err := json.Unmarshal(respBytes, &act); err != nil {
			t.Fatalf("解析动作失败: %v", err)
		}

		p, _ := security.NewPrincipal("astrbot", "20001")
		if !ctrl.IsLocked(p) {
			t.Fatal("显式唤醒 ban_user 必须成功锁定用户")
		}

		if act.Action != "send_message" {
			t.Fatalf("期望 action=send_message, 实际=%s", act.Action)
		}
		if !strings.Contains(act.Content, security.RejectGatewayMsg) {
			t.Fatalf("显式唤醒 ban_user 应发送封禁提示，实际=%s", act.Content)
		}
	})
}
